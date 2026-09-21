// Package payments is the compiled source for the idempotency section of
// docs/guide/effects-and-server-push.md.
//
// It is a checkout: a Pay button, an effect that moves money through a third
// party, and the idempotency key that makes a second execution of that effect
// harmless.
//
// It is deliberately not a counter. An idempotency mistake on a counter shows
// up as a number that is one too high and nobody notices; the same mistake here
// is a second charge on somebody's card, which is where PRD R-12 says the
// obligation actually gets found. Everything in this file that looks like
// ceremony is the ceremony that stops that.
package payments

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

const (
	// EventPay is the Pay button. Two clicks are two of these, and the library
	// will not collapse them — that is the contract this package exists to
	// survive, not a defect to work around.
	EventPay = "checkout.pay"

	// EventCharged is emitted by the executor once the provider has answered.
	EventCharged = "checkout.charged"

	FieldChargeID = "charge_id"
	FieldOrderID  = "order_id"

	// SourceCharge reaches the wire as the origin source "effect:checkout.charge"
	// on every patch the charge causes, and is the value a failure event carries
	// in live.EffectFailedSourceField.
	SourceCharge = "checkout.charge"
)

// The checkout's three states. Status is what the page renders and what the
// reducer's in-session guard reads.
const (
	StatusOpen     = "open"
	StatusCharging = "charging"
	StatusCharged  = "charged"
)

// State is one session's view of one checkout.
//
// OrderID and AmountCents are not typed here for the session's benefit: they
// are the two values the idempotency key is derived from, so they have to be
// state a reconnecting session can rebuild from the application's own store
// rather than anything the connection carried.
type State struct {
	OrderID     string
	AmountCents int64
	Status      string
	ChargeID    string
}

// ChargeIntent is one request to move money, as the reducer decides it.
//
// Key is the whole point. It is decided by the REDUCER rather than minted
// inside the effect's Run, because the reducer is the deterministic half: the
// same state and the same event produce the same key on every replay, where a
// key minted at the actor boundary would be a fresh one per attempt and the
// retry it exists to make safe would charge twice.
type ChargeIntent struct {
	OrderID     string
	AmountCents int64
	Key         string
}

// IdempotencyKey is this package's one load-bearing function.
//
// It is derived from the ORDER — the thing the customer means to pay for once —
// and from nothing else. In particular it is derived from none of:
//
//   - the event that scheduled the effect. Two clicks are two events with two
//     different live.Event.ID values, so a key built from one charges twice for
//     the double-click it was supposed to stop.
//   - the session. A session dies with its connection (RFC-0001 §7.1) and a
//     reconnect gets a fresh one, so a key built from live.Session[live.AnonymousIdentity].ID differs
//     across exactly the retry it was supposed to stop.
//   - a clock or a random source. Either makes the key unreproducible, which is
//     the same as having no key, and neither is available to a reducer anyway.
//
// What is left is the application's own domain, which is where PRD FR-77(b)
// says the key belongs.
func IdempotencyKey(orderID string, amountCents int64) string {
	sum := sha256.Sum256([]byte(orderID + "\x00" + strconv.FormatInt(amountCents, 10)))
	return "checkout-" + hex.EncodeToString(sum[:16])
}

// charge is the request this state would schedule, and it is a method so that
// every site that schedules one mints the key the same way.
func (s State) charge() ChargeIntent {
	return ChargeIntent{
		OrderID:     s.OrderID,
		AmountCents: s.AmountCents,
		Key:         IdempotencyKey(s.OrderID, s.AmountCents),
	}
}

// Reducer returns a pure transition that does two separate things about double
// execution.
//
// The Status guard is the cheap one: within one live session, transitions are
// serialised on the session's goroutine, so the second click of a double-click
// sees StatusCharging and schedules nothing. That is real and it is worth
// having, and it is NOT the mechanism — it is in-process state, and a customer
// who reconnects gets a fresh session whose Init rebuilds this struct from the
// order. The key is what survives that; the guard is what saves a round trip.
func Reducer(gateway *Gateway) live.Reducer[State, live.AnonymousIdentity] {
	return func(s State, ev live.Event) (State, []live.Effect[live.AnonymousIdentity]) {
		switch ev.Name {
		case EventPay:
			if s.Status != StatusOpen {
				return s, nil
			}
			s.Status = StatusCharging
			return s, []live.Effect[live.AnonymousIdentity]{gateway.ChargeEffect(s.charge())}

		case EventCharged:
			s.Status = StatusCharged
			s.ChargeID = ev.Fields.Get(FieldChargeID)
			return s, nil

		case live.EffectFailedEvent:
			return failedCharge(gateway, s, ev)
		}
		return s, nil
	}
}

