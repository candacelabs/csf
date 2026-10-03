package opencode

import "errors"

// Sentinel errors returned by this package. Every error a caller can act on is
// reachable with errors.Is; nothing in this package expects a caller to match
// on error text. Errors that carry detail wrap the sentinel with
// fmt.Errorf("%w: ...") and keep the "opencode: " prefix supplied by the
// sentinel.
var (
	// ErrConfigRequired reports a nil OpenCodeConfig supplied to NewFactory.
	ErrConfigRequired = errors.New("opencode: configuration is required")
	// ErrHostRequired reports a nil harness.IHost supplied to Factory.New.
	ErrHostRequired = errors.New("opencode: host is required")
	// ErrProviderRequired reports a runtime constructed with no provider.
	ErrProviderRequired = errors.New("opencode: provider is required")
	// ErrModel reports a configured model outside the providerID/modelID
	// grammar. Only the first separator splits, so "openrouter/openai/gpt" is
	// provider "openrouter" and model "openai/gpt".
	ErrModel = errors.New("opencode: model must be providerID/modelID")

	// ErrClosed reports an operation that arrived after Close began. It is
	// terminal: the runtime never becomes usable again.
	ErrClosed = errors.New("opencode: runtime is closed")
	// ErrAlreadyStarted reports a second Start on one runtime.
	ErrAlreadyStarted = errors.New("opencode: runtime is already started")
	// ErrAlreadyActivated reports a second Activate on one runtime.
	ErrAlreadyActivated = errors.New("opencode: runtime is already activated")
	// ErrSessionUnavailable reports an operation with no attached, activated,
	// live session: before Start, before Activate, or after the session
	// lifecycle was canceled. Core may retry after a fresh Start.
	ErrSessionUnavailable = errors.New("opencode: session is unavailable")

	// ErrQueueFull reports a follow-up prompt rejected because the bounded FIFO
	// queue is at its configured capacity. The caller may retry once a turn
	// completes; nothing was submitted to the provider.
	ErrQueueFull = errors.New("opencode: prompt queue is full")

	// ErrUnhealthy reports that the OpenCode server answered its health probe
	// without reporting itself healthy.
	ErrUnhealthy = errors.New("opencode: server reported unhealthy")
	// ErrVersionMismatch reports a server outside the pinned version contract.
	// The wrapped message names both the reported and the pinned version.
	ErrVersionMismatch = errors.New("opencode: server version does not match the pinned contract")
	// ErrEmptySession reports a session response with no usable identity.
	ErrEmptySession = errors.New("opencode: server returned an empty session")
	// ErrWorkspaceMismatch reports a session bound to a directory other than
	// the workspace Core supplied. The runtime refuses to attach to it.
	ErrWorkspaceMismatch = errors.New("opencode: session belongs to another workspace")
	// ErrIncoherentSession reports that the session's status kept changing
	// while the runtime tried to read one coherent transcript snapshot.
	ErrIncoherentSession = errors.New("opencode: session kept changing while hydrating")
	// ErrAbortRejected reports an abort the server accepted but did not
	// acknowledge as applied.
	ErrAbortRejected = errors.New("opencode: server did not acknowledge the abort")
)
