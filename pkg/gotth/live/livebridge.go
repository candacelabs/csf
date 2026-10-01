package live

import "github.com/candacelabs/csf/pkg/gotth/internal/livebridge"

// NewSessionFor builds the [Session] live/livetest hands to a specification.
//
// # Why this is exported, and why exporting it is still safe
//
// A Session is the pair bound at the handshake, and the reason nothing
// downstream can mint one is that its fields are unexported — which is also why
// livetest, a different package, cannot build the Session a spec needs to drive
// Config.Init, Config.Authorize, Config.Teardown or an [Effect.Run] directly.
//
// Until 2026-09-03 that was solved by an assignment: live set a function
// variable in internal/livebridge and livetest read it, so no identifier
// appeared here at all. A package-level variable cannot be generic, and Session
// is generic now, so the indirection cannot hold the constructor any more.
//
// What replaced it keeps the property rather than the mechanism. The token is
// obtainable only from internal/livebridge, whose import path is internal to
// this module and whose importers internal/arch asserts are exactly live and
// live/livetest. A consumer's handler cannot obtain one, so it cannot call
// this, which is the same guarantee the old design bought with an `any` and a
// type assertion — stated in the type system instead of in a comment.
//
// It panics on a zero Token rather than returning an error: the only way to
// hold one is to have composed it from a struct literal, which is a deliberate
// attempt to reach a constructor this package does not offer.
func NewSessionFor[I IIdentity](token livebridge.Token, id ID, identity I) Session[I] {
	if !token.Granted() {
		panic("gotth-live: live.NewSessionFor was called with a Token nobody granted: " +
			"a Session is bound at the handshake, and livetest.NewSession is the only way to build one outside it")
	}
	return Session[I]{id: id, identity: identity}
}
