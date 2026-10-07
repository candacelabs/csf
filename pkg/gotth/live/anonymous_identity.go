package live

import "net/http"

// Anonymous is a Config.Authenticate implementation binding every session to a
// single anonymous identity. It is the explicit opt-out from authentication,
// named rather than implied by a nil hook.
func Anonymous(request *http.Request) (AnonymousIdentity, error) { return AnonymousIdentity{}, nil }

// AnonymousIdentity is the identity [Anonymous] produces, and the type an
// application with no accounts instantiates its Config on.
//
// It is a concrete struct rather than the interface, and it is exported for
// that reason alone: since 2026-09-03 a Config carries its identity type as a
// type parameter, so an application that opts out of authentication still has
// to name a type — and the type it names must not be `live.IIdentity`, because
// naming the interface there is the erasure the ruling removed.
type AnonymousIdentity struct{}

// Subject is the one subject every anonymous session shares.
func (AnonymousIdentity) Subject() string { return "anonymous" }
