package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	opencodesdk "github.com/sst/opencode-sdk-go"
	"go.uber.org/mock/gomock"
)

// submittedPrompt is one prompt as the provider received it.
type submittedPrompt struct {
	MessageID string
	System    string
	Model     promptModel
	Text      string
}

// providerScript is a scripted OpenCode server that a MockProvider delegates
// to. It is the behavior specs' whole world: no HTTP, no SSE, no JSON
// transport - just the provider responses the runtime decides from.
//
// It is stateful on purpose. A turn's life ("prompt accepted, assistant
// streams, assistant completes, session goes idle") is a sequence of differing
// answers to the same repeated rehydrate call, which ordered gomock
// expectations model poorly and a small state machine models exactly.
//
// Every method is safe to call from a spec goroutine while the runtime is
// polling, and nothing here asserts: specs assert on what it recorded.
type providerScript struct {
	mu sync.Mutex

	version  string
	attached providerSession

	phase       sessionPhase
	phaseScript []sessionPhase
	messages    []providerMessage

	requests     []string
	prompts      []submittedPrompt
	sessionReads []string
	created      int

	abortFailure         error
	abortErrorName       string
	abortErrorMessage    string
	abortToolInterrupted bool

	holdPrompts   bool
	promptHeld    chan struct{}
	promptHeldOne sync.Once

	staleMessages   []providerMessage
	staleCompletion string
	staleServed     chan struct{}
	freshRelease    chan struct{}

	violations []string

	receive          func(event json.RawMessage)
	streamConnected  chan struct{}
	streamConnectOne sync.Once
}

// newProviderScript returns a healthy, idle provider pinned to the contracted
// version, holding one workspace-scoped session.
func newProviderScript() *providerScript {
	return &providerScript{
		version: PinnedServerVersion,
		attached: providerSession{
			ID: fixtureSessionID, Directory: "/workspace", Title: "Claw", Version: PinnedServerVersion,
		},
		phase:           phaseIdle,
		streamConnected: make(chan struct{}),
	}
}

