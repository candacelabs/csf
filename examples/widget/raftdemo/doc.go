// Package raftdemo runs a real leader election in one process, so that the raft
// widget beside it animates a protocol rather than a script.
//
// # What it is
//
// Raft's election half and nothing else: terms and votes, no log, no replicated
// state machine. The only thing this cluster agrees on is who leads it right
// now, which is the same reduction candacenet's warden daemon makes and for the
// same reason — see app/warden/README.md, "Leader election (Raft-style, no
// log)". Nothing here imports warden: pkg/ may not depend on services/, so a
// widget example that reached for the daemon's election would put the example
// on the wrong side of that line. The design is read from it; the code is not.
//
// # Every node is a goroutine, and nothing is shared
//
// A cluster of N nodes is N goroutines, plus one for the network and one for the
// fleet view. Each node goroutine owns its own nodeState — its term, its vote,
// its role, its liveness bitmasks — and no other goroutine can read or write it.
// Peers do not call each other; they send messages, and a message that arrives
// at a full inbox is dropped the way a packet is. There is no mutex in this
// package and there is nothing for one to guard: every datum has exactly one
// owner and the owners talk.
//
// That is not an aesthetic choice about concurrency primitives. Raft *is* a
// message-passing protocol, so a lock standing in for it would be modelling the
// thing with the wrong shape. The state transitions are pure functions — one per
// message kind, registered in a table — and the goroutine around them does
// nothing but deliver, apply and publish, which is why the protocol can be
// specified without starting a single goroutine.
//
// # What comes out
//
// One [Snapshot] per heartbeat round, on every channel a caller got from
// [Cluster.Subscribe]. Its fields are the fleet view the leader itself reports,
// exactly as warden piggybacks its authoritative cluster view on each heartbeat:
// while a leader exists the view is the leader's and is marked authoritative,
// and while none does the view is the observer's own aggregate and is not.
//
// One snapshot is one heartbeat round, which is what makes the widget beside
// this package honest: the card re-arms its pulses on the snapshot counter, so
// a pulse crossing an edge is a heartbeat that actually crossed a channel.
//
// # Determinism
//
// Election timeouts are jittered — without jitter a cluster of equal timers
// splits its vote forever — and the jitter comes from a per-node PCG stream
// seeded from [Config.Seed] and the node's index. A specification that fixes the
// seed fixes the jitter; it does not fix the scheduler, so the engine is
// deterministic enough to demonstrate and not deterministic enough to assert a
// particular winner. No package-level clock or random source is read anywhere,
// so two clusters in one test binary do not perturb each other.
package raftdemo
