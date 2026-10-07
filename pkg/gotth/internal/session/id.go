package session

import "encoding/hex"

// ID is a session identifier: sixteen random bytes minted by the server at
// handshake. It is server-minted so that untrusted input can never name
// another session, and sixteen bytes wide so a patch frame captured in
// isolation is resolvable.
type ID [16]byte

// String returns the lower-case hex form used in logs and span attributes.
func (id ID) String() string { return hex.EncodeToString(id[:]) }
