package httpapi_test

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/browserroutes"
	"github.com/candacelabs/csf/services/candaceos/httpapi"
	"github.com/candacelabs/csf/services/candaceos/httpserver"
	"github.com/candacelabs/csf/services/candaceos/operator"
	"github.com/candacelabs/csf/services/candaceos/webui"
)

func TestHTTPAPI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "candaceos-core HTTP API suite")
}

var _ = Describe("API", func() {
	var (
		backend   *MockIBackend
		handler   http.Handler
		router    *gin.Engine
		transport *httpapi.API
	)

	BeforeEach(func() {
		backend = NewMockIBackend(gomock.NewController(GinkgoT()))
		router = httpserver.NewEngine()
		ui, err := webui.New(backend)
		Expect(err).NotTo(HaveOccurred())
		transport, err = httpapi.New(backend)
		Expect(err).NotTo(HaveOccurred())
		ui.Register(router)
		transport.Register(router)
		handler = router
	})

	It("requires a backend", func() {
		value, err := httpapi.New(nil)
		Expect(value).To(BeNil())
		Expect(err).To(MatchError(httpapi.ErrNilBackend))
	})

	It("registers beside routes already owned by the caller", func() {
		const callerPath = "/caller-owned"
		router.GET(callerPath, func(c *gin.Context) { c.Status(http.StatusNoContent) })

		response := perform(handler, httptest.NewRequest(http.MethodGet, callerPath, nil))
		Expect(response.Code).To(Equal(http.StatusNoContent))
	})

	It("serves the browser and read API without a login or session cookie", func() {
		backend.EXPECT().Snapshot(gomock.Any()).Return(readySnapshot(), nil).Times(2)

		page := perform(handler, httptest.NewRequest(http.MethodGet, browserroutes.Index, nil))
		Expect(page.Code).To(Equal(http.StatusOK))
		Expect(page.Header().Get("Cache-Control")).To(Equal("no-store"))
		Expect(page.Header().Get("Content-Security-Policy")).To(ContainSubstring("default-src 'self'"))
		Expect(page.Header().Get("Content-Security-Policy")).To(ContainSubstring("frame-ancestors 'none'"))
		Expect(page.Body.String()).To(ContainSubstring("CandaceOS"))
		Expect(page.Result().Cookies()).To(BeEmpty())
		asset := perform(handler, httptest.NewRequest(http.MethodGet, browserroutes.AssetPath("app.css"), nil))
		Expect(asset.Code).To(Equal(http.StatusOK), asset.Body.String())
		Expect(asset.Header().Get("Content-Type")).To(ContainSubstring("text/css"))

		snapshot := perform(handler, httptest.NewRequest(http.MethodGet, browserroutes.Snapshot, nil))
		Expect(snapshot.Code).To(Equal(http.StatusOK), snapshot.Body.String())
		Expect(snapshot.Result().Cookies()).To(BeEmpty())
	})

	It("accepts a valid prompt and returns its run identifier", func() {
		backend.EXPECT().Send(gomock.Any(), "build me a private notes app").Return("run-123", nil)

		request := jsonRequest(http.MethodPost, browserroutes.Prompts, `{"prompt":"  build me a private notes app  "}`)
		request.Host = "candaceos.example"
		request.Header.Set("Origin", "https://candaceos.example")
		response := perform(handler, request)

		Expect(response.Code).To(Equal(http.StatusAccepted), response.Body.String())
		Expect(response.Body.String()).To(MatchJSON(`{"run_id":"run-123"}`))
	})

	DescribeTable("steers a rendered Claw run with canonical delivery",
		func(delivery string, expected candaceosv1.HarnessDelivery) {
			backend.EXPECT().SendToClaw(
				gomock.Any(), "session-7", "run-7", "adjust course", expected,
			).Return("run-123", nil)

			request := jsonRequest(http.MethodPost, browserroutes.ClawMessagePath("session-7"), fmt.Sprintf(
				`{"prompt":"  adjust course  ","delivery":%q,"expectedRunId":"run-7"}`,
				delivery,
			))
			response := perform(handler, request)

			Expect(response.Code).To(Equal(http.StatusAccepted), response.Body.String())
			Expect(response.Body.String()).To(MatchJSON(`{"run_id":"run-123"}`))
		},
		Entry("enqueue", "HARNESS_DELIVERY_ENQUEUE", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE),
		Entry("immediate", "HARNESS_DELIVERY_IMMEDIATE", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE),
	)

	DescribeTable("returns a conflict when a Claw target is stale",
		func(targetErr error) {
			backend.EXPECT().SendToClaw(
				gomock.Any(), "session-7", "run-7", "adjust course",
				candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE,
			).Return("", targetErr)

			response := perform(handler, jsonRequest(
				http.MethodPost,
				browserroutes.ClawMessagePath("session-7"),
				`{"prompt":"adjust course","delivery":"HARNESS_DELIVERY_ENQUEUE","expectedRunId":"run-7"}`,
			))
			Expect(response.Code).To(Equal(http.StatusConflict), response.Body.String())
		},
		Entry("session", operator.ErrSessionConflict),
		Entry("run", operator.ErrRunConflict),
		Entry("inactive run", operator.ErrRunNotActive),
	)

	It("validates the generated Claw request before delegation", func() {
		response := perform(handler, jsonRequest(
			http.MethodPost,
			browserroutes.ClawMessagePath("session-7"),
			`{"prompt":"adjust course","delivery":"HARNESS_DELIVERY_UNSPECIFIED","expectedRunId":"run-7"}`,
		))
		Expect(response.Code).To(Equal(http.StatusBadRequest), response.Body.String())
	})

	DescribeTable("rejects invalid prompts without starting a run",
		func(contentType, body, expected string) {
			request := httptest.NewRequest(http.MethodPost, browserroutes.Prompts, strings.NewReader(body))
			request.Header.Set("Content-Type", contentType)
			response := perform(handler, request)
			Expect(response.Code).To(Equal(http.StatusBadRequest))
			Expect(response.Body.String()).To(ContainSubstring(expected))
		},
		Entry("without JSON content type", "text/plain", `{"prompt":"hello"}`, "Content-Type must be application/json"),
		Entry("when blank", "application/json", `{"prompt":"   "}`, "prompt must contain between 1 and 12000 bytes"),
		Entry("when too long", "application/json", fmt.Sprintf(`{"prompt":%q}`, strings.Repeat("x", 12_001)), "prompt must contain between 1 and 12000 bytes"),
		Entry("with an unknown field", "application/json", `{"prompt":"hello","yolo":true}`, "unknown field"),
		Entry("with a trailing value", "application/json", `{"prompt":"hello"} {}`, "exactly one JSON value"),
	)

	It("aborts a current run and resolves an approval", func() {
		backend.EXPECT().Abort(gomock.Any()).Return(nil)
		backend.EXPECT().ResolveApproval("approval-7", "approve").Return(nil)

		abort := perform(handler, jsonRequest(http.MethodPost, browserroutes.CurrentRunAbort, `{}`))
		Expect(abort.Code).To(Equal(http.StatusOK), abort.Body.String())
		Expect(abort.Body.String()).To(MatchJSON(`{"status":"aborted"}`))

		approval := perform(handler, jsonRequest(http.MethodPost, browserroutes.ApprovalPath("approval-7"), `{"decision":"approve"}`))
		Expect(approval.Code).To(Equal(http.StatusOK), approval.Body.String())
		Expect(approval.Body.String()).To(MatchJSON(`{"status":"approved"}`))
	})

	It("aborts only the Claw run named by the route", func() {
		backend.EXPECT().AbortRun(gomock.Any(), "session-7", "run-7").Return(nil)

		response := perform(handler, jsonRequest(http.MethodPost, browserroutes.ClawRunAbortPath("session-7", "run-7"), `{}`))
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		Expect(response.Body.String()).To(MatchJSON(`{"status":"aborted"}`))
	})

	It("reports conflicts from abort and approval state transitions", func() {
		backend.EXPECT().Abort(gomock.Any()).Return(errors.New("no active run"))
		response := perform(handler, jsonRequest(http.MethodPost, browserroutes.CurrentRunAbort, `{}`))
		Expect(response.Code).To(Equal(http.StatusConflict))
		Expect(response.Body.String()).To(MatchJSON(`{"error":"no active run"}`))

		backend.EXPECT().ResolveApproval("approval-7", "reject").Return(errors.New("approval already resolved"))
		response = perform(handler, jsonRequest(http.MethodPost, browserroutes.ApprovalPath("approval-7"), `{"decision":"reject"}`))
		Expect(response.Code).To(Equal(http.StatusConflict))
		Expect(response.Body.String()).To(MatchJSON(`{"error":"approval already resolved"}`))
	})

	It("validates approval decisions and empty abort bodies", func() {
		approval := perform(handler, jsonRequest(http.MethodPost, browserroutes.ApprovalPath("approval-7"), `{"decision":"later"}`))
		Expect(approval.Code).To(Equal(http.StatusBadRequest))
		Expect(approval.Body.String()).To(ContainSubstring("decision must be approve or reject"))

		abort := perform(handler, jsonRequest(http.MethodPost, browserroutes.CurrentRunAbort, `{"force":true}`))
		Expect(abort.Code).To(Equal(http.StatusBadRequest))
		Expect(abort.Body.String()).To(ContainSubstring("unknown field"))
	})

	DescribeTable("rejects cross-origin mutations before invoking the backend",
		func(path, body string) {
			request := jsonRequest(http.MethodPost, path, body)
			request.Host = "candaceos.example"
			request.Header.Set("Origin", "https://evil.example")
			response := perform(handler, request)
			Expect(response.Code).To(Equal(http.StatusForbidden))
			Expect(response.Body.String()).To(MatchJSON(`{"error":"cross-origin action rejected"}`))
		},
		Entry("prompt", browserroutes.Prompts, `{"prompt":"hello"}`),
		Entry("claw message", browserroutes.ClawMessagePath("session-7"), `{"prompt":"hello","delivery":"HARNESS_DELIVERY_ENQUEUE","expectedRunId":"run-7"}`),
		Entry("abort", browserroutes.CurrentRunAbort, `{}`),
		Entry("claw abort", browserroutes.ClawRunAbortPath("session-7", "run-7"), `{}`),
		Entry("approval", browserroutes.ApprovalPath("approval-7"), `{"decision":"approve"}`),
	)

	It("exposes health and fails closed when dependencies are unavailable", func() {
		gomock.InOrder(
			backend.EXPECT().Health(gomock.Any()).Return(errors.New("database unavailable")),
			backend.EXPECT().Health(gomock.Any()).Return(nil),
		)

		response := perform(handler, httptest.NewRequest(http.MethodGet, browserroutes.Health, nil))
		Expect(response.Code).To(Equal(http.StatusServiceUnavailable))
		Expect(response.Body.String()).To(MatchJSON(`{"status":"unavailable"}`))

		response = perform(handler, httptest.NewRequest(http.MethodGet, browserroutes.Health, nil))
		Expect(response.Code).To(Equal(http.StatusOK))
		Expect(response.Body.String()).To(MatchJSON(`{"status":"ok"}`))
	})

	It("mounts the web UI snapshot API without authentication", func() {
		generatedAt := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
		backend.EXPECT().Snapshot(gomock.Any()).Return(&candaceosv1.WebUISnapshot{
			GeneratedAt: timestamppb.New(generatedAt),
			System:      &candaceosv1.WebUISystem{Status: "ready", Version: "dev"},
			Fleet:       &candaceosv1.WebUIFleet{LeaderId: "node-a", Term: 9},
		}, nil)

		response := perform(handler, httptest.NewRequest(http.MethodGet, browserroutes.Snapshot, nil))
		Expect(response.Code).To(Equal(http.StatusOK), response.Body.String())
		Expect(response.Header().Get("Cache-Control")).To(Equal("no-store"))
		var snapshot candaceosv1.WebUISnapshot
		Expect(protojson.Unmarshal(response.Body.Bytes(), &snapshot)).To(Succeed())
		Expect(snapshot.GeneratedAt.AsTime()).To(Equal(generatedAt))
		Expect(snapshot.System.Name).To(Equal("CandaceOS"), "the mounted web UI must normalize its browser contract")
		Expect(snapshot.System.Status).To(Equal("ready"))
		Expect(snapshot.Fleet.LeaderId).To(Equal("node-a"))
		Expect(snapshot.Fleet.Term).To(Equal(uint64(9)))
		Expect(response.Body.String()).To(ContainSubstring(`"attention":[]`))
		Expect(response.Body.String()).To(ContainSubstring(`"nodes":[]`))
		Expect(response.Body.String()).To(ContainSubstring(`"apps":[]`))
		Expect(response.Body.String()).To(ContainSubstring(`"activity":[]`))
	})

	It("sends the current snapshot as the first SSE event", func() {
		generatedAt := time.Date(2026, time.August, 17, 12, 34, 56, 0, time.UTC)
		backend.EXPECT().Snapshot(gomock.Any()).Return(&candaceosv1.WebUISnapshot{
			GeneratedAt: timestamppb.New(generatedAt),
			System:      &candaceosv1.WebUISystem{Name: "CandaceOS", Status: "ready"},
			Attention:   []*candaceosv1.WebUIAttention{},
			Fleet:       &candaceosv1.WebUIFleet{Nodes: []*candaceosv1.WebUINode{}},
			Apps:        []*candaceosv1.WebUIApp{},
			Activity:    []*candaceosv1.WebUIActivity{},
		}, nil)
		updates := make(chan struct{})
		var cancelled atomic.Int32
		backend.EXPECT().Subscribe().Return((<-chan struct{})(updates), func() { cancelled.Add(1) })
		server := httptest.NewServer(handler)
		defer server.Close()

		request, err := http.NewRequest(http.MethodGet, server.URL+browserroutes.Events, nil)
		Expect(err).NotTo(HaveOccurred())
		response, err := server.Client().Do(request)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(response.Header.Get("Content-Type")).To(Equal("text/event-stream"))

		reader := bufio.NewReader(response.Body)
		eventLine, err := reader.ReadString('\n')
		Expect(err).NotTo(HaveOccurred())
		Expect(eventLine).To(Equal("event: snapshot\n"))
		dataLine, err := reader.ReadString('\n')
		Expect(err).NotTo(HaveOccurred())
		Expect(dataLine).To(HavePrefix("data: "))
		var snapshot candaceosv1.WebUISnapshot
		Expect(protojson.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(dataLine, "data: "))), &snapshot)).To(Succeed())
		Expect(snapshot.GeneratedAt.AsTime()).To(Equal(generatedAt))
		Expect(snapshot.System.Status).To(Equal("ready"))

		Expect(response.Body.Close()).To(Succeed())
		Eventually(cancelled.Load).Should(Equal(int32(1)))
	})
})

func readySnapshot() *candaceosv1.WebUISnapshot {
	return &candaceosv1.WebUISnapshot{
		System:    &candaceosv1.WebUISystem{Name: "CandaceOS", Status: "ready"},
		Attention: []*candaceosv1.WebUIAttention{},
		Fleet:     &candaceosv1.WebUIFleet{Nodes: []*candaceosv1.WebUINode{}},
		Apps:      []*candaceosv1.WebUIApp{},
		Activity:  []*candaceosv1.WebUIActivity{},
	}
}

func jsonRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func perform(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
