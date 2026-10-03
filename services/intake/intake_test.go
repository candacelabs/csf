// Copyright 2026 Candace Labs

package intake_test

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/ipc"
	"github.com/candacelabs/csf/ipc/model/copilot"
	"github.com/candacelabs/csf/pkg/eventually"
	intakev1 "github.com/candacelabs/csf/proto/candace/intake/v1"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/intake"
	"github.com/candacelabs/csf/services/relay"
)

const (
	repository                                = "example/widgets"
	pullRequest                               = 7
	reviewer                    relay.AgentID = "reviewer"
	apiBase                                   = "https://api.github.example.invalid"
	eventsPath                                = "/repos/example/widgets/events?per_page=100"
	runsPath                                  = "/repos/example/widgets/actions/runs?status=failure&per_page=30"
	eventsETag                                = `W/"events-v1"`
	pollInterval                              = 30 * time.Second
	reviewerUUID                              = "5d0c4b5e-8e2f-4f3e-9f53-1b7a3c1d9a07"
	headerETag                                = "ETag"
	headerIfNoneMatch                         = "If-None-Match"
	headerRemaining                           = "X-RateLimit-Remaining"
	headerReset                               = "X-RateLimit-Reset"
	headerRetryAfter                          = "Retry-After"
	headerAuthorization                       = "Authorization"
	headerPollInterval                        = "X-Poll-Interval"
	token                                     = "test-token"
	ignoredBot                                = "csf-bot"
	maxBackoff                                = 10 * time.Minute
	rateLimitReset                            = 90 * time.Second
	rateLimitResetBeyondCeiling               = 5 * time.Hour
	secondaryRetry                            = 45 * time.Second
	askedPollInterval                         = 120 * time.Second
)

// started is the fixed moment every spec's intake starts at: the backlog
// cut-off, so the fixture's 11:00 comment is history and everything after
// noon is news.
var started = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// deliveryBudget bounds a wait for an event to cross the dispatcher; it is
// generous because a loaded CI machine schedules goroutines slowly.
var deliveryBudget = eventually.Budget{Within: 10 * time.Second}

// eventsPage is the repository events feed, newest first as GitHub sends it:
// a comment from the agents' own bot account, a push (not actionable), a
// review and a PR comment after the start, and a comment before it.
const eventsPage = `[
 {"id":"5","type":"IssueCommentEvent","actor":{"login":"csf-bot"},"created_at":"2026-10-01T12:03:30Z",
  "payload":{"action":"created","issue":{"number":7,"pull_request":{"url":"x"}},
   "comment":{"id":505,"html_url":"https://github.com/example/widgets/pull/7#issuecomment-505","body":"rebased","user":{"login":"csf-bot"}}}},
 {"id":"4","type":"PushEvent","actor":{"login":"octo"},"created_at":"2026-10-01T12:03:00Z","payload":{}},
 {"id":"3","type":"PullRequestReviewEvent","actor":{"login":"octo"},"created_at":"2026-10-01T12:02:00Z",
  "payload":{"action":"created","pull_request":{"number":7},
   "review":{"id":502,"state":"CHANGES_REQUESTED","body":"tighten the backoff","html_url":"https://github.com/example/widgets/pull/7#pullrequestreview-502","user":{"login":"octo"}}}},
 {"id":"2","type":"IssueCommentEvent","actor":{"login":"octo"},"created_at":"2026-10-01T12:01:00Z",
  "payload":{"action":"created","issue":{"number":7,"pull_request":{"url":"x"}},
   "comment":{"id":501,"html_url":"https://github.com/example/widgets/pull/7#issuecomment-501","body":"please rebase","user":{"login":"octo"}}}},
 {"id":"1","type":"IssueCommentEvent","actor":{"login":"octo"},"created_at":"2026-10-01T11:00:00Z",
  "payload":{"action":"created","issue":{"number":7,"pull_request":{"url":"x"}},
   "comment":{"id":500,"html_url":"https://github.com/example/widgets/pull/7#issuecomment-500","body":"old news","user":{"login":"octo"}}}}
]`