// failedCharge decides what a failed charge does to the checkout.
//
// A retryable failure re-schedules the same effect, which mints the same key,
// so the provider answers the retry with the charge it already made rather than
// making a second one. A terminal failure reopens the checkout and lets the
// customer press Pay again — same key, same answer. Both branches are only
// honest because of IdempotencyKey; without it this reducer would be a
// double-charge generator with good manners.
func failedCharge(gateway *Gateway, s State, ev live.Event) (State, []live.Effect[live.AnonymousIdentity]) {
	if ev.Fields.Get(live.EffectFailedSourceField) != SourceCharge {
		return s, nil
	}
	if retryable, _ := strconv.ParseBool(ev.Fields.Get(live.EffectFailedRetryableField)); retryable {
		return s, []live.Effect[live.AnonymousIdentity]{gateway.ChargeEffect(s.charge())}
	}
	s.Status = StatusOpen
	return s, nil
}

// Gateway is the application's side of a payment provider.
type Gateway struct {
	Provider Provider
}

// ChargeEffect performs the charge at the actor boundary.
//
// The failure to notice here is the emit: the money has moved by the time that
// line runs, so an emit that fails is the second double-execution path
// happening inside one process — committed externally, and the session never
// learned. Marking it retryable is a claim about idempotence, and this effect
// is entitled to make it because it passed a key the reducer derived from the
// order. Without the key the honest classification would be terminal, and the
// customer would be looking at a checkout that had already taken their money.
func (g *Gateway) ChargeEffect(request ChargeIntent) live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: SourceCharge,
		Run: func(ctx context.Context, sess live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			charge, err := g.Provider.Charge(ctx, ChargeRequest{
				IdempotencyKey: request.Key,
				OrderID:        request.OrderID,
				AmountCents:    request.AmountCents,
			})
			if err != nil {
				return fmt.Errorf("payments: charging order %s: %w", request.OrderID, err)
			}
			if err := emit(live.Event{
				Name: EventCharged,
				Fields: live.NewFields(map[string]string{
					FieldChargeID: charge.ID,
					FieldOrderID:  request.OrderID,
				}),
			}); err != nil {
				return live.Retryable(fmt.Errorf(
					"payments: order %s was charged as %s and the session did not learn: %w",
					request.OrderID, charge.ID, err))
			}
			return nil
		},
	}
}

// Provider is the third party that actually moves the money.
type Provider interface {
	Charge(context.Context, ChargeRequest) (Charge, error)
}

// ChargeRequest is what a provider is asked for. IdempotencyKey is the field
// every real payment API has under one name or another, and the one this whole
// package is about.
type ChargeRequest struct {
	IdempotencyKey string
	OrderID        string
	AmountCents    int64
}

// Charge is what came back.
type Charge struct {
	ID          string
	OrderID     string
	AmountCents int64
}

// KeyedProvider is a stand-in for a real gateway, and it is here so the
// documentation's claim is executable rather than asserted: it collapses
// repeated keys the way Stripe, Adyen and PayPal all do.
//
// The important line is that the record it consults is ITS OWN. Any check the
// application performs before calling out is a check against a row the
// application wrote; the key is a check against the row that took the money.
// Those are the same row only when nothing went wrong in between.
type KeyedProvider struct {
	mu    sync.Mutex
	byKey map[string]Charge
	made  int
}

func NewKeyedProvider() *KeyedProvider {
	return &KeyedProvider{byKey: map[string]Charge{}}
}

// Charge returns the existing charge for a key it has seen, and makes a new one
// otherwise. A provider with no key to go on has no way to tell the two apart
// and makes two charges, which is the failure this page is about.
func (p *KeyedProvider) Charge(_ context.Context, req ChargeRequest) (Charge, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if existing, ok := p.byKey[req.IdempotencyKey]; ok {
		return existing, nil
	}
	p.made++
	charge := Charge{
		ID:          fmt.Sprintf("ch_%d", p.made),
		OrderID:     req.OrderID,
		AmountCents: req.AmountCents,
	}
	p.byKey[req.IdempotencyKey] = charge
	return charge, nil
}

// Made is how many distinct charges this provider actually made, which is the
// number a specification about double execution has to assert on.
func (p *KeyedProvider) Made() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.made
}
