// Package fleet is the part of every CandaWS engine that is the same part.
//
// The five services in this directory are five different shapes — a chain, a
// broker, a write quorum, a dispatcher and a fan-in — and they share almost
// nothing. What they do share is the seam between an engine and the host that
// watches it: a set of goroutines started against one context, a token that
// makes "run once" mean it, a fan-out that hands the latest view to whoever
// subscribed, and a per-goroutine random stream that a seed fixes.
//
// Those four are here rather than copied five times because they are exactly
// the kind of thing that gets copied five times and then drifts: a fan-out that
// drops in four services and blocks in the fifth is a demo whose protocol is
// hostage to a browser, and nothing about the fifth service would say so.
//
// Everything here is CSP-native, which is the same rule the engines follow.
// [Feed] owns its subscriber set in one goroutine and is reached by sending to
// it; [Once] is a token in a channel rather than a flag behind a lock; the one
// [sync.WaitGroup] in the package is [Crew]'s, counting goroutines that have
// returned, which is a leaf counter and guards no protocol.
package fleet
