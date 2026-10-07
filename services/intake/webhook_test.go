// Copyright 2026 Candace Labs

package intake_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	stdhttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/kernel/clock"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	intakev1 "github.com/candacelabs/csf/proto/candace/intake/v1"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/intake"
)

const (
	hookSecret      = "webhook-secret-for-specs"
	hookRepository  = "candacelabs/csf"
	hookBranch      = "dev/github-events"
	hookPullRequest = "https://github.com/candacelabs/csf/pull/412"
	ownerAssignment = "1f0e2d3c-4b5a-4968-8776-655443322110"
	otherAssignment = "2a1b0c9d-8e7f-4a6b-9c5d-4e3f2a1b0c9d"
	endedAssignment = "3b2c1d0e-9f8a-4b7c-8d6e-5f4a3b2c1d0e"
	mergedDelivery  = "6a1c2e30-a3b1-11f0-8b2e-1a2b3c4d5e6f"
	commentDelivery = "7b2d3f41-a3b1-11f0-8b2e-1a2b3c4d5e6f"
	checkDelivery   = "8c3e4052-a3b1-11f0-8b2e-1a2b3c4d5e6f"
	pingDelivery    = "9d4f5163-a3b1-11f0-8b2e-1a2b3c4d5e6f"
	missedDelivery  = "ae506274-a3b1-11f0-8b2e-1a2b3c4d5e6f"
	olderDelivery   = "bf617385-a3b1-11f0-8b2e-1a2b3c4d5e6f"
	mergedSlice     = "github-events"
	hookID          = 501
	otherHookID     = 502
	nextPage        = "cursor-2"
)

// The deliveries, reduced to the fields the receiver reads, as GitHub sends
// them for a pull request merged into main, a comment on it, a failed check
// on its branch, a release, a push and a hook's ping.
const (
	mergedPayload = `{"action":"closed","number":412,"pull_request":{"id":3000000412,"number":412,` +
		`"title":"GITHUB-EVENTS (#401): CSF hears GitHub","html_url":"https://github.com/candacelabs/csf/pull/412","merged":true,` +
		`"head":{"ref":"dev/github-events"},"base":{"ref":"main"},"user":{"login":"octocat"},` +
		`"created_at":"2026-10-05T06:00:00Z","closed_at":"2026-10-05T06:59:58Z","merged_at":"2026-10-05T06:59:58Z"},` +
		`"repository":{"full_name":"candacelabs/csf"},"sender":{"login":"octocat"}}`
	commentPayload = `{"action":"created","issue":{"id":1000412,"number":412,"title":"GITHUB-EVENTS",` +
		`"html_url":"https://github.com/candacelabs/csf/pull/412","pull_request":{"url":"https://api.github.com/repos/candacelabs/csf/pulls/412"}},` +
		`"comment":{"id":4400000001,"html_url":"https://github.com/candacelabs/csf/pull/412#issuecomment-4400000001",` +
		`"body":"Please rename the stream constant.","user":{"login":"octocat"},"created_at":"2026-10-05T06:59:30Z"},` +
		`"repository":{"full_name":"candacelabs/csf"},"sender":{"login":"octocat"}}`
	checkRunPayload = `{"action":"completed","check_run":{"id":5500000001,"name":"house-lint","conclusion":"failure",` +
		`"html_url":"https://github.com/candacelabs/csf/runs/5500000001","completed_at":"2026-10-05T06:58:00Z",` +
		`"check_suite":{"id":66,"head_branch":"dev/github-events"},"pull_requests":[{"number":412}]},` +
		`"repository":{"full_name":"candacelabs/csf"},"sender":{"login":"octocat"}}`
	releasePayload = `{"action":"published","release":{"id":77,"tag_name":"csf-4ed91b355e21","name":"csf-4ed91b355e21",` +
		`"html_url":"https://github.com/candacelabs/csf/releases/tag/csf-4ed91b355e21","author":{"login":"octocat"},` +
		`"published_at":"2026-10-05T06:59:00Z"},"repository":{"full_name":"candacelabs/csf"},"sender":{"login":"octocat"}}`
	pushPayload = `{"ref":"refs/heads/main","after":"4ed91b355e21a05f9caac3940cb2dc44cd87835d",` +
		`"compare":"https://github.com/candacelabs/csf/compare/9d092e0...4ed91b3",` +
		`"head_commit":{"id":"4ed91b355e21a05f9caac3940cb2dc44cd87835d","message":"GITHUB-EVENTS (#401)\n\nbody","timestamp":"2026-10-05T06:59:58Z"},` +
		`"repository":{"full_name":"candacelabs/csf"},"sender":{"login":"octocat"}}`
	pingPayload = `{"zen":"Keep it logically awesome.","hook_id":501,"repository":{"full_name":"candacelabs/csf"},"sender":{"login":"octocat"}}`
)

