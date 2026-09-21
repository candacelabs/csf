package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	opencodesdk "github.com/sst/opencode-sdk-go"
	"github.com/sst/opencode-sdk-go/option"
	"github.com/sst/opencode-sdk-go/packages/ssestream"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

// Endpoints the pinned v0.19.2 SDK does not generate. Its generic Get/Post are
// the documented escape hatch for endpoints absent from the generated API.
const (
	healthPath        = "global/health"
	sessionStatusPath = "session/status"
	promptAsyncPath   = "session/%s/prompt_async"
	eventPath         = "event"
)

const (
	// sessionErrorEvent is the one event type whose properties may not name a
	// session; it is treated as relevant to whatever session is attached.
	sessionErrorEvent = "session.error"
	// serverEventPrefix marks server-lifecycle events, which are always
	// relevant because they may mean the transcript moved while disconnected.
	serverEventPrefix = "server."
)

// sessionPhase is the provider's view of whether a session is doing work.
type sessionPhase string

const (
	phaseIdle  sessionPhase = "idle"
	phaseBusy  sessionPhase = "busy"
	phaseRetry sessionPhase = "retry"
)

// active reports whether the provider considers the session to be working.
func (phase sessionPhase) active() bool {
	return phase == phaseBusy || phase == phaseRetry
}

// Provider wire types. They are aliases, not wrappers: message, part, tool
// state, and error unions are owned by the generated SDK, and this package
// never mirrors them by hand.
type (
	providerSession = opencodesdk.Session
	providerMessage = opencodesdk.SessionMessagesResponse
	providerPart    = opencodesdk.Part
)

// sdkAdapter is the whole transport boundary. It centralizes authentication,
// workspace scoping, and per-request timeouts, fills the endpoint gaps above,
// and keeps event streaming forward compatible; everything else is delegated to
// the generated SDK. It holds no session state, so its methods are safe for
// concurrent use to the same extent as the underlying HTTP client.
type sdkAdapter struct {
	sdk            *opencodesdk.Client
	workspace      string
	requestTimeout time.Duration
}

// newSDKAdapter builds the transport for one workspace. A nil httpClient gets a
// clone of the default transport whose response-header timeout matches the
// configured request timeout; tests inject their own.
func newSDKAdapter(
	config *candaceosv1.OpenCodeConfig,
	workspace string,
	httpClient *http.Client,
) (*sdkAdapter, error) {
	if config == nil {
		return nil, ErrConfigRequired
	}
	baseURL, err := url.Parse(config.GetUrl())
	if err != nil {
		return nil, fmt.Errorf("opencode: parsing server URL: %w", err)
	}
	timeout := requestTimeout(config)
	if httpClient == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.ResponseHeaderTimeout = timeout
		httpClient = &http.Client{Transport: transport}
	}
	username, password := config.GetUsername(), config.GetPassword()
	sdk := opencodesdk.NewClient(
		option.WithBaseURL(strings.TrimRight(baseURL.String(), "/")),
		option.WithHTTPClient(httpClient),
		// Retries are the runtime's decision, not the transport's: a blind
		// retry of a prompt submission would duplicate a turn.
		option.WithMaxRetries(0),
		option.WithMiddleware(func(request *http.Request, next option.MiddlewareNext) (*http.Response, error) {
			request.SetBasicAuth(username, password)
			return next(request)
		}),
	)
	return &sdkAdapter{sdk: sdk, workspace: workspace, requestTimeout: timeout}, nil
}

// health reports whether the server considers itself healthy, and the version
// it reports, which Start checks against PinnedServerVersion.
func (c *sdkAdapter) health(ctx context.Context) (bool, string, error) {
	var health struct {
		Healthy bool   `json:"healthy"`
		Version string `json:"version"`
	}
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	if err := c.sdk.Get(ctx, healthPath, nil, &health); err != nil {
		return false, "", err
	}
	return health.Healthy, health.Version, nil
}

// createSession opens a new session scoped to this adapter's workspace.
func (c *sdkAdapter) createSession(ctx context.Context, title string) (providerSession, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	session, err := c.sdk.Session.New(ctx, opencodesdk.SessionNewParams{
		Directory: opencodesdk.F(c.workspace),
		Title:     opencodesdk.F(title),
	})
	if err != nil {
		return providerSession{}, err
	}
	if session == nil {
		return providerSession{}, ErrEmptySession
	}
	return *session, nil
}

// session reads one existing session. Its directory is the caller's to check.
func (c *sdkAdapter) session(ctx context.Context, sessionID string) (providerSession, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	session, err := c.sdk.Session.Get(ctx, sessionID, opencodesdk.SessionGetParams{
		Directory: opencodesdk.F(c.workspace),
	})
	if err != nil {
		return providerSession{}, err
	}
	if session == nil {
		return providerSession{}, ErrEmptySession
	}
	return *session, nil
}

