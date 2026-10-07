package session

// Peer is the immutable pair a session is bound to.
type Peer[I IIdentity] struct {
	// ID is the sixteen server-minted bytes naming this session, carried on
	// every frame in both directions.
	ID ID

	// Identity is the authenticated principal, derived once from the upgrade
	// request, as the application's own type. It never changes for the life of
	// the session.
	Identity I
}