// hookNow is the moment every receiver spec's clock reads: two seconds after
// the merge.
var hookNow = time.Date(2026, 10, 5, 7, 0, 0, 0, time.UTC)

// signedHeader is the header GitHub sends with body, signed under secret.
func signedHeader(name string, delivery string, body string, secret string) stdhttp.Header {
	header := stdhttp.Header{}
	header.Set(intake.HeaderEvent, name)
	header.Set(intake.HeaderDelivery, delivery)
	header.Set(intake.HeaderSignature, intake.Sign([]byte(body), []byte(secret)))
	return header
}

// streamRecords reads every record of the GitHub event stream under state.
func streamRecords(state string) []map[string]any {
	file, err := os.Open(filepath.Join(state, intake.StreamAssignment, session.EventsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = file.Close() }()
	var records []map[string]any
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		record := map[string]any{}
		Expect(json.Unmarshal(scanner.Bytes(), &record)).To(Succeed())
		records = append(records, record)
	}
	return records
}

// recordsOf is the stream's records of one type.
func recordsOf(state string, eventType string) []map[string]any {
	var matching []map[string]any
	for _, record := range streamRecords(state) {
		if record[session.KeyEventType] == eventType {
			matching = append(matching, record)
		}
	}
	return matching
}

func staticSecret(secret string) func() ([]byte, error) {
	return func() ([]byte, error) { return []byte(secret), nil }
}

func newReceiver(state string, options ...intake.WebhookOption) *intake.WebhookReceiver {
	receiver, err := intake.NewWebhookReceiver(append([]intake.WebhookOption{
		intake.WithStateDirectory(state), intake.WithWebhookSecret(staticSecret(hookSecret)),
		intake.WithWebhookClock(clock.NewManualClock(hookNow)),
	}, options...)...)
	Expect(err).NotTo(HaveOccurred())
	return receiver
}

