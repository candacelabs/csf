package raftdemo

import (
	"context"
	"time"
)

// Snapshot is the cluster's fleet view at one moment, and it is the whole of
// what this engine tells anybody.
//
// It is deliberately the shape the raft widget's declared stream event carries.
// Not because the engine knows about a widget — it names no region, no wire name
// and no field spelling — but because a fleet view is a small closed set of
// facts and there is no honest disagreement about which ones they are.
type Snapshot struct {
	// Sequence is this view's position in the stream, from 1. It rises by one
	// per view and never repeats, which is what the widget re-arms its motion
	// on: one snapshot is one heartbeat round is one round of pulses.
	Sequence uint64

	// Term is the highest term any node has reached.
	Term uint64

	// Leader is the name of the node leading that term, or empty while there
	// is none.
	Leader string

	// LeaderKnown is whether a leader exists.
	LeaderKnown bool

	// Authoritative is whether this view came from the leader itself. A leader
	// tracks peer liveness and attaches its own view to each heartbeat; a
	// leaderless cluster has nobody entitled to speak for it, so the view is
	// the observer's own aggregate and says so.
	Authoritative bool

	// HasQuorum is whether the live membership still reaches a majority.
	HasQuorum bool

	// Voters is the cluster's size.
	Voters int

	// AliveVoters is how many members are currently reachable, counted by the
	// leader when there is one and by the observer when there is not.
	AliveVoters int

	// LeaderClaims is how many nodes claim to lead the current term.
	//
	// It is here because it is the one number that makes the safety property
	// checkable from outside: a node votes at most once per term and a leader
	// needs a majority, so two leaders of one term would need two majorities
	// that overlap on a node that voted twice. This is never above one, and a
	// specification can say so rather than take the argument on faith.
	LeaderClaims int
}

// report is one node telling the observer about itself.
//
// A node reports when its observable state moved and on every heartbeat round.
// The observer never asks: asking would mean reading a node's state from another
// goroutine, which is the thing this package does not do.
type report struct {
	// ID is the reporting node's index.
	ID int

	// Term, Role, Leader and Down are that node's observable state.
	Term   uint64
	Role   role
	Leader int
	Down   bool

	// AliveVoters is the node's own liveness count. It is only meaningful from
	// a leader, which is the only node that receives acknowledgements.
	AliveVoters int

	// Beat marks a leader's heartbeat round, and is what mints a view.
	Beat bool
}

// subscription is one caller's feed.
type subscription struct {
	// ctx is the subscriber's lifetime. The observer holds it rather than
	// spawning a goroutine per subscriber to watch it: a session that ends is
	// dropped at the next fan-out, which is at most one heartbeat later.
	ctx context.Context

	// deliveries is the channel the caller reads. The observer owns closing it.
	deliveries chan Snapshot
}

// subscriberBuffer is how far behind a subscriber may fall before it starts
// missing views.
//
// A view is a complete picture rather than a delta, so a subscriber that missed
// one has lost nothing it needs — which is why the fan-out drops rather than
// blocks. A cluster that stalled because a browser stopped reading would be a
// demo whose protocol is hostage to its UI.
const subscriberBuffer = 8

// observe is the fleet view: one goroutine owning the last report from every
// node, the sequence counter, and the subscriber set.
//
// Nothing else in the process may touch any of those three, which is why they
// are locals here rather than fields anywhere. A subscriber arrives as a message
// and leaves by having its context end.
func (cluster *Cluster) observe(ctx context.Context) {
	views := make([]report, cluster.config.Nodes)
	for index := range views {
		views[index] = report{ID: index, Role: roleFollower, Leader: unknownLeader}
	}

	var subscribers []subscription
	sequence := uint64(0)
	var latest Snapshot

	// The watchdog is what keeps a leaderless cluster visible, and it is a
	// deadline rather than a second clock on purpose: a ticker running beside
	// the heartbeat would mint views of its own whenever the two drifted past
	// each other, and "one snapshot is one heartbeat round" would stop being
	// exactly true. Every beat pushes this out; only the absence of one fires
	// it, and what it mints then says LeaderKnown false — which is the state
	// the card beside this package draws as an election in progress.
	silence := time.NewTimer(viewDeadline(cluster.config))
	defer silence.Stop()

	mint := func() {
		sequence++
		latest = fleetView(cluster.config, views, sequence)
		subscribers = fanOut(subscribers, latest)
		silence.Reset(viewDeadline(cluster.config))
	}

	// The stream opens with a view rather than with a wait, so a subscriber
	// that arrives before the first election has something to render.
	mint()

	for {
		select {
		case <-ctx.Done():
			for _, subscriber := range subscribers {
				close(subscriber.deliveries)
			}
			return

		case incoming := <-cluster.reports:
			views[incoming.ID] = incoming
			if incoming.Beat {
				mint()
			}

		case request := <-cluster.subscriptions:
			subscribers = append(subscribers, request)
			// A session that opens between two rounds gets the current view
			// immediately rather than a blank card for up to a heartbeat.
			offer(request, latest)

		case <-silence.C:
			mint()
		}
	}
}

// viewDeadline is how long the fleet view waits for a heartbeat round before
// minting a view of its own.
//
// Half a heartbeat of slack: long enough that a leader beating on time is always
// the thing that mints, short enough that a cluster which has just lost its
// leader reports the loss well inside one election timeout — which
// Config.Validate holds above two heartbeats for exactly this reason.
func viewDeadline(config Config) time.Duration { return config.Heartbeat * 3 / 2 }

// fleetView builds one view from what every node last said.
//
// The leader's own numbers win when there is a leader, because the leader is the
// only node that receives acknowledgements and therefore the only one that knows
// who is reachable. With no leader nobody is entitled to speak for the cluster,
// so the view is this function's own count of who has not reported itself down,
// and it is marked non-authoritative to say exactly that.
func fleetView(config Config, views []report, sequence uint64) Snapshot {
	term := uint64(0)
	for _, current := range views {
		if current.Term > term {
			term = current.Term
		}
	}

	leader := unknownLeader
	claims := 0
	for _, current := range views {
		if current.Down || current.Role != roleLeader || current.Term != term {
			continue
		}
		claims++
		if leader == unknownLeader {
			leader = current.ID
		}
	}

	alive := 0
	if leader != unknownLeader {
		alive = views[leader].AliveVoters
	} else {
		for _, current := range views {
			if !current.Down {
				alive++
			}
		}
	}

	snapshot := Snapshot{
		Sequence:      sequence,
		Term:          term,
		LeaderKnown:   leader != unknownLeader,
		Authoritative: leader != unknownLeader,
		HasQuorum:     alive >= config.quorum(),
		Voters:        config.Nodes,
		AliveVoters:   alive,
		LeaderClaims:  claims,
	}
	if snapshot.LeaderKnown {
		snapshot.Leader = nodeName(leader)
	}
	return snapshot
}

// fanOut delivers one view to every live subscriber, and closes and forgets the
// ones whose context has ended.
//
// The filtering is done in place on the slice it was given, which is the
// observer's own and reachable from nowhere else.
func fanOut(subscribers []subscription, snapshot Snapshot) []subscription {
	remaining := subscribers[:0]
	for _, subscriber := range subscribers {
		if subscriber.ctx.Err() != nil {
			close(subscriber.deliveries)
			continue
		}
		offer(subscriber, snapshot)
		remaining = append(remaining, subscriber)
	}
	return remaining
}

// offer hands a view to one subscriber without waiting for it.
func offer(subscriber subscription, snapshot Snapshot) {
	select {
	case subscriber.deliveries <- snapshot:
	default:
	}
}