// scriptedProvider builds a MockProvider that delegates every call to script.
// The mock is what the runtime holds, so the seam under test is the real
// provider interface; the script supplies the answers.
func scriptedProvider(script *providerScript) *MockProvider {
	mock := NewMockProvider(gomock.NewController(GinkgoT()))
	mock.EXPECT().health(gomock.Any()).AnyTimes().DoAndReturn(script.health)
	mock.EXPECT().createSession(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(script.createSession)
	mock.EXPECT().session(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(script.session)
	mock.EXPECT().rehydrate(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(script.rehydrate)
	mock.EXPECT().promptAsync(
		gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(),
	).AnyTimes().DoAndReturn(script.promptAsync)
	mock.EXPECT().abort(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(script.abort)
	mock.EXPECT().streamEvents(gomock.Any(), gomock.Any()).AnyTimes().DoAndReturn(script.streamEvents)
	return mock
}

// ---- provider implementation --------------------------------------------

func (script *providerScript) health(ctx context.Context) (bool, string, error) {
	script.mu.Lock()
	defer script.mu.Unlock()
	return true, script.version, nil
}

func (script *providerScript) createSession(_ context.Context, _ string) (providerSession, error) {
	script.mu.Lock()
	defer script.mu.Unlock()
	script.created++
	return script.attached, nil
}

func (script *providerScript) session(_ context.Context, sessionID string) (providerSession, error) {
	script.mu.Lock()
	defer script.mu.Unlock()
	script.sessionReads = append(script.sessionReads, sessionID)
	return script.attached, nil
}

// rehydrate answers one transcript read, reporting coherence the way the real
// adapter does: the phase is sampled before and after the transcript, and a
// scripted phase sequence is consumed one entry per sample, so a spec can drive
// a read that straddles a phase change.
func (script *providerScript) rehydrate(
	ctx context.Context,
	_ string,
) ([]providerMessage, sessionPhase, bool, error) {
	script.mu.Lock()
	before := script.nextPhaseLocked()
	if script.staleMessages != nil {
		snapshot := cloneMessages(script.staleMessages)
		script.staleMessages = nil
		script.completeLatestLocked(script.staleCompletion)
		close(script.staleServed)
		after := script.nextPhaseLocked()
		script.mu.Unlock()
		return snapshot, after, before == after, nil
	}
	release := script.freshRelease
	script.mu.Unlock()
	if release != nil {
		select {
		case <-ctx.Done():
			return nil, "", false, ctx.Err()
		case <-release:
		}
	}
	script.mu.Lock()
	defer script.mu.Unlock()
	messages := cloneMessages(script.messages)
	after := script.nextPhaseLocked()
	return messages, after, before == after, nil
}

// promptAsync records one submission. When prompts are held it blocks until the
// caller cancels, which is how a spec exercises cancellation of a submission
// the provider never answers.
func (script *providerScript) promptAsync(
	ctx context.Context,
	_, messageID, prompt, system string,
	model promptModel,
) error {
	script.mu.Lock()
	held := script.holdPrompts
	signal := script.promptHeld
	script.mu.Unlock()
	if held {
		if signal != nil {
			script.promptHeldOne.Do(func() { close(signal) })
		}
		<-ctx.Done()
		return ctx.Err()
	}
	script.mu.Lock()
	defer script.mu.Unlock()
	script.requests = append(script.requests, "prompt:"+prompt)
	script.prompts = append(script.prompts, submittedPrompt{
		MessageID: messageID, System: system, Model: model, Text: prompt,
	})
	script.phase = phaseBusy
	script.appendLocked(userMessage(messageID, prompt))
	return nil
}

func (script *providerScript) abort(_ context.Context, _ string) error {
	script.mu.Lock()
	defer script.mu.Unlock()
	script.requests = append(script.requests, "abort")
	if script.abortFailure != nil {
		return script.abortFailure
	}
	if script.abortErrorName != "" {
		script.appendErrorLocked(
			script.latestUserIDLocked(), script.abortErrorName,
			script.abortErrorMessage, script.abortToolInterrupted,
		)
	}
	script.phase = phaseIdle
	return nil
}

// streamEvents holds the subscription open until the session ends, recording
// the callback so a spec can deliver an invalidation through it.
func (script *providerScript) streamEvents(ctx context.Context, receive func(event json.RawMessage)) error {
	script.mu.Lock()
	script.receive = receive
	script.mu.Unlock()
	script.streamConnectOne.Do(func() { close(script.streamConnected) })
	<-ctx.Done()
	script.mu.Lock()
	script.receive = nil
	script.mu.Unlock()
	return ctx.Err()
}

// appendLocked adds one built message to the transcript, recording a violation
// instead of asserting when the fixture itself is malformed. It is reached from
// the runtime's goroutine through the scripted stale read, where a Ginkgo
// failure would have no spec to land on.
func (script *providerScript) appendLocked(message providerMessage, err error) {
	if err != nil {
		script.violations = append(script.violations, err.Error())
		return
	}
	script.messages = append(script.messages, message)
}

// contractViolations reports every malformed fixture the script built.
func (script *providerScript) contractViolations() []string {
	script.mu.Lock()
	defer script.mu.Unlock()
	return append([]string(nil), script.violations...)
}

// nextPhaseLocked reports the phase for one sample, consuming the scripted
// sequence first.
func (script *providerScript) nextPhaseLocked() sessionPhase {
	if len(script.phaseScript) == 0 {
		return script.phase
	}
	phase := script.phaseScript[0]
	script.phaseScript = script.phaseScript[1:]
	return phase
}

// cloneMessages deep-copies a transcript down to its parts, because the
// scripting helpers below mutate message parts in place while the runtime is
// reading the snapshot it was handed.
func cloneMessages(messages []providerMessage) []providerMessage {
	cloned := make([]providerMessage, len(messages))
	for index, message := range messages {
		message.Parts = append([]providerPart(nil), message.Parts...)
		cloned[index] = message
	}
	return cloned
}

// ---- scripting -----------------------------------------------------------

// withVersion makes the provider report a version other than the pinned one.
func (script *providerScript) withVersion(version string) *providerScript {
	script.version = version
	return script
}

// withPhase sets the phase the session reports before any prompt.
func (script *providerScript) withPhase(phase sessionPhase) *providerScript {
	script.phase = phase
	return script
}

// withPhaseScript queues phases consumed one per sample, so a spec can drive an
// incoherent transcript read.
func (script *providerScript) withPhaseScript(phases ...sessionPhase) *providerScript {
	script.phaseScript = phases
	return script
}

// withTranscript seeds history that predates the runtime attaching.
func (script *providerScript) withTranscript(messages ...providerMessage) *providerScript {
	script.messages = messages
	return script
}

// withAbortError makes an accepted abort record a provider error on the turn it
// stopped, optionally including an interrupted tool part.
func (script *providerScript) withAbortError(name, failure string, interruptedTool bool) *providerScript {
	script.abortErrorName, script.abortErrorMessage = name, failure
	script.abortToolInterrupted = interruptedTool
	return script
}

// withAbortFailure makes the abort operation fail.
func (script *providerScript) withAbortFailure(failure error) *providerScript {
	script.abortFailure = failure
	return script
}

// withHeldPrompts makes every submission block until its caller cancels, and
// reports a channel closed once the first submission is being held.
func (script *providerScript) withHeldPrompts() (*providerScript, <-chan struct{}) {
	script.holdPrompts = true
	script.promptHeld = make(chan struct{})
	return script, script.promptHeld
}

// ---- transcript mutation -------------------------------------------------

// streamAssistant appends an in-progress assistant reply and marks the session
// busy.
func (script *providerScript) streamAssistant(parentID, assistantID, content string) {
	script.mu.Lock()
	defer script.mu.Unlock()
	script.phase = phaseBusy
	script.appendLocked(assistantMessage(assistantID, parentID, content, nil, nil))
}

// completeAssistant finalizes an existing assistant reply and reports idle. It
// rebuilds the message rather than editing it: a decoded message keeps its
// content inside the SDK union, which an in-place field write would not reach.
func (script *providerScript) completeAssistant(assistantID, content string) {
	script.mu.Lock()
	defer script.mu.Unlock()
	completed := fixtureMillis()
	for index := range script.messages {
		if script.messages[index].Info.ID != assistantID {
			continue
		}
		rebuilt, err := assistantMessage(
			assistantID, script.messages[index].Info.ParentID, content, &completed, nil,
		)
		if err != nil {
			script.violations = append(script.violations, err.Error())
			continue
		}
		script.messages[index] = rebuilt
	}
	script.phase = phaseIdle
}

// completeLatest answers the most recent prompt and reports idle.
func (script *providerScript) completeLatest(content string) {
	script.mu.Lock()
	defer script.mu.Unlock()
	script.completeLatestLocked(content)
}

func (script *providerScript) completeLatestLocked(content string) {
	assistantID := fmt.Sprintf("msg_answer_%d", len(script.messages))
	completed := fixtureMillis()
	script.appendLocked(assistantMessage(assistantID, script.latestUserIDLocked(), content, &completed, nil))
	script.phase = phaseIdle
}

// completeWithTool answers parentID with a completed tool call and reports idle.
func (script *providerScript) completeWithTool(parentID, callID string) {
	script.mu.Lock()
	defer script.mu.Unlock()
	completed := fixtureMillis()
	assistantID := fmt.Sprintf("msg_tool_%d", len(script.messages))
	script.appendLocked(toolAssistantMessage(assistantID, parentID, callID, "inspection complete", completed))
	script.phase = phaseIdle
}

// appendHistoricalReplies attaches a delta, a final message, and a completed
// tool call to an already-concluded turn, without changing the phase.
func (script *providerScript) appendHistoricalReplies(parentID string) {
	script.mu.Lock()
	defer script.mu.Unlock()
	completed := fixtureMillis()
	script.appendLocked(assistantMessage("msg_late_delta", parentID, "late delta", nil, nil))
	script.appendLocked(assistantMessage("msg_late_final", parentID, "late final", &completed, nil))
	script.appendLocked(toolAssistantMessage("msg_late_tool", parentID, "call_late_tool", "late output", completed))
}

// failLatest fails the most recent prompt and reports idle.
func (script *providerScript) failLatest(name, failure string) {
	script.mu.Lock()
	defer script.mu.Unlock()
	script.appendErrorLocked(script.latestUserIDLocked(), name, failure, false)
	script.phase = phaseIdle
}

// failTurn fails one specific turn and reports idle.
func (script *providerScript) failTurn(parentID, name, failure string) {
	script.mu.Lock()
	defer script.mu.Unlock()
	script.appendErrorLocked(parentID, name, failure, false)
	script.phase = phaseIdle
}

// appendError fails one turn without changing the phase.
func (script *providerScript) appendError(parentID, name, failure string) {
	script.mu.Lock()
	defer script.mu.Unlock()
	script.appendErrorLocked(parentID, name, failure, false)
}

func (script *providerScript) appendErrorLocked(parentID, name, failure string, interruptedTool bool) {
	assistantID := fmt.Sprintf("msg_error_%d", len(script.messages))
	completed := fixtureMillis()
	parts := []any{textPart(assistantID, "")}
	if interruptedTool {
		parts = append(parts, interruptedToolPart(assistantID))
	}
	script.appendLocked(assistantMessageWithParts(
		assistantID, parentID, &completed, providerFailure(name, failure), parts...,
	))
}

// completeAfterStaleRead scripts one transcript read that answers with a
// snapshot taken before the turn completed, while the turn completes underneath
// it. It reports a channel closed once that stale snapshot has been served, and
// a function that releases the next, coherent read.
func (script *providerScript) completeAfterStaleRead(content string) (<-chan struct{}, func()) {
	script.mu.Lock()
	script.staleMessages = cloneMessages(script.messages)
	script.staleCompletion = content
	script.staleServed = make(chan struct{})
	script.freshRelease = make(chan struct{})
	served, release := script.staleServed, script.freshRelease
	script.mu.Unlock()
	var once sync.Once
	return served, func() { once.Do(func() { close(release) }) }
}

// publishSessionEvent delivers one invalidation naming the attached session,
// which is how a spec makes the runtime reconcile without waiting for the poll
// interval. It goes through the same raw envelope the real stream delivers, so
// the event filter stays exercised by behavior specs too.
func (script *providerScript) publishSessionEvent(eventType string) {
	script.mu.Lock()
	receive := script.receive
	sessionID := script.attached.ID
	script.mu.Unlock()
	if receive == nil {
		return
	}
	receive(json.RawMessage(
		`{"type":"` + eventType + `","properties":{"sessionID":"` + sessionID + `"}}`,
	))
}

// ---- recorded observations ----------------------------------------------

// promptContents reports the text of every accepted prompt, in order.
func (script *providerScript) promptContents() []string {
	script.mu.Lock()
	defer script.mu.Unlock()
	contents := make([]string, 0, len(script.prompts))
	for _, prompt := range script.prompts {
		contents = append(contents, prompt.Text)
	}
	return contents
}

// submittedPrompts reports every accepted prompt as the provider received it.
func (script *providerScript) submittedPrompts() []submittedPrompt {
	script.mu.Lock()
	defer script.mu.Unlock()
	return append([]submittedPrompt(nil), script.prompts...)
}

// promptMessageID reports the message ID minted for a prompt, or the empty
// string until that prompt has been accepted.
func (script *providerScript) promptMessageID(content string) string {
	script.mu.Lock()
	defer script.mu.Unlock()
	for _, prompt := range script.prompts {
		if prompt.Text == content {
			return prompt.MessageID
		}
	}
	return ""
}

// requestOrder reports the ordered log of prompt and abort operations.
func (script *providerScript) requestOrder() []string {
	script.mu.Lock()
	defer script.mu.Unlock()
	return append([]string(nil), script.requests...)
}

// sessionReadIDs reports every session that was read rather than created.
func (script *providerScript) sessionReadIDs() []string {
	script.mu.Lock()
	defer script.mu.Unlock()
	return append([]string(nil), script.sessionReads...)
}

// createdSessions reports how many sessions the runtime created.
func (script *providerScript) createdSessions() int {
	script.mu.Lock()
	defer script.mu.Unlock()
	return script.created
}

// eventStreamConnected is closed once the runtime has subscribed to events.
func (script *providerScript) eventStreamConnected() <-chan struct{} {
	return script.streamConnected
}

func (script *providerScript) latestUserIDLocked() string {
	for index := len(script.messages) - 1; index >= 0; index-- {
		if script.messages[index].Info.Role == opencodesdk.MessageRoleUser {
			return script.messages[index].Info.ID
		}
	}
	return ""
}