// messages reads the session's full transcript. OpenCode has no incremental
// read, so every reconciliation re-reads everything and the runtime dedupes.
func (c *sdkAdapter) messages(ctx context.Context, sessionID string) ([]providerMessage, error) {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	messages, err := c.sdk.Session.Messages(ctx, sessionID, opencodesdk.SessionMessagesParams{
		Directory: opencodesdk.F(c.workspace),
	})
	if err != nil {
		return nil, err
	}
	if messages == nil {
		return nil, nil
	}
	return *messages, nil
}

// status reads one session's phase. A session absent from the status map is
// idle.
func (c *sdkAdapter) status(ctx context.Context, sessionID string) (sessionPhase, error) {
	phaseBySession := make(map[string]struct {
		Type sessionPhase `json:"type"`
	})
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	err := c.sdk.Get(
		ctx,
		sessionStatusPath,
		nil,
		&phaseBySession,
		option.WithQuery("directory", c.workspace),
	)
	if err != nil {
		return "", err
	}
	phase, running := phaseBySession[sessionID]
	if !running {
		return phaseIdle, nil
	}
	return phase.Type, nil
}

// rehydrate brackets the transcript read with status reads. A status transition
// across the read means the messages and the phase were not one coherent
// observation, which it reports as coherent=false: the caller must retry rather
// than complete a turn from a stale transcript.
func (c *sdkAdapter) rehydrate(
	ctx context.Context,
	sessionID string,
) ([]providerMessage, sessionPhase, bool, error) {
	before, err := c.status(ctx, sessionID)
	if err != nil {
		return nil, "", false, err
	}
	messages, err := c.messages(ctx, sessionID)
	if err != nil {
		return nil, "", false, err
	}
	after, err := c.status(ctx, sessionID)
	if err != nil {
		return nil, "", false, err
	}
	return messages, after, before == after, nil
}

// promptAsync submits one prompt and returns as soon as the server accepts it.
// The turn's result arrives through the transcript, never through this call.
// messageID is supplied by the caller so the resulting provider message can be
// correlated back to its run.
func (c *sdkAdapter) promptAsync(
	ctx context.Context,
	sessionID, messageID, prompt, system string,
	model promptModel,
) error {
	params := opencodesdk.SessionPromptParams{
		Directory: opencodesdk.F(c.workspace),
		MessageID: opencodesdk.F(messageID),
		System:    opencodesdk.F(system),
		Model: opencodesdk.F(opencodesdk.SessionPromptParamsModel{
			ProviderID: opencodesdk.F(model.ProviderID),
			ModelID:    opencodesdk.F(model.ModelID),
		}),
		Parts: opencodesdk.F([]opencodesdk.SessionPromptParamsPartUnion{
			opencodesdk.TextPartInputParam{
				Type: opencodesdk.F(opencodesdk.TextPartInputTypeText),
				Text: opencodesdk.F(prompt),
			},
		}),
	}
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	return c.sdk.Post(ctx, fmt.Sprintf(promptAsyncPath, url.PathEscape(sessionID)), params, nil)
}

// abort asks the server to stop the session's active turn. A response that does
// not acknowledge the abort is reported as ErrAbortRejected, because the caller
// must not treat an unapplied abort as applied.
func (c *sdkAdapter) abort(ctx context.Context, sessionID string) error {
	ctx, cancel := c.requestContext(ctx)
	defer cancel()
	aborted, err := c.sdk.Session.Abort(ctx, sessionID, opencodesdk.SessionAbortParams{
		Directory: opencodesdk.F(c.workspace),
	})
	if err != nil {
		return err
	}
	if aborted == nil || !*aborted {
		return ErrAbortRejected
	}
	return nil
}

// streamEvents delivers raw provider events to receive until ctx ends or the
// stream breaks, reporting io.EOF for a clean end of stream. Events are
// deliberately left as raw JSON: the pinned SDK's generated event union
// predates 1.18.21 variants such as session.status and message.part.delta, and
// an unmodeled variant must not tear down streaming. The stream carries no
// per-request timeout, since it is expected to stay open.
func (c *sdkAdapter) streamEvents(ctx context.Context, receive func(event json.RawMessage)) error {
	var response *http.Response
	err := c.sdk.Get(
		ctx,
		eventPath,
		opencodesdk.EventListParams{Directory: opencodesdk.F(c.workspace)},
		&response,
		option.WithHeader("Accept", "text/event-stream"),
	)
	if err != nil {
		return fmt.Errorf("opencode: subscribing to events: %w", err)
	}
	stream := ssestream.NewStream[json.RawMessage](ssestream.NewDecoder(response), nil)
	defer stream.Close()
	for stream.Next() {
		receive(stream.Current())
	}
	if err := stream.Err(); err != nil {
		return fmt.Errorf("opencode: reading event stream: %w", err)
	}
	return io.EOF
}

func (c *sdkAdapter) requestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.requestTimeout)
}
