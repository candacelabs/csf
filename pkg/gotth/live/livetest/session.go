package livetest

import (
	"testing"

	"github.com/candacelabs/csf/pkg/gotth/internal/livebridge"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// NewSession returns the [live.Session] a spec needs in order to call an
// application's own Config hooks directly.
//
// [live.Config]'s Init, Authorize, Teardown and Execute all take a Session, and
// a Session's fields are unexported because identity is bound at the handshake
// and nothing downstream may mint one. The consequence for a test is sharper
// than it first looks: live.Session{} compiles anywhere — an empty composite
// literal names no field — but its ID() is all-zero and its Identity() is nil,
// and identity is the reason those hooks take a Session at all. So an
// application can construct a useless Session and cannot construct a useful
// one, and a hook that takes one is testable only through a running server.
// Every application that unit-tests a hook otherwise invents the same
// workaround: a second, unexported method taking the values the hook would
// have read, with the exported hook reduced to an adapter over it. This
// library's own counter example carried exactly that split, and its comment was
// a defect report.
//
// Both values are the caller's, deliberately. Deriving the identifier from the
// identity would be tempting and wrong: one subject holds many concurrent
// sessions — that is what Limits.MaxSessionsPerIdentity is about — so two tabs
// belonging to one user need two identifiers and one identity.
//
// The nil-identity guard this used to carry is gone, and the type parameter is
// why. Since 2026-09-03 the identity is the application's OWN type rather than
// an interface, so "identity is nil" is not a value a caller can pass unless it
// chose a pointer type and passed a nil of it — which is its own bug, in its own
// Subject(), rather than a trap this constructor sets.
//
// It takes a [testing.TB] first, matching ReplayN and AssertDirtyComplete, and
// that is a guard and not decoration: reaching this constructor from production
// code means importing a package that links testing and then fabricating a
// testing.TB, which is a visible and absurd act rather than an accident. The
// second guard is the token, which only live and live/livetest can obtain.
func NewSession[I live.IIdentity](tb testing.TB, id live.ID, identity I) live.Session[I] {
	tb.Helper()
	return live.NewSessionFor(livebridge.Grant(), id, identity)
}
