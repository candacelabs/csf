package blobfish

import (
	"context"
	"math/rand/v2"
	"time"
)

// A replica at runtime is a goroutine owning its own generation counter and its
// own object count. No other goroutine can name either, which is why nothing in
// this package locks anything and there is nothing for a lock to guard.

// zoneCount is how many zones every object is replicated to. It is a constant
// rather than a configuration field because the card beside this engine draws
// three and says "of 3" in a stat line; a store with a different width would be
// a different card.
const zoneCount = 3

// quorum is the strict majority two writes of one object must overlap on:
// zoneCount/2+1. Two majorities of one set share a member, which is why a read
// of a quorum can never miss a write a quorum acknowledged.
const quorum = zoneCount/2 + 1

// opKind names one thing a replica can be asked to do.
type opKind uint8

const (
	// opWrite is a new generation from the coordinator.
	opWrite opKind = iota

	// opRead is the coordinator asking a zone to answer for a read.
	opRead

	// opRepair is the repairer bringing a zone up to a generation it missed.
	opRepair

	// opProbe is the repairer asking a zone what generation it is on.
	opProbe
)

// String names each kind, so a failure about one says something.
func (kind opKind) String() string {
	if name, named := opNames[kind]; named {
		return name
	}
	return "op " + string(rune('0'+int(kind)))
}

// opNames is the vocabulary, and the only place a kind's name is spelled.
var opNames = map[opKind]string{
	opWrite:  "write",
	opRead:   "read",
	opRepair: "repair",
	opProbe:  "probe",
}

// replicaOp is one thing a zone was asked to do.
type replicaOp struct {
	// Kind is which rule runs.
	Kind opKind

	// Generation is the generation being written or repaired to.
	Generation uint64

	// Reply is where a probe is answered, and is nil for everything else. It
	// travels with the request, so a zone's generation is never read from
	// outside the zone's own goroutine — it is asked for and told.
	Reply chan generationReport
}

// replicaAck is one zone answering the coordinator on the shared channel.
type replicaAck struct {
	// Zone is who answered.
	Zone int

	// Generation is what it answered about. The coordinator counts only the
	// acknowledgements that match the generation it is collecting, so an
	// acknowledgement that arrives after the quorum was reached is a fact about
	// a write that has already been answered and is discarded.
	Generation uint64

	// Kind distinguishes a write acknowledgement from a read one, because both
	// arrive on one channel and R and W are counted separately.
	Kind opKind
}

// generationReport is one zone answering a probe.
type generationReport struct {
	// Zone is who answered, and Generation is where it has got to.
	Zone       int
	Generation uint64
}

// replicaState is one zone's whole state: a goroutine local, copied into and
// out of every rule.
type replicaState struct {
	// Zone is this replica's index.
	Zone int

	// Generation is the highest generation this zone has stored. It only
	// increases.
	Generation uint64

	// Objects is how many distinct objects this zone holds.
	Objects int
}

// replicaOutcome is what one rule decided: the next state, and the two things
// the goroutine around it has to do that a pure function cannot.
type replicaOutcome struct {
	// State is the zone after the operation.
	State replicaState

	// Ack is whether to answer the coordinator on the shared channel.
	Ack bool

	// Report is whether to answer the probe on its own reply channel.
	Report bool

	// Accepted is whether the operation moved the zone. It is what makes "a
	// replica accepts a repair only for a generation greater than its own"
	// checkable rather than asserted.
	Accepted bool
}

// replicaRule is what one kind of operation does to a zone: a pure function
// from a state and an operation to the next state and what to say about it.
//
// The four kinds are a family of one signature, so they are a registry rather
// than a switch: a fifth kind is an entry here, and a specification can assert
// that every kind has exactly one rule — which is unwritable against a switch,
// and without which a fifth kind is an operation every zone silently ignores.
type replicaRule func(state replicaState, op replicaOp) replicaOutcome

// replicaRules is the table, one entry per kind.
var replicaRules = map[opKind]replicaRule{
	opWrite: func(state replicaState, op replicaOp) replicaOutcome {
		if op.Generation <= state.Generation {
			return replicaOutcome{State: state, Ack: true}
		}
		next := state
		next.Generation = op.Generation
		next.Objects++
		return replicaOutcome{State: next, Ack: true, Accepted: true}
	},

	opRead: func(state replicaState, op replicaOp) replicaOutcome {
		// A read moves nothing. It is here as a rule rather than as a special
		// case so that R and W are collected the same way, on one channel, by
		// one coordinator that stops counting at the same place.
		return replicaOutcome{State: state, Ack: true, Accepted: true}
	},

	opRepair: func(state replicaState, op replicaOp) replicaOutcome {
		// The zone the quorum did not wait for, catching up. It accepts only a
		// generation greater than its own — a repair that could move a zone
		// backwards would be a repair that loses a write.
		if op.Generation <= state.Generation {
			return replicaOutcome{State: state}
		}
		next := state
		next.Generation = op.Generation
		return replicaOutcome{State: next, Accepted: true}
	},

	opProbe: func(state replicaState, op replicaOp) replicaOutcome {
		return replicaOutcome{State: state, Report: true, Accepted: true}
	},
}

// replica is one zone: one goroutine, one inbox, one state nothing else sees.
type replica struct {
	// id is this zone's index.
	id int

	// inbox is what the coordinator and the repairer send it. It has two
	// writers and no closer, which is what a channel is for.
	inbox <-chan replicaOp

	// acks is the coordinator's shared collection point.
	acks chan<- replicaAck

	// delay is how long this zone takes to answer, base plus its own jitter.
	// It is the whole of what makes one zone eventually consistent: the
	// coordinator does not wait for it, and nothing else about it is different.
	delay time.Duration

	// spread is the width of this zone's own jitter.
	spread time.Duration

	// jitter is this zone's own random stream.
	jitter *rand.Rand
}

// run is the zone's whole life: take one operation, wait its own latency, apply
// the rule, and say whatever the rule said to say.
func (zone *replica) run(ctx context.Context) {
	state := replicaState{Zone: zone.id}

	for {
		var op replicaOp
		select {
		case <-ctx.Done():
			return
		case op = <-zone.inbox:
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(zone.latency()):
		}

		outcome := replicaRules[op.Kind](state, op)
		state = outcome.State

		if outcome.Ack {
			select {
			case zone.acks <- replicaAck{Zone: zone.id, Generation: op.Generation, Kind: op.Kind}:
			case <-ctx.Done():
				return
			}
		}
		if outcome.Report && op.Reply != nil {
			// The probe's reply channel is buffered by one and read by exactly
			// one repairer, so this never waits and never leaks.
			select {
			case op.Reply <- generationReport{Zone: zone.id, Generation: state.Generation}:
			default:
			}
		}
	}
}

// latency is how long this zone takes to answer one operation.
func (zone *replica) latency() time.Duration {
	if zone.spread <= 0 {
		return zone.delay
	}
	return zone.delay + time.Duration(zone.jitter.Int64N(int64(zone.spread)))
}
