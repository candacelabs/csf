// Package transportidentity carries transport-observed peer identity between
// Warden's gRPC adapter and election state machine.
package transportidentity

import "context"

type peerAddressKey struct{}

// WithPeerAddress records the network peer address observed by the serving
// transport. It is a trusted-host signal, not cryptographic or per-process
// identity.
func WithPeerAddress(ctx context.Context, address string) context.Context {
	return context.WithValue(ctx, peerAddressKey{}, address)
}

// PeerAddress returns the serving transport's observed network peer address.
// An empty or missing value is unauthenticated and must fail closed.
func PeerAddress(ctx context.Context) (string, bool) {
	address, ok := ctx.Value(peerAddressKey{}).(string)
	return address, ok && address != ""
}
