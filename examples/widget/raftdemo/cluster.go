package raftdemo

import (
	"context"
	"fmt"
	"slices"
	"sync"
)

// inboxDepth is how many messages a node's inbox holds before the network starts
// dropping into it.
//
// A cluster of this size exchanges a handful of messages per round, so the depth
// is generous rather than tuned; what matters is that it is finite, because a
// network with an unbounded queue is not a network.
const inboxDepth = 64

// Cluster is one election running in one process.
//
// It holds channels and nothing else — no protocol state, no fleet view, no
// subscriber list. Each of those belongs to exactly one goroutine started by
// [Cluster.Run], and this struct is how the rest of the program reaches them.
type Cluster struct {
	// config is the shape and pace New validated.
	config Config

	// names is every node's neutral name, in index order.
	names []string

	// inboxes is one channel per node. The network writes to them, and so does
	// an operator control — see control for why that path does not go through
	// the network.
	inboxes []chan message

	// traffic is the network's own inbox: everything every node sends.
	traffic chan envelope

	// reports is the fleet view's inbox.
	reports chan report

	// subscriptions is how a caller reaches the fleet view to be added to it.
	subscriptions chan subscription

	// start holds exactly one token, taken by the first Run. It is a channel
	// rather than a flag and a lock because a token that can be taken once is
	// the whole of what "run once" means, and a channel already is one.
	start chan struct{}
}

// New builds a cluster and starts nothing.
//
// Every fault in the configuration is reported here, so a *Cluster that exists
// is one whose pace can actually elect a leader. The goroutines belong to
// [Cluster.Run] because they belong to the context Run is given.
func New(config Config) (*Cluster, error) {
	if validationError := config.Validate(); validationError != nil {
		return nil, validationError
	}

	cluster := &Cluster{
		config:        config,
		names:         make([]string, config.Nodes),
		inboxes:       make([]chan message, config.Nodes),
		traffic:       make(chan envelope, inboxDepth*config.Nodes),
		reports:       make(chan report, inboxDepth*config.Nodes),
		subscriptions: make(chan subscription),
		start:         make(chan struct{}, 1),
	}
	for index := range config.Nodes {
		cluster.names[index] = nodeName(index)
		cluster.inboxes[index] = make(chan message, inboxDepth)
	}
	cluster.start <- struct{}{}
	return cluster, nil
}

// Names is every node's name, in index order. The slice is a copy: a caller
// enumerating the cluster cannot renumber it.
func (cluster *Cluster) Names() []string { return slices.Clone(cluster.names) }

// Config is the configuration this cluster was built from.
func (cluster *Cluster) Config() Config { return cluster.config }

// Run starts every goroutine and returns when the context ends and all of them
// have stopped.
//
// It blocks, so a host runs it in a goroutine of its own and a specification can
// wait on its return to know the cluster is actually gone rather than merely
// asked to go. Cancellation is the caller's own instruction, so it is not
// reported back as an error; the only error Run has is being called twice.
func (cluster *Cluster) Run(ctx context.Context) error {
	select {
	case <-cluster.start:
	default:
		return ErrAlreadyRunning
	}

	var running sync.WaitGroup
	launch := func(goroutine func(ctx context.Context)) {
		running.Add(1)
		go func() {
			defer running.Done()
			goroutine(ctx)
		}()
	}

	launch(cluster.route)
	launch(cluster.observe)
	for index := range cluster.config.Nodes {
		member := newNode(index, cluster.config, cluster.inboxes[index], cluster.traffic, cluster.reports)
		launch(member.run)
	}

	running.Wait()
	return nil
}

// Subscribe returns a channel carrying every view minted from now on, plus the
// current one if there is one.
//
// The channel is closed when the cluster stops. A subscription whose context
// ends is dropped at the next view rather than immediately, which costs at most
// one heartbeat and saves a goroutine per subscriber.
func (cluster *Cluster) Subscribe(ctx context.Context) (<-chan Snapshot, error) {
	request := subscription{ctx: ctx, deliveries: make(chan Snapshot, subscriberBuffer)}
	select {
	case cluster.subscriptions <- request:
		return request.deliveries, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// Crash fails one node: it stops sending, drops everything that arrives, and
// keeps the term and vote it had — which is what a process whose state is on
// disk does when it dies.
func (cluster *Cluster) Crash(ctx context.Context, name string) error {
	return cluster.control(ctx, name, kindCrash)
}

// Recover restarts a crashed node as a follower of whatever term it left in.
func (cluster *Cluster) Recover(ctx context.Context, name string) error {
	return cluster.control(ctx, name, kindRecover)
}

// control delivers an operator's message straight into a node's inbox.
//
// It deliberately does not go through the network. The network drops what does
// not fit, which is right for a packet and wrong for an instruction: a crash
// that silently did not happen is a demo that stops demonstrating and says
// nothing. Writing to the inbox from the caller's goroutine is safe — a channel
// is — and it cannot deadlock against the network, which is the risk a blocking
// delivery routed through the router would carry.
func (cluster *Cluster) control(ctx context.Context, name string, kind messageKind) error {
	index := slices.Index(cluster.names, name)
	if index < 0 {
		return fmt.Errorf("%w: %q, in a cluster of %v", ErrUnknownNode, name, cluster.names)
	}
	select {
	case cluster.inboxes[index] <- message{Kind: kind, From: selfDelivered}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// route is the network: one goroutine owning every inbox, delivering what nodes
// send each other.
//
// Delivery never waits. A message that arrives at a full inbox is dropped, which
// is the one liberty a network is allowed to take with a packet and the reason
// no node can be made to block on another. Raft is built to lose messages: a
// dropped vote request costs one election timeout and a dropped heartbeat costs
// one round.
func (cluster *Cluster) route(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case sending := <-cluster.traffic:
			cluster.deliver(sending)
		}
	}
}

// deliver hands one envelope to its destination, or to every peer but the
// sender.
func (cluster *Cluster) deliver(sending envelope) {
	if sending.To != broadcast {
		cluster.offerTo(sending.To, sending.Message)
		return
	}
	for index := range cluster.inboxes {
		if index == sending.Message.From {
			continue
		}
		cluster.offerTo(index, sending.Message)
	}
}

// offerTo is the network's one act: a delivery that does not wait.
func (cluster *Cluster) offerTo(index int, sending message) {
	select {
	case cluster.inboxes[index] <- sending:
	default:
	}
}