var _ = Describe("the webhook receiver", func() {
	var (
		ctx   context.Context
		state string
	)

	BeforeEach(func() {
		ctx = context.Background()
		state = GinkgoT().TempDir()
	})

	It("turns a signed merge into a typed event on the stream csf events reads, with the raw delivery kept", func() {
		receiver := newReceiver(state)
		status, answer := receiver.Receive(ctx, signedHeader(intake.WebhookPullRequest, mergedDelivery, mergedPayload, hookSecret), strings.NewReader(mergedPayload))
		Expect(status).To(Equal(stdhttp.StatusAccepted))
		Expect(answer).To(Equal(intake.Answer{Delivery: mergedDelivery, Event: intake.WebhookPullRequest, Outcome: intake.OutcomeAccepted, Events: []string{"pull_request_merged"}}))

		events := recordsOf(state, intake.EventTypeEvent)
		Expect(events).To(HaveLen(1))
		Expect(events[0]).To(SatisfyAll(
			HaveKeyWithValue(intake.KeyKind, "pull_request_merged"),
			HaveKeyWithValue(intake.KeyRepository, hookRepository),
			HaveKeyWithValue(intake.KeyNumber, BeNumerically("==", 412)),
			HaveKeyWithValue(intake.KeyBranch, hookBranch),
			HaveKeyWithValue(intake.KeyBase, intake.MainBranch),
			HaveKeyWithValue(intake.KeyURL, hookPullRequest),
			HaveKeyWithValue(intake.KeyDelivery, mergedDelivery),
			HaveKeyWithValue(intake.KeyLatency, BeNumerically("==", 2)),
			HaveKeyWithValue(session.KeySessionID, intake.StreamSession),
		))
		Expect(recordsOf(state, intake.EventTypeDelivery)).To(ConsistOf(HaveKeyWithValue(intake.KeyOutcome, string(intake.OutcomeAccepted))))
		kept, err := os.ReadFile(filepath.Join(state, intake.StreamAssignment, intake.DeliveriesDirectory, mergedDelivery+".json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(kept)).To(Equal(mergedPayload))
		_, err = session.ReadRunState(filepath.Join(state, intake.StreamAssignment))
		Expect(err).To(MatchError(session.ErrNoRunState), "the stream is never resumed as a session")
		Expect(receiver.Counts().Events(intakev1.EventKind_EVENT_KIND_PULL_REQUEST_MERGED)).To(BeNumerically("==", 1))
	})

	It("refuses a forged signature, keeps nothing and routes nothing", func() {
		routed := 0
		receiver := newReceiver(state, intake.WithEventRoutes(func(_ context.Context, _ *intakev1.Event) (intake.Routed, error) {
			routed++
			return intake.Routed{}, nil
		}))
		forged := signedHeader(intake.WebhookPullRequest, mergedDelivery, mergedPayload, "not-the-secret")
		status, answer := receiver.Receive(ctx, forged, strings.NewReader(mergedPayload))
		Expect(status).To(Equal(stdhttp.StatusUnauthorized))
		Expect(answer.Outcome).To(Equal(intake.OutcomeRefused))
		Expect(answer.Refusal).To(Equal(intake.RefusedSignature))
		Expect(routed).To(BeZero())
		Expect(recordsOf(state, intake.EventTypeEvent)).To(BeEmpty())
		Expect(recordsOf(state, intake.EventTypeDelivery)).To(ConsistOf(HaveKeyWithValue(intake.KeyRefusal, string(intake.RefusedSignature))))
		Expect(filepath.Join(state, intake.StreamAssignment, intake.DeliveriesDirectory, mergedDelivery+".json")).NotTo(BeAnExistingFile())
		Expect(receiver.Counts().Deliveries(intake.OutcomeRefused, intake.RefusedSignature)).To(BeNumerically("==", 1))
	})

	It("refuses a delivery with no signature, or one signed with sha1", func() {
		receiver := newReceiver(state)
		unsigned := signedHeader(intake.WebhookPullRequest, mergedDelivery, mergedPayload, hookSecret)
		unsigned.Del(intake.HeaderSignature)
		status, answer := receiver.Receive(ctx, unsigned, strings.NewReader(mergedPayload))
		Expect(status).To(Equal(stdhttp.StatusUnauthorized))
		Expect(answer.Refusal).To(Equal(intake.RefusedSignature))
		sha1 := signedHeader(intake.WebhookPullRequest, mergedDelivery, mergedPayload, hookSecret)
		sha1.Set(intake.HeaderSignature, strings.Replace(sha1.Get(intake.HeaderSignature), "sha256=", "sha1=", 1))
		status, _ = receiver.Receive(ctx, sha1, strings.NewReader(mergedPayload))
		Expect(status).To(Equal(stdhttp.StatusUnauthorized))
	})

	It("refuses everything with a typed reason while no secret is configured", func() {
		receiver, err := intake.NewWebhookReceiver(intake.WithStateDirectory(state),
			intake.WithWebhookSecret(func() ([]byte, error) { return nil, intake.ErrNoSecret }))
		Expect(err).NotTo(HaveOccurred())
		status, answer := receiver.Receive(ctx, signedHeader(intake.WebhookPullRequest, mergedDelivery, mergedPayload, hookSecret), strings.NewReader(mergedPayload))
		Expect(status).To(Equal(stdhttp.StatusServiceUnavailable))
		Expect(answer.Refusal).To(Equal(intake.RefusedNoSecret))
		Expect(recordsOf(state, intake.EventTypeDelivery)).To(ConsistOf(HaveKeyWithValue(intake.KeyRefusal, string(intake.RefusedNoSecret))))
	})

	It("refuses a delivery identifier that could name another file", func() {
		receiver := newReceiver(state)
		status, answer := receiver.Receive(ctx, signedHeader(intake.WebhookPullRequest, "../../run", mergedPayload, hookSecret), strings.NewReader(mergedPayload))
		Expect(status).To(Equal(stdhttp.StatusBadRequest))
		Expect(answer.Refusal).To(Equal(intake.RefusedDelivery))
	})

	It("answers a redelivery of a kept delivery as a duplicate and routes it once", func() {
		routed := 0
		receiver := newReceiver(state, intake.WithEventRoutes(func(_ context.Context, _ *intakev1.Event) (intake.Routed, error) {
			routed++
			return intake.Routed{}, nil
		}))
		header := signedHeader(intake.WebhookPullRequest, mergedDelivery, mergedPayload, hookSecret)
		status, _ := receiver.Receive(ctx, header, strings.NewReader(mergedPayload))
		Expect(status).To(Equal(stdhttp.StatusAccepted))
		status, answer := receiver.Receive(ctx, header, strings.NewReader(mergedPayload))
		Expect(status).To(Equal(stdhttp.StatusOK))
		Expect(answer.Outcome).To(Equal(intake.OutcomeDuplicate))
		Expect(routed).To(Equal(1))
		Expect(recordsOf(state, intake.EventTypeEvent)).To(HaveLen(1))
	})

	It("acknowledges a ping and records it as ignored", func() {
		receiver := newReceiver(state)
		status, answer := receiver.Receive(ctx, signedHeader(intake.WebhookPing, pingDelivery, pingPayload, hookSecret), strings.NewReader(pingPayload))
		Expect(status).To(Equal(stdhttp.StatusAccepted))
		Expect(answer.Outcome).To(Equal(intake.OutcomeIgnored))
		Expect(recordsOf(state, intake.EventTypeEvent)).To(BeEmpty())
	})

	It("is served on its route, refusing a forged delivery with a JSON answer", func() {
		gin.SetMode(gin.TestMode)
		engine := gin.New()
		newReceiver(state).Register(engine)
		request := httptest.NewRequest(stdhttp.MethodPost, intake.WebhookPath, strings.NewReader(mergedPayload))
		request.Header = signedHeader(intake.WebhookPullRequest, mergedDelivery, mergedPayload, "forged")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, request)
		Expect(recorder.Code).To(Equal(stdhttp.StatusUnauthorized))
		Expect(recorder.Body.String()).To(MatchJSON(`{"delivery":"` + mergedDelivery + `","event":"pull_request","outcome":"refused","refusal":"bad_signature"}`))
	})

	DescribeTable("normalizes each delivery CSF acts on into its typed event",
		func(name string, payload string, kind intakev1.EventKind, branch string, conclusion string) {
			events, handled, err := intake.NormalizeDelivery(name, mergedDelivery, []byte(payload))
			Expect(err).NotTo(HaveOccurred())
			Expect(handled).To(BeTrue())
			Expect(events).To(HaveLen(1))
			Expect(events[0].GetKind()).To(Equal(kind))
			Expect(events[0].GetBranch()).To(Equal(branch))
			Expect(events[0].GetConclusion()).To(Equal(conclusion))
			Expect(events[0].GetDelivery()).To(Equal(mergedDelivery))
			Expect(errors.Join(intakev1.ValidateEvent(events[0]), intakev1.ValidateSubject(events[0].GetSubject()))).To(Succeed())
		},
		Entry("a merge", intake.WebhookPullRequest, mergedPayload, intakev1.EventKind_EVENT_KIND_PULL_REQUEST_MERGED, hookBranch, ""),
		Entry("a closed, unmerged pull request", intake.WebhookPullRequest, strings.Replace(mergedPayload, `"merged":true`, `"merged":false`, 1), intakev1.EventKind_EVENT_KIND_PULL_REQUEST_CLOSED, hookBranch, ""),
		Entry("an opened pull request", intake.WebhookPullRequest, strings.Replace(mergedPayload, `"action":"closed"`, `"action":"opened"`, 1), intakev1.EventKind_EVENT_KIND_PULL_REQUEST_OPENED, hookBranch, ""),
		Entry("a comment on a pull request", intake.WebhookIssueComment, commentPayload, intakev1.EventKind_EVENT_KIND_COMMENT, "", ""),
		Entry("a failed check run", intake.WebhookCheckRun, checkRunPayload, intakev1.EventKind_EVENT_KIND_CHECK_RUN_COMPLETED, hookBranch, "failure"),
		Entry("a release", intake.WebhookRelease, releasePayload, intakev1.EventKind_EVENT_KIND_RELEASE_PUBLISHED, "", ""),
		Entry("a push to main", intake.WebhookPush, pushPayload, intakev1.EventKind_EVENT_KIND_PUSH, intake.MainBranch, ""),
		Entry("a submitted review", intake.WebhookPullRequestReview,
			`{"action":"submitted","review":{"id":9,"state":"CHANGES_REQUESTED","body":"no","html_url":"https://github.com/candacelabs/csf/pull/412#r9","user":{"login":"octocat"},"submitted_at":"2026-10-05T06:59:00Z"},`+
				`"pull_request":{"number":412,"head":{"ref":"dev/github-events"},"base":{"ref":"main"}},"repository":{"full_name":"candacelabs/csf"},"sender":{"login":"octocat"}}`,
			intakev1.EventKind_EVENT_KIND_REVIEW, hookBranch, "changes_requested"),
		Entry("an opened issue", intake.WebhookIssues,
			`{"action":"opened","issue":{"id":8,"number":420,"title":"a bug","html_url":"https://github.com/candacelabs/csf/issues/420","created_at":"2026-10-05T06:59:00Z"},"repository":{"full_name":"candacelabs/csf"},"sender":{"login":"octocat"}}`,
			intakev1.EventKind_EVENT_KIND_ISSUE_OPENED, "", ""),
	)

	It("leaves an action CSF does not act on unnormalized, and an unknown event unhandled", func() {
		events, handled, err := intake.NormalizeDelivery(intake.WebhookPullRequest, mergedDelivery, []byte(strings.Replace(mergedPayload, `"action":"closed"`, `"action":"labeled"`, 1)))
		Expect(err).NotTo(HaveOccurred())
		Expect(handled).To(BeTrue())
		Expect(events).To(BeEmpty())
		_, handled, err = intake.NormalizeDelivery("star", mergedDelivery, []byte(`{}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(handled).To(BeFalse())
	})

	Describe("routing", func() {
		var (
			controller *gomock.Controller
			sessions   *MockISessionMessenger
		)

		BeforeEach(func() {
			controller = gomock.NewController(GinkgoT())
			sessions = NewMockISessionMessenger(controller)
			sessions.EXPECT().List(gomock.Any(), gomock.Any()).Return(&harnessv1.ListAgentSessionsResponse{Sessions: []*harnessv1.AgentSessionState{
				{AssignmentId: endedAssignment, Branch: hookBranch, Phase: harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CLOSED},
				{AssignmentId: otherAssignment, Branch: "dev/other", Phase: harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN},
				{AssignmentId: ownerAssignment, Branch: hookBranch, PullRequestUrl: hookPullRequest, Phase: harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN},
			}}, nil).AnyTimes()
		})

		It("queues a comment on a pull request as a message in the live session whose pull request it is", func() {
			sessions.EXPECT().Send(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, request *harnessv1.SendAgentSessionMessageRequest) (*harnessv1.SendAgentSessionMessageResponse, error) {
					Expect(request.GetAssignmentId()).To(Equal(ownerAssignment))
					Expect(request.GetMessage()).To(ContainSubstring("Please rename the stream constant."))
					Expect(request.GetMessage()).To(ContainSubstring("#issuecomment-4400000001"))
					Expect(harnessv1.ValidateSendAgentSessionMessageRequest(request)).To(Succeed())
					return &harnessv1.SendAgentSessionMessageResponse{TurnId: "2"}, nil
				})
			receiver := newReceiver(state, intake.WithEventRoutes(intake.SessionRoute(sessions)))
			status, _ := receiver.Receive(ctx, signedHeader(intake.WebhookIssueComment, commentDelivery, commentPayload, hookSecret), strings.NewReader(commentPayload))
			Expect(status).To(Equal(stdhttp.StatusAccepted))
			Expect(recordsOf(state, intake.EventTypeRouted)).To(ConsistOf(SatisfyAll(
				HaveKeyWithValue(intake.KeyTarget, intake.TargetSession),
				HaveKeyWithValue(intake.KeyDetail, ownerAssignment),
			)))
		})

		It("queues a failed check in the live session whose branch it ran on, never the ended one", func() {
			sessions.EXPECT().Send(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, request *harnessv1.SendAgentSessionMessageRequest) (*harnessv1.SendAgentSessionMessageResponse, error) {
					Expect(request.GetAssignmentId()).To(Equal(ownerAssignment))
					return &harnessv1.SendAgentSessionMessageResponse{}, nil
				})
			receiver := newReceiver(state, intake.WithEventRoutes(intake.SessionRoute(sessions)))
			receiver.Receive(ctx, signedHeader(intake.WebhookCheckRun, checkDelivery, checkRunPayload, hookSecret), strings.NewReader(checkRunPayload))
		})

		It("sends a passing check, a merge and a push to no session", func() {
			receiver := newReceiver(state, intake.WithEventRoutes(intake.SessionRoute(sessions)))
			passed := strings.Replace(checkRunPayload, `"conclusion":"failure"`, `"conclusion":"success"`, 1)
			receiver.Receive(ctx, signedHeader(intake.WebhookCheckRun, checkDelivery, passed, hookSecret), strings.NewReader(passed))
			receiver.Receive(ctx, signedHeader(intake.WebhookPullRequest, mergedDelivery, mergedPayload, hookSecret), strings.NewReader(mergedPayload))
			receiver.Receive(ctx, signedHeader(intake.WebhookPush, pingDelivery, pushPayload, hookSecret), strings.NewReader(pushPayload))
			Expect(recordsOf(state, intake.EventTypeRouted)).To(BeEmpty())
		})

		It("records a send the harness refused, and still accepts the delivery", func() {
			sessions.EXPECT().Send(gomock.Any(), gomock.Any()).Return(nil, errors.New("session is canceling"))
			receiver := newReceiver(state, intake.WithEventRoutes(intake.SessionRoute(sessions)))
			status, _ := receiver.Receive(ctx, signedHeader(intake.WebhookIssueComment, commentDelivery, commentPayload, hookSecret), strings.NewReader(commentPayload))
			Expect(status).To(Equal(stdhttp.StatusAccepted))
			Expect(recordsOf(state, intake.EventTypeRouted)).To(ConsistOf(HaveKeyWithValue(session.KeyError, ContainSubstring("session is canceling"))))
		})

		It("marks a merge into main on the dispatcher, and only a merge into main", func() {
			var merged []string
			route := intake.MergeRoute(func(_ context.Context, url string) ([]string, error) {
				merged = append(merged, url)
				return []string{mergedSlice}, nil
			})
			receiver := newReceiver(state, intake.WithEventRoutes(route))
			receiver.Receive(ctx, signedHeader(intake.WebhookPullRequest, mergedDelivery, mergedPayload, hookSecret), strings.NewReader(mergedPayload))
			elsewhere := strings.Replace(mergedPayload, `"base":{"ref":"main"}`, `"base":{"ref":"release"}`, 1)
			receiver.Receive(ctx, signedHeader(intake.WebhookPullRequest, missedDelivery, elsewhere, hookSecret), strings.NewReader(elsewhere))
			Expect(merged).To(Equal([]string{hookPullRequest}))
			Expect(recordsOf(state, intake.EventTypeRouted)).To(ConsistOf(SatisfyAll(
				HaveKeyWithValue(intake.KeyTarget, intake.TargetDispatcher), HaveKeyWithValue(intake.KeyDetail, mergedSlice))))
		})
	})

	Describe("recovery", func() {
		var (
			controller *gomock.Controller
			hooks      *MockIHookDeliveries
			manual     *clock.ManualClock
		)

		BeforeEach(func() {
			controller = gomock.NewController(GinkgoT())
			hooks = NewMockIHookDeliveries(controller)
			manual = clock.NewManualClock(hookNow)
		})

		It("redelivers every delivery since the last one kept that never arrived, and no other", func() {
			receiver, err := intake.NewWebhookReceiver(intake.WithStateDirectory(state), intake.WithWebhookSecret(staticSecret(hookSecret)),
				intake.WithWebhookClock(manual), intake.WithRecovery(hooks, hookRepository))
			Expect(err).NotTo(HaveOccurred())
			receiver.Receive(ctx, signedHeader(intake.WebhookPullRequest, mergedDelivery, mergedPayload, hookSecret), strings.NewReader(mergedPayload))
			manual.Advance(time.Hour)

			hooks.EXPECT().ListHooks(gomock.Any(), hookRepository).Return([]intake.Hook{
				{ID: otherHookID, URL: "https://ci.example.invalid/hook"},
				{ID: hookID, URL: "https://hooks.example.invalid" + intake.WebhookPath},
			}, nil)
			hooks.EXPECT().ListHookDeliveries(gomock.Any(), hookRepository, int64(hookID), "").Return([]intake.HookDelivery{
				{ID: 3, GUID: missedDelivery, Event: intake.WebhookIssueComment, DeliveredAt: hookNow.Add(30 * time.Minute), Redelivery: true},
				{ID: 2, GUID: missedDelivery, Event: intake.WebhookIssueComment, DeliveredAt: hookNow.Add(20 * time.Minute)},
			}, nextPage, nil)
			hooks.EXPECT().ListHookDeliveries(gomock.Any(), hookRepository, int64(hookID), nextPage).Return([]intake.HookDelivery{
				{ID: 1, GUID: mergedDelivery, Event: intake.WebhookPullRequest, DeliveredAt: hookNow, StatusCode: stdhttp.StatusAccepted},
				{ID: 0, GUID: olderDelivery, Event: intake.WebhookPush, DeliveredAt: hookNow.Add(-time.Hour)},
			}, "", nil)
			hooks.EXPECT().RedeliverHookDelivery(gomock.Any(), hookRepository, int64(hookID), int64(3)).Return(nil)

			report, err := receiver.Recover(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(report).To(Equal(intake.RecoveryReport{Redelivered: 1}))
			Expect(recordsOf(state, intake.EventTypeRedelivery)).To(ConsistOf(HaveKeyWithValue(intake.KeyDelivery, missedDelivery)))
		})

		It("looks back GitHub's whole retention when nothing was ever kept, and counts a refused redelivery", func() {
			receiver, err := intake.NewWebhookReceiver(intake.WithStateDirectory(state), intake.WithWebhookSecret(staticSecret(hookSecret)),
				intake.WithWebhookClock(manual), intake.WithRecovery(hooks, hookRepository))
			Expect(err).NotTo(HaveOccurred())
			hooks.EXPECT().ListHooks(gomock.Any(), hookRepository).Return([]intake.Hook{{ID: hookID, URL: "https://hooks.example.invalid" + intake.WebhookPath}}, nil)
			hooks.EXPECT().ListHookDeliveries(gomock.Any(), hookRepository, int64(hookID), "").Return([]intake.HookDelivery{
				{ID: 7, GUID: olderDelivery, DeliveredAt: hookNow.Add(-71 * time.Hour)},
				{ID: 6, GUID: missedDelivery, DeliveredAt: hookNow.Add(-73 * time.Hour)},
			}, "", nil)
			hooks.EXPECT().RedeliverHookDelivery(gomock.Any(), hookRepository, int64(hookID), int64(7)).Return(errors.New("HTTP 422"))
			report, err := receiver.Recover(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(report).To(Equal(intake.RecoveryReport{Failed: 1}))
		})

		It("runs once on Start in a goroutine that joins with its scope", func() {
			baseline := goleak.IgnoreCurrent()
			receiver, err := intake.NewWebhookReceiver(intake.WithStateDirectory(state), intake.WithWebhookSecret(staticSecret(hookSecret)),
				intake.WithWebhookClock(manual), intake.WithRecovery(hooks, hookRepository))
			Expect(err).NotTo(HaveOccurred())
			listed := make(chan struct{})
			hooks.EXPECT().ListHooks(gomock.Any(), hookRepository).DoAndReturn(func(_ context.Context, _ string) ([]intake.Hook, error) {
				close(listed)
				return nil, nil
			})
			scope := runtime.NewScope(ctx, "webhook spec")
			Expect(receiver.Start(scope)).To(Succeed())
			Eventually(listed).Should(BeClosed())
			Expect(receiver.Start(scope)).To(MatchError(intake.ErrAlreadyStarted))
			Expect(scope.Close()).To(Succeed())
			Expect(goleak.Find(baseline)).To(Succeed())
		})
	})

	Describe("misuse", func() {
		It("refuses to build without its state directory or its secret source", func() {
			_, err := intake.NewWebhookReceiver(intake.WithWebhookSecret(staticSecret(hookSecret)))
			Expect(err).To(MatchError(intake.ErrNoStateDirectory))
			_, err = intake.NewWebhookReceiver(intake.WithStateDirectory(state))
			Expect(err).To(MatchError(intake.ErrNoSecretSource))
			_, err = intake.NewWebhookReceiver(intake.WithStateDirectory(state), intake.WithWebhookSecret(nil))
			Expect(err).To(MatchError(intake.ErrNoSecretSource))
			_, err = intake.NewWebhookReceiver(intake.WithStateDirectory(state), intake.WithWebhookSecret(staticSecret(hookSecret)), nil)
			Expect(err).To(HaveOccurred())
			_, err = intake.NewWebhookReceiver(intake.WithStateDirectory(state), intake.WithWebhookSecret(staticSecret(hookSecret)), intake.WithEventRoutes(nil))
			Expect(err).To(HaveOccurred())
			_, err = intake.NewWebhookReceiver(intake.WithStateDirectory(state), intake.WithWebhookSecret(staticSecret(hookSecret)), intake.WithRecovery(nil, hookRepository))
			Expect(err).To(HaveOccurred())
		})

		It("refuses recovery that names no repository, and a Recover never granted", func() {
			controller := gomock.NewController(GinkgoT())
			_, err := intake.NewWebhookReceiver(intake.WithStateDirectory(state), intake.WithWebhookSecret(staticSecret(hookSecret)),
				intake.WithRecovery(NewMockIHookDeliveries(controller)))
			Expect(err).To(MatchError(intake.ErrMissingOption))
			_, err = newReceiver(state).Recover(ctx)
			Expect(err).To(MatchError(intake.ErrMissingOption))
		})

		It("refuses Start without a scope", func() {
			Expect(newReceiver(state).Start(nil)).To(HaveOccurred())
		})
	})
})

var _ = Describe("the webhook secret", func() {
	var providers string

	BeforeEach(func() {
		providers = filepath.Join(GinkgoT().TempDir(), "providers.json")
	})

	It("is github.webhook_secret of an owner-only providers.json", func() {
		Expect(os.WriteFile(providers, []byte(`{"burst":{},"github":{"webhook_secret":"s3cret"}}`), 0o600)).To(Succeed())
		Expect(intake.ReadWebhookSecret(providers)).To(Equal([]byte("s3cret")))
	})

	It("is no secret when the file or the key is missing", func() {
		_, err := intake.ReadWebhookSecret(providers)
		Expect(err).To(MatchError(intake.ErrNoSecret))
		Expect(os.WriteFile(providers, []byte(`{"burst":{}}`), 0o600)).To(Succeed())
		_, err = intake.ReadWebhookSecret(providers)
		Expect(err).To(MatchError(intake.ErrNoSecret))
	})

	It("is refused from a file others may read", func() {
		Expect(os.WriteFile(providers, []byte(`{"github":{"webhook_secret":"s3cret"}}`), 0o600)).To(Succeed())
		Expect(os.Chmod(providers, 0o644)).To(Succeed())
		_, err := intake.ReadWebhookSecret(providers)
		Expect(err).To(HaveOccurred())
		Expect(err).NotTo(MatchError(intake.ErrNoSecret))
	})
})
