// Copyright 2026 Candace Labs

package await_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/proc"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/services/await"
)

// steppingClock is the controllable clock: every wait is delivered at once
// and moves the clock by exactly the duration asked, after tick has changed
// the world the next poll reads. No spec waits on real time.
type steppingClock struct {
	now  time.Time
	tick func()
}

func (clock *steppingClock) Now() time.Time { return clock.now }

func (clock *steppingClock) After(d time.Duration) <-chan time.Time {
	clock.now = clock.now.Add(d)
	if clock.tick != nil {
		clock.tick()
	}
	delivered := make(chan time.Time, 1)
	delivered <- clock.now
	return delivered
}

const (
	hostPID      = 4242
	assignment   = "6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e71"
	pullRequest  = "https://github.com/candacelabs/repository/pull/7"
	awaitedURL   = "http://127.0.0.1:14120/"
	runningStat  = "4242 (csf serve) S 1 4242 4242 0 -1"
	zombieStat   = "4242 (csf serve) Z 1 4242 4242 0 -1"
	tenSeconds   = "10s"
	pollsInTenS  = 11
	elapsedTenMS = 10000
)

func hostRecord(pid int) *fstest.MapFile {
	content, err := json.Marshal(await.HostRecord{PID: pid, Endpoint: "http://127.0.0.1:14120"})
	Expect(err).NotTo(HaveOccurred())
	return &fstest.MapFile{Data: content}
}

func session(phase harnessv1.AgentSessionPhase, turns uint32, queued uint32) *harnessv1.AgentSessionState {
	return &harnessv1.AgentSessionState{AssignmentId: assignment, Phase: phase, Turns: turns, Queued: queued}
}

func answer(status int) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(""))}
}

