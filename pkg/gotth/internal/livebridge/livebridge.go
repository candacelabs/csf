// Package livebridge lets live/livetest construct a value only live can build.
//
// # Why this exists at all
//
// live.Session has unexported fields, so livetest — a different package —
// cannot build one. What anybody can build outside live is live.Session{},
// whose ID() is all-zero and whose Identity() is nil; identity is the reason
// Init, Authorize, Teardown and Execute take a Session at all, so a value
// without one is not a Session worth passing to a hook. The accurate statement
// is that an application can construct a useless Session and cannot construct a
// useful one, and livetest.NewSession answers the second half.
//
// # Why it is a package and not an exported constructor
//
// live could export NewSession and be done. That would put an identity
// constructor in the production package, reachable from any handler, which is a
// materially worse trade than the problem it solves: the whole point of binding
// identity at the handshake is that nothing downstream can mint one.
//
// So live assigns the constructor here at init and livetest reads it. The path
// gotth-live/internal/... is importable by gotth-live/live and
// gotth-live/live/livetest and by nothing outside this module, which is what
// makes the constructor unreachable to a consumer except through livetest —
// where the first parameter is a testing.TB, so calling it from production code
// means fabricating one.
//
// internal/arch asserts that this package's importers are exactly those two.
// The paragraph above is a claim about who imports this, and an unverified
// claim is how one quietly stops being true.
package livebridge

// Token is the capability that makes live's session constructor unreachable
// from a consumer.
//
// # Why the bridge changed shape on 2026-09-03
//
// It used to be a function VARIABLE that live assigned at init and livetest
// read. That worked because live.Session was one type. It is now
// live.Session[I], parameterized on the application's own identity type
// (operator ruling: `Identity() IIdentity` was the last erasure in the public
// surface), and **a package-level variable cannot be generic in Go** — there is
// no way to store one function that builds a Session[I] for an I the assignment
// does not know.
//
// So the direction inverted. live exports the constructor as a generic
// function, and this package exports the token it demands. The containment
// argument is unchanged and is now carried by the token rather than by the
// indirection: this package's import path is internal to the module, so only
// live and live/livetest can obtain a Token, and internal/arch asserts that
// those two are the only importers. A handler in a consumer's own package
// cannot name the parameter type, so it cannot call the constructor — which is
// the property the old design bought with an `any` and a type assertion.
type Token struct {
	// granted is unexported and unset by anything outside this package, so a
	// Token cannot be composed from a struct literal elsewhere.
	granted bool
}

// Grant returns the Token. It is callable only from live and live/livetest,
// because nothing else in the module may import this package and nothing
// outside the module can.
func Grant() Token { return Token{granted: true} }

// Granted reports whether a Token came from Grant rather than from a zero
// value, so the constructor can refuse a fabricated one loudly rather than
// building a Session for it.
func (token Token) Granted() bool { return token.granted }
