package opencode

import (
	"context"
	"encoding/json"
)

// iProvider is the OpenCode server as the session runtime sees it: every
// iProvider operation the lifecycle, steering, projection, and reconciliation
// code depends on, and nothing else. It is the package's only seam between
// session behavior and the wire.
//
// Keeping the seam here rather than at the HTTP boundary is deliberate. The
// behavior this package owns - run fencing, bounded admission, abort-and-
// resubmit steering, publication retry - is decided from iProvider *responses*,
// not from HTTP. Specs that exercise that behavior script this interface
// directly; only the sdkAdapter contract specs pay for a real server.
//
// Implementations must honor each call's context and are used concurrently:
// streamEvents runs on its own goroutine for the whole session while the other
// calls are issued from the runtime's command goroutine.
type iProvider interface {
	// health reports whether the server considers itself healthy and the
	// version it advertises.
	health(ctx context.Context) (bool, string, error)

	// createSession opens a new session scoped to the runtime's workspace.
	createSession(ctx context.Context, title string) (providerSession, error)

	// session reads one existing session, whose directory the caller checks.
	session(ctx context.Context, sessionID string) (providerSession, error)

	// rehydrate reads one transcript snapshot together with the session phase
	// it was observed with. The bool reports whether the two were coherent: a
	// false means the phase moved across the read and the caller must retry
	// rather than conclude a turn from a stale transcript.
	rehydrate(ctx context.Context, sessionID string) ([]providerMessage, sessionPhase, bool, error)

	// promptAsync submits one prompt and returns once the server accepts it.
	// The turn's result arrives through the transcript, never through this call.
	promptAsync(
		ctx context.Context,
		sessionID, messageID, prompt, system string,
		model promptModel,
	) error

	// abort asks the server to stop the session's active turn, reporting an
	// error when the server does not acknowledge that it applied.
	abort(ctx context.Context, sessionID string) error

	// streamEvents delivers provider events to receive until ctx ends or the
	// stream breaks. Events are only invalidation hints: the runtime re-reads
	// the transcript rather than trusting them, so a broken stream degrades to
	// interval polling instead of losing work.
	streamEvents(ctx context.Context, receive func(event json.RawMessage)) error
}

var _ iProvider = (*sdkAdapter)(nil)
