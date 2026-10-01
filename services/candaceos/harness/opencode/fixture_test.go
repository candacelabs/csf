package opencode

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

// errPublishUnavailable is the rejection a scripted host returns while it is
// refusing publications.
var errPublishUnavailable = errors.New("host publication unavailable")

// eventRecorder collects the events a host accepted. It is written from the
// runtime's own goroutines and read from spec goroutines, so every access is
// guarded and every event is cloned on the way in and out.
type eventRecorder struct {
	mu     sync.Mutex
	events []*candaceosv1.HarnessEvent
}

func (recorder *eventRecorder) append(event *candaceosv1.HarnessEvent) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.events = append(recorder.events, proto.Clone(event).(*candaceosv1.HarnessEvent))
}

// all reports every accepted event, in publication order.
func (recorder *eventRecorder) all() []*candaceosv1.HarnessEvent {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	events := make([]*candaceosv1.HarnessEvent, len(recorder.events))
	for index, event := range recorder.events {
		events[index] = proto.Clone(event).(*candaceosv1.HarnessEvent)
	}
	return events
}

// count reports how many accepted events satisfy matches.
func (recorder *eventRecorder) count(matches func(event *candaceosv1.HarnessEvent) bool) int {
	total := 0
	for _, event := range recorder.all() {
		if matches(event) {
			total++
		}
	}
	return total
}

// counting adapts count for Eventually and Consistently.
func (recorder *eventRecorder) counting(matches func(event *candaceosv1.HarnessEvent) bool) func() int {
	return func() int { return recorder.count(matches) }
}

// Event predicates. Naming them keeps specs reading as contracts rather than
// as protobuf accessor chains.
func isIdle(event *candaceosv1.HarnessEvent) bool        { return event.GetIdle() != nil }
func isError(event *candaceosv1.HarnessEvent) bool       { return event.GetError() != nil }
func isUserMessage(event *candaceosv1.HarnessEvent) bool { return event.GetUserMessage() != nil }
func isToolStarted(event *candaceosv1.HarnessEvent) bool { return event.GetToolStarted() != nil }

// Event projections used by WithTransform matchers.
func userContent(event *candaceosv1.HarnessEvent) string {
	return event.GetUserMessage().GetContent()
}

func assistantContent(event *candaceosv1.HarnessEvent) string {
	return event.GetAssistantMessage().GetContent()
}

func deltaContent(event *candaceosv1.HarnessEvent) string {
	return event.GetAssistantDelta().GetContent()
}

func failureMessage(event *candaceosv1.HarnessEvent) string {
	return event.GetError().GetMessage()
}

// publishGate scripts host publication failures for one class of event and
// counts how often that class was attempted, so a spec can assert that the
// runtime retried rather than dropped an event.
type publishGate struct {
	mu sync.Mutex

	matches           func(event *candaceosv1.HarnessEvent) bool
	remainingFailures int
	attempts          int
}

// rejectUntilAllowed refuses every matching publication until allow is called.
func rejectUntilAllowed(matches func(event *candaceosv1.HarnessEvent) bool) *publishGate {
	return &publishGate{matches: matches, remainingFailures: -1}
}

// rejectOnce refuses only the first matching publication.
func rejectOnce(matches func(event *candaceosv1.HarnessEvent) bool) *publishGate {
	return &publishGate{matches: matches, remainingFailures: 1}
}