// runsPage holds one failed run linked to the pull request.
const runsPage = `{"total_count":1,"workflow_runs":[
 {"id":900,"name":"CI","run_attempt":1,"conclusion":"failure","head_branch":"feature",
  "html_url":"https://github.com/example/widgets/actions/runs/900","updated_at":"2026-10-01T12:04:00Z",
  "actor":{"login":"octo"},"pull_requests":[{"number":7}]}
]}`

// reply is one canned GitHub response.
type reply struct {
	status int
	header map[string]string
	body   string
}

// fakeGitHub answers the poller's requests from per-path scripts; a path's
// last reply repeats. It runs on the poller goroutine, the only caller of
// Do, and hands every request to the spec on a channel.
type fakeGitHub struct {
	scripts  map[string][]reply
	requests chan *stdhttp.Request
}

func newFakeGitHub(scripts map[string][]reply) *fakeGitHub {
	return &fakeGitHub{scripts: scripts, requests: make(chan *stdhttp.Request, 64)}
}

func (fake *fakeGitHub) Do(request *stdhttp.Request) (*stdhttp.Response, error) {
	fake.requests <- request
	script := fake.scripts[request.URL.RequestURI()]
	if len(script) == 0 {
		return nil, errors.New("fake GitHub: unscripted path " + request.URL.RequestURI())
	}
	answer := script[0]
	if len(script) > 1 {
		fake.scripts[request.URL.RequestURI()] = script[1:]
	}
	header := stdhttp.Header{}
	for name, value := range answer.header {
		header.Set(name, value)
	}
	return &stdhttp.Response{
		StatusCode: answer.status, Status: strconv.Itoa(answer.status) + " " + stdhttp.StatusText(answer.status),
		Header: header, Body: io.NopCloser(strings.NewReader(answer.body)), Request: request,
	}, nil
}

// takeRequest is the next request the poller sent.
func (fake *fakeGitHub) takeRequest() *stdhttp.Request {
	var request *stdhttp.Request
	Eventually(fake.requests).WithTimeout(deliveryBudget.Within).Should(Receive(&request))
	return request
}

// fakeClock stands still at started. Every wait the poller asks for is handed
// to the spec on waits, and the poller does not move on until the spec ticks.
type fakeClock struct {
	waits chan time.Duration
	ticks chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{waits: make(chan time.Duration, 64), ticks: make(chan time.Time)}
}

func (clock *fakeClock) clock() intake.Clock {
	return intake.Clock{
		Now: func() time.Time { return started },
		After: func(duration time.Duration) <-chan time.Time {
			clock.waits <- duration
			return clock.ticks
		},
	}
}

// awaitRound waits for the poller to finish a round and returns the wait it
// asked for.
func (clock *fakeClock) awaitRound() time.Duration {
	var wait time.Duration
	Eventually(clock.waits).WithTimeout(deliveryBudget.Within).Should(Receive(&wait))
	return wait
}

func (clock *fakeClock) tick(ctx context.Context) {
	select {
	case clock.ticks <- started:
	case <-ctx.Done():
		Fail("the poller never took the tick")
	}
}

// harness is one started relay, one registered in-process agent and one
// started intake on a scope the spec owns.
type harness struct {
	intake    *intake.EventIntake
	messenger *relay.Messenger[*intakev1.Event]
	github    *fakeGitHub
	clock     *fakeClock
}

