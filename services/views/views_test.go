// Copyright 2026 Candace Labs

package views_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/kernel/clock"
	"github.com/candacelabs/csf/io/ipc/docker"
	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/views"
	"github.com/candacelabs/csf/services/views/mocks"
)

// scrapeBudget bounds the wait for the fold's first measurement to publish.
var scrapeBudget = eventually.Budget{Within: 10 * time.Second, Interval: 20 * time.Millisecond}

func get(router http.Handler, path string, host string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Host = host
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func liveSources() []views.Option {
	return []views.Option{
		views.WithSessions(func(ctx context.Context) ([]views.SessionState, error) {
			return []views.SessionState{{Assignment: "run-1", Agent: "csf-metrics", Phase: "running"}}, nil
		}),
		views.WithAdmission(func(ctx context.Context) (views.Admission, error) {
			return views.Admission{WorkerCap: 4, FreeBytes: 9, DiskFloorBytes: 3, Pressure: map[string]float64{"cpu": 36, "memory": 0.08}, ResumesHeld: 3}, nil
		}),
		views.WithDispatch(func(ctx context.Context) (views.Dispatch, error) {
			lead := 5400.0
			return views.Dispatch{Queued: 2, Held: 1, Running: 1, Remaining: map[string]int64{"budget": 5}, Slices: map[string]string{"run-1": "CSF-METRICS"},
				Stages: map[string]int{"ready": 1, "blocked": 2}, StageAges: map[string]float64{"ready": 60, "blocked": 7200}, LeadTime: &lead}, nil
		}),
		views.WithPulls(func(ctx context.Context) ([]views.MergedPull, error) {
			return []views.MergedPull{views.ParsePull(319, "LABELER (#318): x", at(1, 37), pullBody)}, nil
		}),
	}
}

var _ = Describe("Views", func() {
	It("needs the corpus and refuses a nil option", func() {
		_, err := views.NewViews()
		Expect(err).To(MatchError(views.ErrMissingCapability))
		_, err = views.NewViews(views.WithCorpus(stateDirectory()), nil)
		Expect(err).To(MatchError(views.ErrInvalidOption))
		_, err = views.NewViews(views.WithCorpus(stateDirectory()), views.WithSessions(nil))
		Expect(err).To(MatchError(views.ErrInvalidOption))
	})

	It("serves every live family at once, the folded ones once the first measurement publishes, labeled with the slice", func(ctx SpecContext) {
		exported, err := views.NewViews(append(liveSources(), views.WithCorpus(stateDirectory()),
			views.WithClock(clock.NewManualClock(at(23, 0))))...)
		Expect(err).NotTo(HaveOccurred())
		router := gin.New()
		exported.Register(router)
		scope := runtime.NewScope(ctx, "views")
		DeferCleanup(scope.Close)

		before := get(router, views.MetricsPath, "127.0.0.1:14120").Body.String()
		Expect(before).To(ContainSubstring(`csf_dispatch_slices{state="queued"} 2`))
		Expect(before).To(ContainSubstring(`csf_dispatch_stage_slices{stage="blocked"} 2`))
		Expect(before).To(ContainSubstring(`csf_dispatch_stage_age_seconds{stage="blocked"} 7200`))
		Expect(before).To(ContainSubstring(`csf_dispatch_lead_time_seconds 5400`))
		Expect(before).To(ContainSubstring(`csf_operator_corrections_total{class="#145",source="hand_labeled"} 4`))
		Expect(before).To(ContainSubstring(`csf_bazel_build_wall_seconds{arm="A"} 57.05`))
		Expect(before).NotTo(ContainSubstring("csf_session_turns_total"))
		Expect(before).To(ContainSubstring(`csf_host_pressure_some_avg10{resource="cpu"} 36`))
		Expect(before).To(ContainSubstring(`csf_host_pressure_some_avg10{resource="memory"} 0.08`))
		Expect(before).NotTo(ContainSubstring(`resource="io"`), "a resource the kernel does not report is absent, not zero")
		Expect(before).To(ContainSubstring("csf_resumes_held 3"))

		Expect(exported.Start(scope)).To(Succeed())
		Expect(exported.Start(scope)).To(MatchError(views.ErrStarted))
		after := eventually.Await(GinkgoT(), "the fold's first measurement on /metrics", scrapeBudget,
			func() string { return get(router, views.MetricsPath, "127.0.0.1:14120").Body.String() },
			func(body string) bool { return strings.Contains(body, "csf_session_turns_total") })
		Expect(after).To(ContainSubstring(`csf_session_turns_total{agent="csf-metrics",executor="claudecode",model="claude-opus-5-5",slice="CSF-METRICS"} 3`))
		Expect(after).To(ContainSubstring(`csf_sessions{agent="csf-metrics",executor="claudecode",model="claude-opus-5-5",phase="running",slice="CSF-METRICS"} 1`))
		Expect(after).To(ContainSubstring(`csf_ontology_alignment_score 0.539`))
		Expect(after).To(ContainSubstring(`csf_operator_attention_hours_total{source="runs"} 1`))
	})

	It("exports the host's pressure and the held resumes while housekeeping holds admission", func() {
		exported, err := views.NewViews(views.WithCorpus(stateDirectory()), views.WithClock(clock.NewManualClock(at(23, 0))),
			views.WithAdmission(func(ctx context.Context) (views.Admission, error) {
				return views.Admission{Held: true, Pressure: map[string]float64{"io": 2.5}, ResumesHeld: 30}, nil
			}))
		Expect(err).NotTo(HaveOccurred())
		router := gin.New()
		exported.Register(router)

		body := get(router, views.MetricsPath, "127.0.0.1:14120").Body.String()

		Expect(body).To(ContainSubstring("csf_admission_held 1"))
		Expect(body).To(ContainSubstring(`csf_host_pressure_some_avg10{resource="io"} 2.5`))
		Expect(body).To(ContainSubstring("csf_resumes_held 30"))
		Expect(body).NotTo(ContainSubstring("csf_admission_worker_cap"))
	})

	It("answers a panel link with 503 until Grafana is provisioned, 404 for a family no panel shows, then the panel on the host the browser used", func(ctx SpecContext) {
		controller := gomock.NewController(GinkgoT())
		containers, listener := mocks.NewMockIContainers(controller), NewMockIListener(controller)
		containers.EXPECT().InspectService(gomock.Any(), gomock.Any()).Return(docker.ServiceState{}, docker.ErrNoContainer).AnyTimes()
		listener.EXPECT().Listen(gomock.Any(), gomock.Any(), gomock.Any()).Return(probe{port: 41002}, nil).Times(2)
		containers.EXPECT().EnsureService(gomock.Any(), gomock.Any()).Return(healthy, nil).AnyTimes()
		containers.EXPECT().SignalService(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		containers.EXPECT().Exec(gomock.Any(), gomock.Any(), gomock.Any()).Return(docker.ExecResult{}, nil).AnyTimes()
		stack, err := newStack(GinkgoT().TempDir(), containers, listener)
		Expect(err).NotTo(HaveOccurred())
		exported, err := views.NewViews(views.WithCorpus(stateDirectory()), views.WithStack(stack), views.WithClock(clock.NewManualClock(at(23, 0))))
		Expect(err).NotTo(HaveOccurred())
		router := gin.New()
		exported.Register(router)

		Expect(get(router, views.PanelPath+views.MetricSessions, "192.0.2.7:14120").Code).To(Equal(http.StatusServiceUnavailable))
		Expect(get(router, views.PanelPath+"csf_nothing", "192.0.2.7:14120").Code).To(Equal(http.StatusNotFound))

		scope := runtime.NewScope(ctx, "views")
		DeferCleanup(scope.Close)
		Expect(exported.Start(scope)).To(Succeed())
		redirected := eventually.Await(GinkgoT(), "the panel link to redirect once Grafana is provisioned", scrapeBudget,
			func() *httptest.ResponseRecorder {
				return get(router, views.PanelPath+views.MetricSessions, "192.0.2.7:14120")
			},
			func(response *httptest.ResponseRecorder) bool { return response.Code == http.StatusFound })
		Expect(redirected.Header().Get("Location")).To(Equal("http://192.0.2.7:41002/d/csf?viewPanel=panel-1"))
		Expect(get(router, views.DashboardPath, "127.0.0.1:14120").Header().Get("Location")).To(Equal("http://127.0.0.1:41002/d/csf"))
	})
})
