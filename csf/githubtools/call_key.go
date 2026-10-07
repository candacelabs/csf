// Copyright 2026 Candace Labs

package githubtools

// CallKey is one series of the call count.
type CallKey struct {
	Operation string
	Actor     ActorKind
	Outcome   string
}