func (gate *publishGate) intercept(_ context.Context, event *candaceosv1.HarnessEvent) error {
	if !gate.matches(event) {
		return nil
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.attempts++
	if gate.remainingFailures == 0 {
		return nil
	}
	if gate.remainingFailures > 0 {
		gate.remainingFailures--
	}
	return errPublishUnavailable
}

// allow stops refusing matching publications.
func (gate *publishGate) allow() {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.remainingFailures = 0
}

// attemptCount reports how many matching publications the host was offered.
func (gate *publishGate) attemptCount() int {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.attempts
}

func (gate *publishGate) countingAttempts() func() int { return gate.attemptCount }

// runtimeFixture is one runtime wired to a scripted provider and a recording
// host.
type runtimeFixture struct {
	script  *providerScript
	runtime *harnessRuntime
	events  *eventRecorder
}

type fixtureSpec struct {
	script        *providerScript
	sessionID     string
	queueCapacity int32
	intercept     func(ctx context.Context, event *candaceosv1.HarnessEvent) error
}

type fixtureOption func(spec *fixtureSpec)

// withScript supplies a pre-scripted provider instead of a default one.
func withScript(script *providerScript) fixtureOption {
	return func(spec *fixtureSpec) { spec.script = script }
}

// withConfiguredSession makes the runtime attach to an existing session rather
// than create one.
func withConfiguredSession(sessionID string) fixtureOption {
	return func(spec *fixtureSpec) { spec.sessionID = sessionID }
}

// withQueueCapacity bounds the follow-up queue.
func withQueueCapacity(capacity int32) fixtureOption {
	return func(spec *fixtureSpec) { spec.queueCapacity = capacity }
}

// withPublishInterceptor scripts the host's response to each publication. A
// non-nil error rejects it, and the event is then not recorded.
func withPublishInterceptor(
	intercept func(ctx context.Context, event *candaceosv1.HarnessEvent) error,
) fixtureOption {
	return func(spec *fixtureSpec) { spec.intercept = intercept }
}

// buildFixture wires a runtime to a scripted provider without attaching a
// session, and registers its cleanup.
func buildFixture(options ...fixtureOption) *runtimeFixture {
	GinkgoHelper()
	spec := fixtureSpec{queueCapacity: 2}
	for _, option := range options {
		option(&spec)
	}
	if spec.script == nil {
		spec.script = newProviderScript()
	}
	host, events := newRecordingHost(spec.intercept)
	runtime, err := newRuntime(
		testConfig(spec.sessionID, spec.queueCapacity), "/workspace", host, scriptedProvider(spec.script),
	)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(runtime.Close)
	DeferCleanup(func() {
		Expect(spec.script.contractViolations()).To(BeEmpty(), "the scripted provider built a malformed fixture")
	})
	return &runtimeFixture{script: spec.script, runtime: runtime, events: events}
}

// attach starts and activates the session, asserting both succeed.
func (fixture *runtimeFixture) attach(ctx SpecContext) *runtimeFixture {
	GinkgoHelper()
	session, err := fixture.runtime.Start(ctx)
	Expect(err).NotTo(HaveOccurred())
	Expect(session.GetId()).To(Equal(fixtureSessionID))
	Expect(fixture.runtime.Activate(ctx)).To(Succeed())
	return fixture
}

// startFixture builds a fixture and attaches its session.
func startFixture(ctx SpecContext, options ...fixtureOption) *runtimeFixture {
	GinkgoHelper()
	return buildFixture(options...).attach(ctx)
}

// send admits one prompt and asserts it was accepted.
func (fixture *runtimeFixture) send(
	ctx context.Context,
	runID, content string,
	delivery candaceosv1.HarnessDelivery,
) {
	GinkgoHelper()
	Expect(fixture.runtime.Send(ctx, testPrompt(runID, content, delivery))).To(Succeed())
}

// sendImmediate admits one prompt that steers the active turn.
func (fixture *runtimeFixture) sendImmediate(ctx context.Context, runID, content string) {
	GinkgoHelper()
	fixture.send(ctx, runID, content, candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
}

// enqueue admits one follow-up prompt behind the active turn.
func (fixture *runtimeFixture) enqueue(ctx context.Context, runID, content string) {
	GinkgoHelper()
	fixture.send(ctx, runID, content, candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE)
}

// newRecordingHost builds a Host mock that records every accepted event, and
// optionally consults intercept first. The callback runs on the runtime's own
// goroutines, so it only records and never asserts.
func newRecordingHost(
	intercept func(ctx context.Context, event *candaceosv1.HarnessEvent) error,
) (*MockIHost, *eventRecorder) {
	host := NewMockIHost(gomock.NewController(GinkgoT()))
	recorder := &eventRecorder{}
	host.EXPECT().Publish(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(
		func(publishCtx context.Context, event *candaceosv1.HarnessEvent) error {
			if intercept != nil {
				if err := intercept(publishCtx, event); err != nil {
					return err
				}
			}
			recorder.append(event)
			return nil
		},
	)
	return host, recorder
}

// testConfig is the suite's transport policy: a fast poll interval so specs
// converge quickly, and a request timeout long enough that no spec depends on
// one expiring. The URL is inert for behavior specs, which never reach a
// socket; only the sdkAdapter contract specs use a real one.
func testConfig(sessionID string, queueCapacity int32) *candaceosv1.OpenCodeConfig {
	return &candaceosv1.OpenCodeConfig{
		Url: "http://opencode.invalid", Username: "opencode", Password: "secret", SessionId: sessionID,
		RequestTimeout: int64(2 * time.Second),
		PollInterval:   int64(20 * time.Millisecond),
		QueueCapacity:  queueCapacity,
		Model:          "openrouter/openai/gpt-5.4-nano",
	}
}

// testConfigWithURL is the suite policy pointed at one explicit URL, for the
// adapter construction specs.
func testConfigWithURL(url string) *candaceosv1.OpenCodeConfig {
	config := testConfig("", 2)
	config.Url = url
	return config
}

func testPrompt(runID, content string, delivery candaceosv1.HarnessDelivery) *candaceosv1.HarnessPrompt {
	return &candaceosv1.HarnessPrompt{RunId: runID, Content: content, Delivery: delivery}
}

// observedPublication records what a scripted host saw on the runtime's own
// goroutine, so the spec can assert on it from a Ginkgo node instead of
// asserting where a failure would panic the process.
type observedPublication struct {
	mu         sync.Mutex
	seen       bool
	contextErr error
}

func (observed *observedPublication) record(contextErr error) {
	observed.mu.Lock()
	defer observed.mu.Unlock()
	observed.seen = true
	observed.contextErr = contextErr
}

// result reports whether the publication was observed and the state of the
// context it was published on.
func (observed *observedPublication) result() (bool, error) {
	observed.mu.Lock()
	defer observed.mu.Unlock()
	return observed.seen, observed.contextErr
}

// runFence is a host that refuses any event carrying a run other than the one
// it is pinned to, the way Controller rejects a projection from a historical
// run, and counts those refusals.
type runFence struct {
	mu         sync.Mutex
	runID      string
	rejections int
}

// pin starts refusing events from any run but runID.
func (fence *runFence) pin(runID string) {
	fence.mu.Lock()
	defer fence.mu.Unlock()
	fence.runID = runID
}

func (fence *runFence) intercept(_ context.Context, event *candaceosv1.HarnessEvent) error {
	fence.mu.Lock()
	defer fence.mu.Unlock()
	if fence.runID == "" || event.GetRunId() == "" || event.GetRunId() == fence.runID {
		return nil
	}
	fence.rejections++
	return errors.New("controller rejected historical run")
}

// rejectionCount reports how many events were offered from a superseded run.
func (fence *runFence) rejectionCount() int {
	fence.mu.Lock()
	defer fence.mu.Unlock()
	return fence.rejections
}

// deltaFor matches the streaming deltas of one assistant message.
func deltaFor(messageID string) func(event *candaceosv1.HarnessEvent) bool {
	return func(event *candaceosv1.HarnessEvent) bool {
		return event.GetAssistantDelta().GetMessageId() == messageID
	}
}

// finalAssistantSaying matches a final assistant message with exact content.
func finalAssistantSaying(content string) func(event *candaceosv1.HarnessEvent) bool {
	return func(event *candaceosv1.HarnessEvent) bool {
		return assistantContent(event) == content
	}
}

// toolCompletionFor matches the completion of one tool call.
func toolCompletionFor(callID string) func(event *candaceosv1.HarnessEvent) bool {
	return func(event *candaceosv1.HarnessEvent) bool {
		return event.GetToolCompleted().GetToolCallId() == callID
	}
}