func startHarness(scripts map[string][]reply, options ...intake.Option) harness {
	core, err := relay.NewRelay[*intakev1.Event]()
	Expect(err).NotTo(HaveOccurred())
	scope := runtime.NewScope(context.Background(), "intake spec")
	DeferCleanup(func() { Expect(scope.Close()).To(Succeed()) })
	Expect(core.Start(scope)).To(Succeed())
	address, err := copilot.NewInProcessAddress(uuid.MustParse(reviewerUUID))
	Expect(err).NotTo(HaveOccurred())
	Expect(core.Register(scope.Context(), relay.Registration{Agent: reviewer, Address: address})).To(Succeed())
	messenger, err := relay.NewMessenger[*intakev1.Event](core)
	Expect(err).NotTo(HaveOccurred())
	routes, err := intake.NewStaticRoutes(intake.Route{Repository: repository, Number: pullRequest, Agent: reviewer})
	Expect(err).NotTo(HaveOccurred())

	github := newFakeGitHub(scripts)
	controller := gomock.NewController(GinkgoT())
	client := NewMockIHTTPClient(controller)
	client.EXPECT().Do(gomock.Any()).DoAndReturn(github.Do).AnyTimes()
	clock := newFakeClock()
	service, err := intake.NewEventIntake(append([]intake.Option{
		intake.WithHTTPClient(client),
		intake.WithRelay(core, messenger),
		intake.WithRoutes(routes),
		intake.WithRepositories(routes.Repositories()...),
		intake.WithAPIBaseURL(apiBase),
		intake.WithPollInterval(pollInterval),
		intake.WithMaxBackoff(maxBackoff),
		intake.WithClock(clock.clock()),
	}, options...)...)
	Expect(err).NotTo(HaveOccurred())
	Expect(service.Start(scope)).To(Succeed())
	return harness{intake: service, messenger: messenger, github: github, clock: clock}
}

func ok(body string, header map[string]string) reply {
	return reply{status: stdhttp.StatusOK, header: header, body: body}
}

