package session

// IIdentity is what this package needs from an application's identity, and it
// appears only as a CONSTRAINT and in the admission bookkeeping that calls
// Subject(). Since 2026-09-03 the identity itself travels as a type parameter,
// so nothing here returns one or hands one back through a field.
type IIdentity interface {
	// Subject returns a stable, non-secret identifier used for logging and
	// per-identity session limits.
	Subject() string
}