var _ = Describe("Awaiter", func() {
	var (
		clock     *steppingClock
		state     fstest.MapFS
		processes fstest.MapFS
		sessions  []*harnessv1.AgentSessionState
		launcher  *MockILauncher
		client    *MockIHTTPClient
		awaiter   *await.Awaiter
	)

	BeforeEach(func() {
		clock = &steppingClock{now: time.Date(2026, 10, 5, 2, 10, 0, 0, time.UTC)}
		state = fstest.MapFS{}
		processes = fstest.MapFS{}
		sessions = nil
		controller := gomock.NewController(GinkgoT())
		launcher = NewMockILauncher(controller)
		client = NewMockIHTTPClient(controller)
		var err error
		awaiter, err = await.NewAwaiter(
			await.WithClock(clock),
			await.WithHostState(state),
			await.WithProcessTable(processes),
			await.WithLauncher(launcher),
			await.WithHTTPClient(client),
			await.WithSessionState(func(_ context.Context, requested string) (*harnessv1.AgentSessionState, error) {
				Expect(requested).To(Equal(assignment))
				if len(sessions) == 0 {
					return nil, errors.New("no such session")
				}
				next := sessions[0]
				if len(sessions) > 1 {
					sessions = sessions[1:]
				}
				return next, nil
			}),
		)
		Expect(err).NotTo(HaveOccurred())
	})

	// after runs change once the clock has moved polls-1 times: the change
	// is seen by poll number polls.
	after := func(polls int, change func()) {
		waits := 0
		clock.tick = func() {
			waits++
			if waits == polls-1 {
				change()
			}
		}
	}

	awaitResult := func(request await.Request) await.Result {
		result, err := awaiter.Await(context.Background(), request)
		Expect(err).NotTo(HaveOccurred())
		return result
	}

	Describe("misuse", func() {
		DescribeTable("is refused before anything is polled",
			func(request await.Request, refusal error, says string) {
				_, err := awaiter.Await(context.Background(), request)
				Expect(err).To(MatchError(refusal))
				Expect(err).To(MatchError(ContainSubstring(says)))
			},
			Entry("an unknown condition", await.Request{Condition: "gpu-idle", Deadline: tenSeconds},
				await.ErrUnknownCondition, "harness-ready, harness-stopped, load-below"),
			Entry("a missing deadline", await.Request{Condition: await.ConditionHarnessReady},
				await.ErrNoDeadline, "such as 90s"),
			Entry("a deadline that is not a duration", await.Request{Condition: await.ConditionHarnessReady, Deadline: "soon"},
				await.ErrNoDeadline, `"soon"`),
			Entry("a deadline that is not positive", await.Request{Condition: await.ConditionHarnessReady, Deadline: "-5s"},
				await.ErrNoDeadline, `"-5s"`),
			Entry("session-phase without an assignment", await.Request{Condition: await.ConditionSessionPhase, Deadline: tenSeconds, Phase: "OPEN"},
				await.ErrMissingOperand, "needs assignment"),
			Entry("session-phase with an unknown phase", await.Request{Condition: await.ConditionSessionPhase, Deadline: tenSeconds, Assignment: assignment, Phase: "DONE"},
				await.ErrMissingOperand, "CANCELED, CANCELING, CLOSED, FAILED, OPEN, RUNNING, STARTING"),
			Entry("turn-finished without an assignment", await.Request{Condition: await.ConditionTurnFinished, Deadline: tenSeconds},
				await.ErrMissingOperand, "needs assignment"),
			Entry("pull-request-merged without a pull request", await.Request{Condition: await.ConditionPullRequestMerged, Deadline: tenSeconds},
				await.ErrMissingOperand, "needs pull_request"),
			Entry("url-status without a URL", await.Request{Condition: await.ConditionURLStatus, Deadline: tenSeconds},
				await.ErrMissingOperand, "needs url"),
			Entry("load-below without a level", await.Request{Condition: await.ConditionLoadBelow, Deadline: tenSeconds},
				await.ErrMissingOperand, "needs a positive load"),
		)

		It("refuses a condition whose capability the binary did not grant", func() {
			bare, err := await.NewAwaiter(await.WithClock(clock))
			Expect(err).NotTo(HaveOccurred())
			for _, request := range []await.Request{
				{Condition: await.ConditionHarnessReady, Deadline: tenSeconds},
				{Condition: await.ConditionHarnessStopped, Deadline: tenSeconds},
				{Condition: await.ConditionSessionPhase, Deadline: tenSeconds, Assignment: assignment, Phase: "OPEN"},
				{Condition: await.ConditionTurnFinished, Deadline: tenSeconds, Assignment: assignment},
				{Condition: await.ConditionPullRequestMerged, Deadline: tenSeconds, PullRequest: pullRequest},
				{Condition: await.ConditionURLStatus, Deadline: tenSeconds, URL: awaitedURL},
				{Condition: await.ConditionLoadBelow, Deadline: tenSeconds, Load: 48},
			} {
				_, err := bare.Await(context.Background(), request)
				Expect(err).To(MatchError(await.ErrUnavailable), string(request.Condition))
			}
		})

		It("refuses to build without a clock, or with a nil capability", func() {
			_, err := await.NewAwaiter()
			Expect(err).To(MatchError(await.ErrInvalidOption))
			_, err = await.NewAwaiter(await.WithClock(clock), await.WithLauncher(nil))
			Expect(err).To(MatchError(await.ErrInvalidOption))
		})

		It("lists the closed set of conditions", func() {
			Expect(await.Conditions()).To(Equal([]await.Condition{
				await.ConditionHarnessReady, await.ConditionHarnessStopped, await.ConditionLoadBelow,
				await.ConditionPullRequestMerged, await.ConditionSessionPhase, await.ConditionTurnFinished,
				await.ConditionURLStatus,
			}))
		})
	})

	Describe("harness-ready", func() {
		It("is met once the host record names the process, which runs", func() {
			processes["4242/stat"] = &fstest.MapFile{Data: []byte(runningStat)}
			after(3, func() { state[await.HostRecordFile] = hostRecord(hostPID) })

			Expect(awaitResult(await.Request{Condition: await.ConditionHarnessReady, Deadline: tenSeconds, PID: hostPID})).To(Equal(await.Result{
				Condition: await.ConditionHarnessReady, Outcome: await.OutcomeMet, ElapsedMS: 2000, Polls: 3,
				Observation: "pid 4242 ready on http://127.0.0.1:14120",
			}))
		})

		It("reaches the deadline while no record is written", func() {
			Expect(awaitResult(await.Request{Condition: await.ConditionHarnessReady, Deadline: tenSeconds})).To(Equal(await.Result{
				Condition: await.ConditionHarnessReady, Outcome: await.OutcomeDeadline, ElapsedMS: elapsedTenMS, Polls: pollsInTenS,
				Observation: "no host record yet",
			}))
		})

		It("does not take a stale record of an exited host for a ready one", func() {
			state[await.HostRecordFile] = hostRecord(hostPID)

			result := awaitResult(await.Request{Condition: await.ConditionHarnessReady, Deadline: tenSeconds})
			Expect(result.Outcome).To(Equal(await.OutcomeDeadline))
			Expect(result.Observation).To(Equal("the host record names pid 4242, which is not running"))
		})

		It("is unreachable once the awaited process exits before it is ready", func() {
			processes["4242/stat"] = &fstest.MapFile{Data: []byte(runningStat)}
			after(2, func() { processes["4242/stat"] = &fstest.MapFile{Data: []byte(zombieStat)} })

			Expect(awaitResult(await.Request{Condition: await.ConditionHarnessReady, Deadline: tenSeconds, PID: hostPID})).To(Equal(await.Result{
				Condition: await.ConditionHarnessReady, Outcome: await.OutcomeUnreachable, ElapsedMS: 1000, Polls: 2,
				Observation: "pid 4242 exited before it was ready",
			}))
		})
	})

	Describe("harness-stopped", func() {
		It("is met once the recorded host process has exited", func() {
			state[await.HostRecordFile] = hostRecord(hostPID)
			processes["4242/stat"] = &fstest.MapFile{Data: []byte(runningStat)}
			after(4, func() { delete(processes, "4242/stat") })

			Expect(awaitResult(await.Request{Condition: await.ConditionHarnessStopped, Deadline: tenSeconds})).To(Equal(await.Result{
				Condition: await.ConditionHarnessStopped, Outcome: await.OutcomeMet, ElapsedMS: 3000, Polls: 4,
				Observation: "pid 4242 exited",
			}))
		})

		It("counts an unreaped zombie as exited", func() {
			processes["4242/stat"] = &fstest.MapFile{Data: []byte(zombieStat)}

			Expect(awaitResult(await.Request{Condition: await.ConditionHarnessStopped, Deadline: tenSeconds, PID: hostPID}).Met()).To(BeTrue())
		})

		It("is met at once when no host is recorded", func() {
			result := awaitResult(await.Request{Condition: await.ConditionHarnessStopped, Deadline: tenSeconds})
			Expect(result).To(Equal(await.Result{Condition: await.ConditionHarnessStopped, Outcome: await.OutcomeMet, Polls: 1,
				Observation: "no host is recorded as running"}))
		})

		It("reaches the deadline while the process runs", func() {
			processes["4242/stat"] = &fstest.MapFile{Data: []byte(runningStat)}

			Expect(awaitResult(await.Request{Condition: await.ConditionHarnessStopped, Deadline: tenSeconds, PID: hostPID})).To(Equal(await.Result{
				Condition: await.ConditionHarnessStopped, Outcome: await.OutcomeDeadline, ElapsedMS: elapsedTenMS, Polls: pollsInTenS,
				Observation: "pid 4242 still running, state S",
			}))
		})
	})

	Describe("session-phase", func() {
		request := await.Request{Condition: await.ConditionSessionPhase, Deadline: tenSeconds, Assignment: assignment, Phase: "open"}

		It("is met once the session is in the phase", func() {
			sessions = []*harnessv1.AgentSessionState{
				session(harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_STARTING, 0, 1),
				session(harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN, 1, 0),
			}
			Expect(awaitResult(request)).To(Equal(await.Result{
				Condition: await.ConditionSessionPhase, Outcome: await.OutcomeMet, ElapsedMS: 1000, Polls: 2,
				Observation: "session " + assignment + " is OPEN after 1 turns, 0 queued",
			}))
		})

		It("is unreachable once the session ended in another phase", func() {
			failed := session(harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_FAILED, 1, 0)
			failed.Error = "executor exited"
			sessions = []*harnessv1.AgentSessionState{failed}

			result := awaitResult(request)
			Expect(result.Outcome).To(Equal(await.OutcomeUnreachable))
			Expect(result.Observation).To(HaveSuffix("is FAILED after 1 turns, 0 queued: executor exited"))
		})

		It("reaches the deadline while the session cannot be read, saying why", func() {
			result := awaitResult(request)
			Expect(result.Outcome).To(Equal(await.OutcomeDeadline))
			Expect(result.Observation).To(Equal("session " + assignment + " unreadable: no such session"))
		})
	})

	Describe("turn-finished", func() {
		It("is met once the named turn has run and nothing is queued", func() {
			sessions = []*harnessv1.AgentSessionState{
				session(harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN, 2, 1),
				session(harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING, 3, 0),
				session(harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN, 3, 0),
			}
			result := awaitResult(await.Request{Condition: await.ConditionTurnFinished, Deadline: tenSeconds, Assignment: assignment, Turn: 3})
			Expect(result.Outcome).To(Equal(await.OutcomeMet))
			Expect(result.Polls).To(Equal(3))
		})

		It("reaches the deadline while the turn runs", func() {
			sessions = []*harnessv1.AgentSessionState{session(harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING, 3, 0)}

			result := awaitResult(await.Request{Condition: await.ConditionTurnFinished, Deadline: tenSeconds, Assignment: assignment})
			Expect(result.Outcome).To(Equal(await.OutcomeDeadline))
			Expect(result.Observation).To(HaveSuffix("is RUNNING after 3 turns, 0 queued"))
		})

		It("is unreachable once the session closed", func() {
			sessions = []*harnessv1.AgentSessionState{session(harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CLOSED, 3, 0)}

			Expect(awaitResult(await.Request{Condition: await.ConditionTurnFinished, Deadline: tenSeconds, Assignment: assignment}).Outcome).
				To(Equal(await.OutcomeUnreachable))
		})
	})

	Describe("pull-request-merged", func() {
		view := func(state string) proc.Result { return proc.Result{Stdout: []byte(`{"state":"` + state + `"}`)} }
		viewCommand := proc.Command{Executable: "gh", Arguments: []string{"pr", "view", "7", "--json", "state", "--repo", "candacelabs/repository"}}

		It("is met once gh reports the pull request merged", func() {
			gomock.InOrder(
				launcher.EXPECT().Run(gomock.Any(), viewCommand).Return(view("OPEN"), nil),
				launcher.EXPECT().Run(gomock.Any(), viewCommand).Return(view("MERGED"), nil),
			)
			Expect(awaitResult(await.Request{Condition: await.ConditionPullRequestMerged, Deadline: tenSeconds, PullRequest: "7", Repository: "candacelabs/repository"})).
				To(Equal(await.Result{Condition: await.ConditionPullRequestMerged, Outcome: await.OutcomeMet, ElapsedMS: 1000, Polls: 2,
					Observation: "pull request 7 is MERGED"}))
		})

		It("is unreachable once the pull request is closed unmerged", func() {
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).Return(view("CLOSED"), nil)

			Expect(awaitResult(await.Request{Condition: await.ConditionPullRequestMerged, Deadline: tenSeconds, PullRequest: pullRequest}).Outcome).
				To(Equal(await.OutcomeUnreachable))
		})

		It("reaches the deadline while gh fails, saying how", func() {
			launcher.EXPECT().Run(gomock.Any(), proc.Command{Executable: "gh", Arguments: []string{"pr", "view", pullRequest, "--json", "state"}}).
				Return(proc.Result{}, errors.New("gh: not logged in")).Times(pollsInTenS)

			result := awaitResult(await.Request{Condition: await.ConditionPullRequestMerged, Deadline: tenSeconds, PullRequest: pullRequest})
			Expect(result.Outcome).To(Equal(await.OutcomeDeadline))
			Expect(result.Observation).To(Equal("pull request " + pullRequest + " unreadable: gh: not logged in"))
		})
	})

	Describe("url-status", func() {
		It("is met once the URL answers the awaited status, 200 by default", func() {
			gomock.InOrder(
				client.EXPECT().Do(gomock.Any()).Return(nil, errors.New("connection refused")),
				client.EXPECT().Do(gomock.Any()).Return(answer(http.StatusServiceUnavailable), nil),
				client.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *http.Request) (*http.Response, error) {
					Expect(request.Method).To(Equal(http.MethodGet))
					Expect(request.URL.String()).To(Equal(awaitedURL))
					return answer(http.StatusOK), nil
				}),
			)
			Expect(awaitResult(await.Request{Condition: await.ConditionURLStatus, Deadline: tenSeconds, URL: awaitedURL})).To(Equal(await.Result{
				Condition: await.ConditionURLStatus, Outcome: await.OutcomeMet, ElapsedMS: 2000, Polls: 3,
				Observation: awaitedURL + " answered 200, awaiting 200",
			}))
		})

		It("reaches the deadline while the URL answers another status", func() {
			client.EXPECT().Do(gomock.Any()).Return(answer(http.StatusOK), nil).Times(pollsInTenS)

			result := awaitResult(await.Request{Condition: await.ConditionURLStatus, Deadline: tenSeconds, URL: awaitedURL, Status: http.StatusNotFound})
			Expect(result.Outcome).To(Equal(await.OutcomeDeadline))
			Expect(result.Observation).To(Equal(awaitedURL + " answered 200, awaiting 404"))
		})
	})

	Describe("load-below", func() {
		It("is met once the one-minute load average falls below the level", func() {
			processes["loadavg"] = &fstest.MapFile{Data: []byte("68.65 61.05 59.68 5/3839 170936\n")}
			after(2, func() { processes["loadavg"] = &fstest.MapFile{Data: []byte("47.10 58.00 59.00 5/3839 170936\n")} })

			Expect(awaitResult(await.Request{Condition: await.ConditionLoadBelow, Deadline: tenSeconds, Load: 48})).To(Equal(await.Result{
				Condition: await.ConditionLoadBelow, Outcome: await.OutcomeMet, ElapsedMS: 1000, Polls: 2,
				Observation: "load 47.10 (5 min 58.00, 15 min 59.00), awaiting below 48",
			}))
		})

		It("reaches the deadline while the load stays at or above the level", func() {
			processes["loadavg"] = &fstest.MapFile{Data: []byte("48.00 61.05 59.68 5/3839 170936\n")}

			Expect(awaitResult(await.Request{Condition: await.ConditionLoadBelow, Deadline: tenSeconds, Load: 48}).Outcome).To(Equal(await.OutcomeDeadline))
		})
	})

	Describe("the operation over HTTP", func() {
		serve := func(body string) *httptest.ResponseRecorder {
			gin.SetMode(gin.TestMode)
			router := gin.New()
			awaiter.Register(router)
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, await.AwaitPath, strings.NewReader(body)))
			return recorder
		}

		It("answers the typed result", func() {
			processes["loadavg"] = &fstest.MapFile{Data: []byte("1.00 2.00 3.00 1/1 1\n")}

			recorder := serve(`{"condition":"load-below","deadline":"10s","load":48}`)
			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Body.String()).To(MatchJSON(`{"condition":"load-below","outcome":"met","elapsed_ms":0,"polls":1,
				"observation":"load 1.00 (5 min 2.00, 15 min 3.00), awaiting below 48"}`))
		})

		It("answers a misuse as a bad request naming it", func() {
			recorder := serve(`{"condition":"load-below","load":48}`)
			Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			Expect(recorder.Body.String()).To(ContainSubstring("a positive deadline is required"))
		})
	})
})
