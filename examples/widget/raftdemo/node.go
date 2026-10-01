package raftdemo

import (
	"context"
	"math/rand/v2"
	"time"
)

// node is one cluster member: one goroutine, one nodeState, three channels.
//
// The state is a field of this struct and this struct is reachable from exactly
// one goroutine, so nothing in the package can read it concurrently and there is
// nothing for a lock to protect. Everything the node learns arrives on inbox;
// everything it says leaves on outbox and reports.
type node struct {
	// config is the cluster's shape and pace, copied rather than shared.
	config Config

	// inbox is what peers, timers and the operator send this node.
	inbox <-chan message

	// outbox is the network. A node addresses a peer by index and never holds
	// a peer's channel, so one node cannot deliver into another's inbox
	// directly and cannot be made to block on one.
	outbox chan<- envelope

	// reports is the fleet view's feed.
	reports chan<- report

	// jitter is this node's own randomness. It is a per-node stream seeded from
	// the cluster's seed and the node's index, so a run is reproducible and no
	// package-level source is touched.
	jitter *rand.Rand

	// state is the protocol state this goroutine owns.
	state nodeState
}

// newNode builds one member. It starts nothing: [Cluster.Run] owns when the
// goroutines exist, because they exist against the context Run was given.
func newNode(index int, config Config, inbox <-chan message, outbox chan<- envelope, reports chan<- report) *node {
	return &node{
		config:  config,
		inbox:   inbox,
		outbox:  outbox,
		reports: reports,
		jitter:  rand.New(rand.NewPCG(uint64(config.Seed), uint64(index))),
		state:   newNodeState(index, config.Nodes),
	}
}

// run is the node's whole life: take one message, apply it, act on what came
// back, repeat until the context ends.
//
// The two timers are the node's own and are read only here. Go 1.23 made
// Timer.Reset discard a value the timer had already delivered, so resetting the
// election timer from a branch that did not drain it cannot deliver a stale
// timeout — which is the bug that would otherwise make a node campaign one
// timeout after being told not to.
func (member *node) run(ctx context.Context) {
	election := time.NewTimer(member.electionDelay())
	defer election.Stop()

	beat := time.NewTicker(member.config.Heartbeat)
	defer beat.Stop()

	for {
		var incoming message
		select {
		case <-ctx.Done():
			return
		case incoming = <-member.inbox:
		case <-election.C:
			incoming = message{Kind: kindElectionTimeout, From: selfDelivered}
		case <-beat.C:
			incoming = message{Kind: kindHeartbeatTick, From: selfDelivered}
		}

		if !member.apply(ctx, incoming, election) {
			return
		}
	}
}

// apply runs one transition and does the three things the transition could not:
// send, re-arm, and tell the observer.
//
// It reports false when the context ended mid-send, which is the one way a node
// stops other than its own select seeing ctx.Done.
func (member *node) apply(ctx context.Context, incoming message, election *time.Timer) bool {
	before := member.state.observable()

	result := step(member.state, incoming)
	member.state = result.State

	for _, sending := range result.Send {
		select {
		case member.outbox <- sending:
		case <-ctx.Done():
			return false
		}
	}

	if result.ResetElectionTimer {
		election.Reset(member.electionDelay())
	}

	// A report is worth sending when the fleet view would read differently, and
	// on every heartbeat round whether or not anything moved — because the beat
	// is what mints a snapshot, and a leader whose cluster is perfectly steady
	// is exactly the state the picture should keep moving through.
	if result.Beat || member.state.observable() != before {
		select {
		case member.reports <- member.report(result.Beat):
		case <-ctx.Done():
			return false
		}
	}
	return true
}

// report is this node's view of itself, as the observer reads it.
func (member *node) report(beat bool) report {
	return report{
		ID:          member.state.id,
		Term:        member.state.term,
		Role:        member.state.role,
		Leader:      member.state.leader,
		Down:        member.state.down,
		AliveVoters: member.state.aliveVoters(),
		Beat:        beat,
	}
}

// electionDelay is how long this node waits before campaigning.
//
// The jitter is what breaks a tie that would otherwise repeat: without it every
// follower campaigns at the same instant, every term splits its vote, and the
// cluster livelocks with a term counter climbing and no leader ever elected.
// Config.Validate refuses a multi-node cluster that asks for none.
func (member *node) electionDelay() time.Duration {
	if member.config.ElectionJitter <= 0 {
		return member.config.ElectionTimeout
	}
	return member.config.ElectionTimeout + time.Duration(member.jitter.Int64N(int64(member.config.ElectionJitter)))
}
