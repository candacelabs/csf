// Copyright 2026 Candace Labs

package intake

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/gin-gonic/gin"

	"github.com/candacelabs/csf/io/kernel/clock"
	intakev1 "github.com/candacelabs/csf/proto/candace/intake/v1"
	"github.com/candacelabs/csf/runtime"
)

// The webhook route and the headers GitHub sends on every delivery.
const (
	// WebhookPath is the one route GitHub's webhooks are delivered to.
	WebhookPath = "/api/intake/github"
	// HeaderSignature carries the HMAC-SHA256 of the body under the
	// webhook's secret, as sha256=<hex>.
	HeaderSignature = "X-Hub-Signature-256"
	// HeaderEvent names the delivery's event: pull_request, push, ...
	HeaderEvent = "X-GitHub-Event"
	// HeaderDelivery is the delivery's GUID, the same on every redelivery.
	HeaderDelivery = "X-GitHub-Delivery"

	signaturePrefix = "sha256="
	// MaxDeliveryBytes is GitHub's own cap on a webhook payload.
	MaxDeliveryBytes = 25 << 20
)

// Refusal is why the receiver refused a delivery. Each is a stable value the
// answer, the stream and the refused-deliveries series carry.
type Refusal string

const (
	// RefusedNoSecret: providers.json holds no github.webhook_secret, so no
	// delivery can be verified and every one is refused.
	RefusedNoSecret Refusal = "no_webhook_secret"
	// RefusedSignature: X-Hub-Signature-256 is missing, malformed, or not
	// the body's HMAC under the secret.
	RefusedSignature Refusal = "bad_signature"
	// RefusedDelivery: X-GitHub-Delivery or X-GitHub-Event is missing or
	// malformed.
	RefusedDelivery Refusal = "bad_delivery"
	// RefusedTooLarge: the body exceeds MaxDeliveryBytes.
	RefusedTooLarge Refusal = "too_large"
)

// Outcome is how the receiver ended one delivery.
type Outcome string

const (
	// OutcomeAccepted: verified, recorded, and its events routed.
	OutcomeAccepted Outcome = "accepted"
	// OutcomeDuplicate: a delivery with this GUID was accepted before.
	OutcomeDuplicate Outcome = "duplicate"
	// OutcomeIgnored: verified and recorded, but of an event or action CSF
	// does not act on.
	OutcomeIgnored Outcome = "ignored"
	// OutcomeRefused: not verified; see the Refusal.
	OutcomeRefused Outcome = "refused"
	// OutcomeFailed: verified, but it could not be recorded; GitHub sees a
	// failure and the delivery is recovered by redelivery.
	OutcomeFailed Outcome = "failed"
)

var (
	// ErrNoSecret is what a secret source returns when no webhook secret is
	// configured.
	ErrNoSecret = errors.New("intake: no GitHub webhook secret is configured")
	// ErrNoStateDirectory reports a receiver built without its state
	// directory.
	ErrNoStateDirectory = errors.New("intake: WithStateDirectory is required")
	// ErrNoSecretSource reports a receiver built without a secret source.
	ErrNoSecretSource = errors.New("intake: WithWebhookSecret is required")
)

// deliveryPattern is the shape of GitHub's delivery GUIDs; anything else is
// refused before it names a file.
var deliveryPattern = regexp.MustCompile(`^[0-9A-Fa-f-]{1,64}$`)

// eventNamePattern is the shape of an X-GitHub-Event name.
var eventNamePattern = regexp.MustCompile(`^[a-z_]{1,64}$`)

// Answer is the receiver's JSON answer to one delivery.
type Answer struct {
	Delivery string   `json:"delivery,omitempty"`
	Event    string   `json:"event,omitempty"`
	Outcome  Outcome  `json:"outcome"`
	Refusal  Refusal  `json:"refusal,omitempty"`
	Events   []string `json:"events,omitempty"`
}

// Routed is what one route did with one event; the zero value means the route
// does not apply to it.
type Routed struct {
	// Target names what received the event: a session, the dispatcher, ...
	Target string
	// Detail is the target's own identifier: an assignment, a slice.
	Detail string
}

// EventRoute takes one accepted event to whoever acts on it. Routes are
// registered in data ([WithEventRoutes]); the receiver runs every route on
// every event and records what each did.
type EventRoute func(ctx context.Context, event *intakev1.Event) (Routed, error)

// WebhookReceiver verifies GitHub's webhook deliveries, records each on the
// GitHub event stream, and routes the events they carry.
type WebhookReceiver struct {
	state    string
	clock    clock.IClock
	stream   *Stream
	secret   func() ([]byte, error)
	routes   []EventRoute
	recovery *recovery
	logger   *slog.Logger
	counts   *Counts
	started  atomic.Bool
}

