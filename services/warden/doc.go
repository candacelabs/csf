// Package warden defines the shared contracts for the candacenet warden
// service: core types, the wire protocol, and the interfaces that the
// election, watchdog, notification, dashboard, and configuration packages
// implement or consume.
//
// This package is frozen. Every other package under services/warden is
// written against the names here, so callers may rely on the types, the route
// path constants, and the eight interfaces keeping their meaning: a change
// that redefines one of them is a change to the whole service, not to one
// subsystem. The package is types, constants, pure helpers (Quorum, SortNodes,
// SortPeers, NewIncidentID) and the one real-time IClock the production binary
// runs on; it opens no connection, reads no file, and owns no shared mutable
// state, which is what lets every other package import it without a cycle.
// Behaviour lives in the packages that implement these interfaces.
//
// # Architecture overview
//
// Every node in the fleet runs the same warden binary. Nodes perform
// Raft-style leader election (terms + votes, no log replication) over a
// static, configured peer set. A leader is elected only with a majority
// quorum, which makes split-brain impossible: at most one leader can exist
// per term, and a minority partition remains leaderless (correct and
// intentional).
//
// The elected leader sends periodic heartbeats to all peers. Heartbeats
// carry the leader's authoritative ClusterView so that every follower can
// render a correct cluster dashboard without extra gossip. The leader also
// acts as the fleet watchdog: it tracks peer liveness transitions
// (alive -> suspect -> dead and recovery) and emits Incidents to an INotifier
// (SMTP email in production, mock/file/log in tests) with per-incident
// deduplication and a cooldown so flapping nodes do not spam the operator.
//
// ITransport is the gRPC WardenService over h2c on the tailnet (WireGuard is
// the node-to-node transport-security boundary). A single bound port per node
// serves the cluster RPCs (Vote, Heartbeat, Identify, and the WatchCluster
// stream) over gRPC, multiplexed by cmux with the HTTP surface: the SSR
// dashboard (PathDashboard), the JSON API (PathAPIStatus), and Prometheus
// metrics (PathMetrics).
//
// Package layout. The reusable pieces live beside this package under
// services/warden; app/warden is the runnable composition on top of them.
//
//	warden         - this package: types, wire protocol, interfaces
//	election       - election state machine + peer liveness tracking
//	grpctransport  - gRPC client implementing ITransport (h2c, pooled conns)
//	grpcserver     - gRPC WardenService server: unary RPCs + WatchCluster
//	grpcmux        - single-port cmux multiplexing the gRPC and HTTP surfaces
//	testclock      - fake clock for deterministic tests
//	watchdog       - leader-only incident engine with dedup/cooldown
//	notify         - INotifier implementations: SMTP, log, file, mock
//	dashboard      - SSR dashboard (HTMX + embedded assets) + JSON API
//	metrics        - Prometheus collectors + /metrics handler
//	config         - YAML + environment configuration loading
//	proto/warden/v1 - the candacenet.warden.v1 schema and its bindings
//	app/warden/cmd - main.go wiring (the runnable composition)
package warden