var _ = Describe("EventIntake", func() {
	BeforeEach(func() {
		baseline := goleak.IgnoreCurrent()
		// Registered first, so it runs after every cleanup a spec defers —
		// including the scope that owns the relay, poller and dispatcher.
		DeferCleanup(func() {
			Expect(goleak.Find(baseline)).To(Succeed(), "the poller and dispatcher must join when their scope ends")
		})
	})

	It("polls, normalizes, deduplicates and wakes the owning in-process agent", func(ctx SpecContext) {
		h := startHarness(map[string][]reply{
			eventsPath: {
				ok(eventsPage, map[string]string{headerETag: eventsETag}),
				{status: stdhttp.StatusNotModified},
			},
			// The runs feed carries no ETag, so the second round sees the
			// same failed run again: deduplication, not the cache, drops it.
			runsPath: {ok(runsPage, nil)},
		}, intake.WithToken(token), intake.WithIgnoredActors(ignoredBot))

		first := h.github.takeRequest()
		Expect(first.URL.RequestURI()).To(Equal(eventsPath))
		Expect(first.Header.Get(headerAuthorization)).To(Equal("Bearer " + token))
		Expect(first.Header.Get(headerIfNoneMatch)).To(BeEmpty())

		var kinds []intakev1.EventKind
		for range 3 {
			envelope, err := h.messenger.Receive(ctx, reviewer)
			Expect(err).NotTo(HaveOccurred())
			Expect(envelope.From).To(Equal(h.intake.Source()))
			Expect(envelope.Tier).To(Equal(ipc.TierInProcess))
			Expect(envelope.Body.GetSubject().GetNumber()).To(BeEquivalentTo(pullRequest))
			kinds = append(kinds, envelope.Body.GetKind())
		}
		Expect(kinds).To(Equal([]intakev1.EventKind{
			intakev1.EventKind_EVENT_KIND_COMMENT,
			intakev1.EventKind_EVENT_KIND_REVIEW,
			intakev1.EventKind_EVENT_KIND_CHECK_FAILURE,
		}))
		Expect(h.clock.awaitRound()).To(Equal(pollInterval))

		h.clock.tick(ctx)
		Expect(h.clock.awaitRound()).To(Equal(pollInterval))
		Expect(h.github.takeRequest().URL.RequestURI()).To(Equal(runsPath))
		second := h.github.takeRequest()
		Expect(second.URL.RequestURI()).To(Equal(eventsPath))
		Expect(second.Header.Get(headerIfNoneMatch)).To(Equal(eventsETag))

		status := h.intake.Status()
		Expect(status.Polls).To(BeEquivalentTo(4))
		Expect(status.NotModified).To(BeEquivalentTo(1))
		Expect(status.Accepted).To(BeEquivalentTo(3))
		Expect(status.Duplicates).To(BeEquivalentTo(1), "the failed run reported twice is queued once")
		Expect(status.Skipped).To(BeEquivalentTo(2), "the pre-start comment and the bot's own comment")
		Expect(status.Delivered).To(BeEquivalentTo(3))
		pending, err := h.messenger.Fetch(ctx, reviewer, 16)
		Expect(err).NotTo(HaveOccurred())
		Expect(pending).To(BeEmpty(), "nothing beyond the three actionable events reached the inbox")
	})

	It("lengthens the round to the X-Poll-Interval GitHub asks for", func() {
		h := startHarness(map[string][]reply{
			eventsPath: {{status: stdhttp.StatusNotModified, header: map[string]string{headerPollInterval: "120"}}},
			runsPath:   {{status: stdhttp.StatusNotModified}},
		})
		Expect(h.clock.awaitRound()).To(Equal(askedPollInterval))
	})

	DescribeTable("backs off when GitHub's rate limit is spent",
		func(limited reply, wait time.Duration) {
			h := startHarness(map[string][]reply{eventsPath: {limited}, runsPath: {ok(runsPage, nil)}})
			Expect(h.clock.awaitRound()).To(Equal(wait))
			Expect(h.github.takeRequest().URL.RequestURI()).To(Equal(eventsPath))
			Consistently(h.github.requests).WithTimeout(time.Second).ShouldNot(Receive(),
				"the round stops at the limited response; the runs feed waits for the backoff")
			status := h.intake.Status()
			Expect(status.RateLimited).To(BeEquivalentTo(1))
			Expect(status.BackoffUntil).To(BeTemporally(">", started))
			Expect(status.Accepted).To(BeZero())
		},
		Entry("until the primary window resets", reply{status: stdhttp.StatusForbidden, header: map[string]string{
			headerRemaining: "0", headerReset: strconv.FormatInt(started.Add(rateLimitReset).Unix(), 10)}},
			rateLimitReset),
		Entry("for the Retry-After of a secondary limit", reply{status: stdhttp.StatusTooManyRequests, header: map[string]string{
			headerRetryAfter: "45"}}, secondaryRetry),
		Entry("never longer than the maximum backoff", reply{status: stdhttp.StatusForbidden, header: map[string]string{
			headerRemaining: "0", headerReset: strconv.FormatInt(started.Add(rateLimitResetBeyondCeiling).Unix(), 10)}},
			maxBackoff),
	)

	It("counts an event no route owns instead of delivering it", func() {
		unrouted := strings.ReplaceAll(runsPage, `"pull_requests":[{"number":7}]`, `"pull_requests":[{"number":8}]`)
		h := startHarness(map[string][]reply{
			eventsPath: {{status: stdhttp.StatusNotModified}},
			runsPath:   {ok(unrouted, nil)},
		})
		eventually.Await(GinkgoTB(), "the unrouted run to be counted", deliveryBudget, h.intake.Status,
			func(status intake.Status) bool { return status.Unrouted == 1 })
		Expect(h.intake.Status().Delivered).To(BeZero())
	})
})