// WebhookOption configures a [WebhookReceiver].
type WebhookOption func(receiver *WebhookReceiver) error

// WithStateDirectory names the harness state directory the GitHub event
// stream lives in. Required.
func WithStateDirectory(directory string) WebhookOption {
	return func(receiver *WebhookReceiver) error {
		if directory == "" {
			return ErrNoStateDirectory
		}
		receiver.state = directory
		return nil
	}
}

// WithWebhookClock replaces the system clock records and latencies are
// measured on.
func WithWebhookClock(source clock.IClock) WebhookOption {
	return func(receiver *WebhookReceiver) error {
		if source == nil {
			return errors.New("intake: WithWebhookClock needs a clock")
		}
		receiver.clock = source
		return nil
	}
}

// WithWebhookSecret grants the source of the webhook secret, read at every
// delivery so a secret added later takes effect without a restart. A source
// returning [ErrNoSecret] or an empty secret makes the receiver refuse every
// delivery. Required.
func WithWebhookSecret(source func() ([]byte, error)) WebhookOption {
	return func(receiver *WebhookReceiver) error {
		if source == nil {
			return ErrNoSecretSource
		}
		receiver.secret = source
		return nil
	}
}

// WithEventRoutes adds routes every accepted event is offered to.
func WithEventRoutes(routes ...EventRoute) WebhookOption {
	return func(receiver *WebhookReceiver) error {
		for index, route := range routes {
			if route == nil {
				return fmt.Errorf("intake: event route %d is nil", index)
			}
		}
		receiver.routes = append(receiver.routes, routes...)
		return nil
	}
}

// WithWebhookLogger receives one record per refused or failed delivery.
func WithWebhookLogger(logger *slog.Logger) WebhookOption {
	return func(receiver *WebhookReceiver) error {
		if logger == nil {
			return errors.New("intake: WithWebhookLogger needs a logger")
		}
		receiver.logger = logger
		return nil
	}
}

// NewWebhookReceiver validates the whole option set before building the
// receiver.
func NewWebhookReceiver(options ...WebhookOption) (*WebhookReceiver, error) {
	receiver := &WebhookReceiver{logger: slog.New(slog.DiscardHandler), counts: NewDeliveryCounts(), clock: clock.NewSystemClock()}
	for index, option := range options {
		if option == nil {
			return nil, fmt.Errorf("intake: webhook option %d is nil", index)
		}
		if err := option(receiver); err != nil {
			return nil, err
		}
	}
	switch {
	case receiver.state == "":
		return nil, ErrNoStateDirectory
	case receiver.secret == nil:
		return nil, ErrNoSecretSource
	}
	stream, err := NewStream(receiver.state, receiver.clock)
	if err != nil {
		return nil, err
	}
	receiver.stream = stream
	if receiver.recovery != nil {
		receiver.recovery.stream = receiver.stream
		receiver.recovery.logger = receiver.logger
	}
	return receiver, nil
}

var _ runtime.IService = (*WebhookReceiver)(nil)

// Start recovers the deliveries missed while this host was down, when
// recovery was granted, in one goroutine on scope.
func (receiver *WebhookReceiver) Start(scope *runtime.Scope) error {
	if scope == nil {
		return errors.New("intake: Start needs a scope")
	}
	if !receiver.started.CompareAndSwap(false, true) {
		return ErrAlreadyStarted
	}
	if receiver.recovery == nil {
		return nil
	}
	return scope.Go(func(ctx context.Context) error {
		report := receiver.recovery.recover(ctx)
		receiver.logger.Info("intake: webhook recovery done", "redelivered", report.Redelivered, "failed", report.Failed)
		return nil
	})
}

// Counts is what the receiver has done since it was built.
func (receiver *WebhookReceiver) Counts() *Counts { return receiver.counts }

// Register mounts the webhook route on the caller's router.
func (receiver *WebhookReceiver) Register(router gin.IRouter) {
	router.POST(WebhookPath, receiver.serve)
}

func (receiver *WebhookReceiver) serve(request *gin.Context) {
	status, answer := receiver.Receive(request.Request.Context(), request.Request.Header, request.Request.Body)
	request.JSON(status, answer)
}

