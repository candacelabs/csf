// Package session implements the service that maintains one WebSocket
// connection's widget state.
//
// Actor, in actor.go, is the existing Go type for this service. Its parent in
// internal/wsx starts the service and waits for its Run method during cleanup.
// The service lives for one connection and processes events on one goroutine.
// All widgets on that connection share its event loop and transport.
// Asynchronous effects may add goroutines.
//
// The loop serializes reducer and renderer access to its state values. Pointers
// inside those values may still alias objects elsewhere: shared mutable objects
// require synchronization or exclusive ownership by agreement. The loop selects
// over three typed, bounded inputs: events and effect results, client
// acknowledgements, and heartbeat ticks. Browser events pass authorization before
// entering the mailbox.
//
// # One step
//
// A step stamps the event with wall time and any generated identifiers, so
// reducers never call a clock or a random source; reduces under a panic
// guard; marks the fragments the transition may have touched; renders those
// fragments; emits one patch through the outbound validation boundary and the
// framer; and finally hands any effects to the actor boundary, which runs them
// in goroutines scoped to the session context and returns their results as
// ordinary events. Effects never run inside a reducer.
//
// # Backpressure
//
// Patches in flight are bounded by an acknowledgement window that retains
// metadata, never frame bytes. When the window fills, the actor keeps reducing
// but stops rendering, then renders once from current state when an
// acknowledgement re-opens it. Because rendering is pure, the skipped frames
// were never needed — memory under a slow client is proportional to the number
// of fragments, not to the number of pending patches. Sustained pressure
// reaches the application as a synthesized event rather than as a transport
// call, so the reducer stays deterministic and replayable.
//
// # Panics
//
// Effect goroutines start through one helper that installs recovery, a counter,
// and shutdown wait-group registration. The transport owns the loop separately;
// bounded effect draining also uses a waiter goroutine. Repeated panics at one
// site close that connection while other connections continue serving.
//
// # Status
//
// Implemented: the actor and its three bounded inputs, the mailbox and its
// pool, the acknowledgement window, the coalescing flush, the effect boundary,
// the panic guard, the rate budgets, and the provenance record.
package session