var _ = Describe("EventIntake misuse", func() {
	var (
		controller *gomock.Controller
		directory  *MockIAgentDirectory
		messenger  *MockIMessenger[*intakev1.Event]
		client     *MockIHTTPClient
		routes     *intake.StaticRoutes
	)

	BeforeEach(func() {
		baseline := goleak.IgnoreCurrent()
		DeferCleanup(func() { Expect(goleak.Find(baseline)).To(Succeed()) })
		controller = gomock.NewController(GinkgoT())
		directory = NewMockIAgentDirectory(controller)
		messenger = NewMockIMessenger[*intakev1.Event](controller)
		client = NewMockIHTTPClient(controller)
		var err error
		routes, err = intake.NewStaticRoutes(intake.Route{Repository: repository, Agent: reviewer})
		Expect(err).NotTo(HaveOccurred())
	})

	required := func() []intake.Option {
		return []intake.Option{
			intake.WithHTTPClient(client),
			intake.WithRelay(directory, messenger),
			intake.WithRoutes(routes),
			intake.WithRepositories(repository),
		}
	}

	DescribeTable("names each missing grant",
		func(drop int, missing string) {
			options := required()
			options = append(options[:drop], options[drop+1:]...)
			_, err := intake.NewEventIntake(options...)
			Expect(err).To(MatchError(intake.ErrMissingOption))
			Expect(err).To(MatchError(ContainSubstring(missing)))
		},
		Entry("the HTTP capability", 0, "WithHTTPClient"),
		Entry("the relay", 1, "WithRelay"),
		Entry("the routes", 2, "WithRoutes"),
		Entry("the repositories", 3, "WithRepositories"),
	)

	It("rejects nil and malformed options before building anything", func() {
		_, err := intake.NewEventIntake(append(required(), nil)...)
		Expect(err).To(MatchError(ContainSubstring("option 4 is nil")))
		_, err = intake.NewEventIntake(append(required(), intake.WithHTTPClient(nil))...)
		Expect(err).To(MatchError(ContainSubstring("WithHTTPClient needs a client")))
		_, err = intake.NewEventIntake(append(required(), intake.WithRelay(nil, messenger))...)
		Expect(err).To(MatchError(ContainSubstring("WithRelay needs a directory and a messenger")))
		_, err = intake.NewEventIntake(append(required(), intake.WithRepositories("not a repository"))...)
		Expect(err).To(MatchError(ContainSubstring("must be owner/name")))
		_, err = intake.NewEventIntake(append(required(), intake.WithPollInterval(0))...)
		Expect(err).To(MatchError(ContainSubstring("poll interval must be positive")))
		_, err = intake.NewEventIntake(append(required(), intake.WithAPIBaseURL("relative/path"))...)
		Expect(err).To(MatchError(ContainSubstring("must be absolute")))
		_, err = intake.NewEventIntake(append(required(), intake.WithClock(intake.Clock{}))...)
		Expect(err).To(MatchError(ContainSubstring("needs Now and After")))
		_, err = intake.NewEventIntake(append(required(), intake.WithSourceAgent("Not Valid"))...)
		Expect(err).To(MatchError(relay.ErrInvalidRegistration))
	})

	It("fails to start when the relay refuses its registration, and starts nothing", func(ctx SpecContext) {
		directory.EXPECT().Register(gomock.Any(), gomock.Any()).Return(relay.ErrNotStarted).Times(1)
		service, err := intake.NewEventIntake(required()...)
		Expect(err).NotTo(HaveOccurred())
		scope := runtime.NewScope(ctx, "misuse spec")
		Expect(service.Start(scope)).To(MatchError(relay.ErrNotStarted))
		Expect(scope.Goroutines()).To(BeZero())
		Expect(scope.Close()).To(Succeed())
	})

	It("fails to start on a scope that has already ended", func(ctx SpecContext) {
		directory.EXPECT().Register(gomock.Any(), gomock.Any()).Return(nil).Times(1)
		service, err := intake.NewEventIntake(required()...)
		Expect(err).NotTo(HaveOccurred())
		scope := runtime.NewScope(ctx, "misuse spec")
		Expect(scope.Close()).To(Succeed())
		Expect(service.Start(scope)).To(MatchError(runtime.ErrScopeClosed))
	})

	It("refuses a second start and a missing scope, and survives a failing API", func(ctx SpecContext) {
		service, err := intake.NewEventIntake(required()...)
		Expect(err).NotTo(HaveOccurred())
		Expect(service.Start(nil)).To(MatchError(ContainSubstring("needs a scope")))

		directory.EXPECT().Register(gomock.Any(), relay.Registration{Agent: intake.DefaultSourceAgent, Address: service.Source()}).Return(nil).Times(1)
		client.EXPECT().Do(gomock.Any()).Return(nil, errors.New("offline")).MinTimes(1)
		scope := runtime.NewScope(ctx, "misuse spec")
		Expect(service.Start(scope)).To(Succeed())
		Expect(service.Start(scope)).To(MatchError(intake.ErrAlreadyStarted))
		eventually.Await(GinkgoTB(), "a failed poll to be counted rather than stop the poller", deliveryBudget, service.Status,
			func(status intake.Status) bool { return status.PollFailures > 0 })
		Expect(scope.Close()).To(Succeed())
	})
})