// Receive handles one delivery: its headers and body as GitHub sent them.
// It returns the HTTP status and the answer GitHub is sent.
func (receiver *WebhookReceiver) Receive(ctx context.Context, header http.Header, body io.Reader) (int, Answer) {
	answer := Answer{Delivery: header.Get(HeaderDelivery), Event: header.Get(HeaderEvent)}
	content, err := io.ReadAll(io.LimitReader(body, MaxDeliveryBytes+1))
	switch {
	case err != nil:
		return receiver.refuse(ctx, http.StatusBadRequest, answer, RefusedDelivery, err)
	case len(content) > MaxDeliveryBytes:
		return receiver.refuse(ctx, http.StatusRequestEntityTooLarge, answer, RefusedTooLarge, nil)
	}
	secret, err := receiver.secret()
	if err != nil || len(secret) == 0 {
		return receiver.refuse(ctx, http.StatusServiceUnavailable, answer, RefusedNoSecret, err)
	}
	if !signed(header.Get(HeaderSignature), content, secret) {
		return receiver.refuse(ctx, http.StatusUnauthorized, answer, RefusedSignature, nil)
	}
	if !deliveryPattern.MatchString(answer.Delivery) || !eventNamePattern.MatchString(answer.Event) {
		return receiver.refuse(ctx, http.StatusBadRequest, answer, RefusedDelivery, nil)
	}
	return receiver.accept(ctx, answer, content)
}

// signed reports whether signature is sha256=<hex> of the HMAC-SHA256 of body
// under secret, compared in constant time.
func signed(signature string, body []byte, secret []byte) bool {
	encoded, found := strings.CutPrefix(signature, signaturePrefix)
	if !found {
		return false
	}
	claimed, err := hex.DecodeString(encoded)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(claimed, mac.Sum(nil))
}

// Sign is the X-Hub-Signature-256 value GitHub sends for body under secret:
// what a replay of a recorded delivery signs it with.
func Sign(body []byte, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return signaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// accept records a verified delivery once, then routes its events. A
// delivery whose raw copy cannot be kept fails, so GitHub records the
// failure and recovery redelivers it.
func (receiver *WebhookReceiver) accept(ctx context.Context, answer Answer, content []byte) (int, Answer) {
	kept, err := receiver.stream.Keep(answer.Delivery, content)
	if err != nil {
		answer.Outcome = OutcomeFailed
		receiver.counts.delivery(answer.Outcome, "")
		receiver.logger.Warn("intake: delivery not kept", "delivery", answer.Delivery, "error", err)
		return http.StatusInternalServerError, answer
	}
	if !kept {
		answer.Outcome = OutcomeDuplicate
		receiver.counts.delivery(answer.Outcome, "")
		receiver.stream.RecordDelivery(ctx, answer, nil)
		return http.StatusOK, answer
	}
	events, handled, err := NormalizeDelivery(answer.Event, answer.Delivery, content)
	switch {
	case err != nil:
		answer.Outcome = OutcomeFailed
		receiver.stream.RecordDelivery(ctx, answer, err)
		receiver.counts.delivery(answer.Outcome, "")
		return http.StatusUnprocessableEntity, answer
	case !handled || len(events) == 0:
		answer.Outcome = OutcomeIgnored
	default:
		answer.Outcome = OutcomeAccepted
	}
	receiver.stream.RecordDelivery(ctx, answer, nil)
	receiver.counts.delivery(answer.Outcome, "")
	for _, event := range events {
		answer.Events = append(answer.Events, kindWord(event.GetKind()))
		receiver.stream.RecordEvent(ctx, event)
		receiver.counts.event(event, receiver.stream.now())
		receiver.route(ctx, event)
	}
	return http.StatusAccepted, answer
}

// route offers one event to every route and records what each did. A route
// that fails is recorded and logged; it never fails the delivery, which is
// already kept.
func (receiver *WebhookReceiver) route(ctx context.Context, event *intakev1.Event) {
	for _, route := range receiver.routes {
		routed, err := route(ctx, event)
		if err != nil {
			receiver.logger.Warn("intake: event not routed", "event", event.GetId(), "error", err)
		}
		if err != nil || routed.Target != "" {
			receiver.stream.RecordRouted(ctx, event, routed, err)
		}
	}
}

func (receiver *WebhookReceiver) refuse(ctx context.Context, status int, answer Answer, refusal Refusal, cause error) (int, Answer) {
	answer.Outcome, answer.Refusal = OutcomeRefused, refusal
	receiver.counts.delivery(answer.Outcome, refusal)
	receiver.stream.RecordDelivery(ctx, answer, cause)
	receiver.logger.Warn("intake: delivery refused", "delivery", answer.Delivery, "event", answer.Event, "refusal", string(refusal))
	return status, answer
}

// fileExists reports whether path names an existing file.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// payloadPath is where one delivery's raw body is kept.
func payloadPath(directory string, delivery string) string {
	return filepath.Join(directory, DeliveriesDirectory, delivery+payloadSuffix)
}
