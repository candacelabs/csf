package webui_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/types/known/timestamppb"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/browserroutes"
	"github.com/candacelabs/csf/services/candaceos/httpserver"
	"github.com/candacelabs/csf/services/candaceos/webui"
)

func TestWebUI(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CandaceOS Core Web UI Suite")
}

var fixedTime = time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)

func richSnapshot() *candaceosv1.WebUISnapshot {
	return &candaceosv1.WebUISnapshot{
		GeneratedAt: timestamppb.New(fixedTime),
		System: &candaceosv1.WebUISystem{
			Name:           "CandaceOS",
			Status:         "healthy",
			Summary:        "4 nodes · quorum healthy",
			Version:        "0.1.0",
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_OLLAMA,
			HarnessModel:   "qwen3:8b",
		},
		Attention: []*candaceosv1.WebUIAttention{{
			Id:          "approval-deploy-42",
			Title:       "Deploy Garden Notes to core-2?",
			Detail:      "This writes a new Compose service and publishes it.",
			Risk:        "writes production",
			RequestedAt: timestamppb.New(fixedTime.Add(-time.Minute)),
		}},
		Run: &candaceosv1.WebUIRun{
			Id:        "run-42",
			SessionId: "session-42",
			Title:     "Build a private garden notes app",
			Status:    "running",
			StartedAt: timestamppb.New(fixedTime.Add(-2 * time.Minute)),
			CanAbort:  true,
			Entries: []*candaceosv1.WebUIRunEntry{
				{Id: "entry-1", Kind: "message", Role: "user", Text: "Build me a garden notes app", At: timestamppb.New(fixedTime.Add(-2 * time.Minute))},
				{Id: "entry-2", Kind: "tool", Name: "shell", Text: "Running focused tests", Detail: "go test ./services/garden/...", Status: "done", At: timestamppb.New(fixedTime.Add(-time.Minute))},
				{Id: "entry-3", Kind: "error", Name: "Claw", Text: "GPU worker unavailable", Status: "failed", At: timestamppb.New(fixedTime)},
			},
		},
		Fleet: &candaceosv1.WebUIFleet{
			LeaderId: "core-2",
			Term:     12,
			Quorum:   &candaceosv1.WebUIQuorum{Healthy: true, Online: 3, Required: 2},
			Nodes: []*candaceosv1.WebUINode{{
				Id:            "core-2",
				Name:          "Core Two",
				Role:          "worker",
				Labels:        map[string]string{"role": "worker"},
				Status:        "healthy",
				Address:       "192.0.2.11",
				Apps:          4,
				CpuPercent:    22,
				MemoryPercent: 61,
				LastSeen:      timestamppb.New(fixedTime),
			}},
		},
		Apps: []*candaceosv1.WebUIApp{{
			Id:        "garden-notes",
			Name:      "Garden Notes",
			Summary:   "Private notes for plants and harvests.",
			Status:    "running",
			NodeId:    "core-2",
			Url:       "/apps/garden-notes",
			Revision:  "a1b2c3d",
			UpdatedAt: timestamppb.New(fixedTime),
		}},
		Activity: []*candaceosv1.WebUIActivity{{
			Id:        "activity-42",
			Kind:      "deploy",
			Title:     "Garden Notes deployed",
			Detail:    "Health check passed on core-2.",
			Status:    "succeeded",
			At:        timestamppb.New(fixedTime),
			ReceiptId: "rcpt_01K2",
		}},
	}
}

func newServer(provider webui.ISnapshotProvider) *httptest.Server {
	GinkgoHelper()
	handler, err := webui.New(provider)
	Expect(err).NotTo(HaveOccurred())
	router := httpserver.NewEngine()
	handler.Register(router)
	return httptest.NewServer(router)
}

func get(server *httptest.Server, path string) (*http.Response, string) {
	GinkgoHelper()
	response, err := http.Get(server.URL + path)
	Expect(err).NotTo(HaveOccurred())
	body, err := io.ReadAll(response.Body)
	Expect(err).NotTo(HaveOccurred())
	Expect(response.Body.Close()).To(Succeed())
	return response, string(body)
}

var _ = Describe("embedded operator UI", func() {
	It("renders the complete server-side home and four-view navigation", func() {
		server := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return richSnapshot(), nil
		}))
		defer server.Close()

		response, body := get(server, browserroutes.Index)
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(response.Header.Get("Content-Type")).To(HavePrefix("text/html"))
		Expect(response.Header.Get("Content-Security-Policy")).To(ContainSubstring("default-src 'self'"))
		Expect(response.Header.Get("Cache-Control")).To(Equal("no-store"))

		for _, label := range []string{
			"What do you want?",
			"Needs you",
			"Claw is working",
			"Ollama · qwen3:8b",
			"Fleet",
			"Leader",
			"Quorum",
			"Apps",
			"Recent receipts",
			"Activity",
		} {
			Expect(body).To(ContainSubstring(label), "missing core UI label %q", label)
		}
		for _, route := range []string{`data-view="home"`, `data-view="apps"`, `data-view="fleet"`, `data-view="activity"`} {
			Expect(body).To(ContainSubstring(route), "missing view %s", route)
		}
		for _, asset := range []string{
			fmt.Sprintf(`href=%q`, browserroutes.AssetPath("app.css")),
			fmt.Sprintf(`src=%q`, browserroutes.AssetPath("app.js")),
		} {
			Expect(body).To(ContainSubstring(asset), "missing offline asset %s", asset)
		}

		// The first paint is useful before JavaScript or SSE starts.
		for _, value := range []string{"Garden Notes", "core-2", "writes production", "go test ./services/garden/...", "rcpt_01K2"} {
			Expect(body).To(ContainSubstring(value), "missing server-rendered snapshot value %q", value)
		}
		Expect(body).To(ContainSubstring("Worker · 4 apps"),
			"the elected leader's node card must retain its configured deployment role")
		Expect(body).To(MatchRegexp(
			`(?s)<div class="tool-card error-card">.*?<strong>Claw</strong>.*?Failed.*?<p>GPU worker unavailable</p>\s*</div>`,
		))
		Expect(body).To(ContainSubstring(fmt.Sprintf(`href=%q`, browserroutes.ClawChatPath("session-42"))),
			"the current run must deep-link to its chat")

		apiResponse, apiBody := get(server, browserroutes.Snapshot)
		Expect(apiResponse.StatusCode).To(Equal(http.StatusOK))
		Expect(apiBody).To(ContainSubstring(`"harness_backend":"HARNESS_BACKEND_OLLAMA"`))
		Expect(apiBody).To(ContainSubstring(`"harness_model":"qwen3:8b"`))
		Expect(apiBody).To(ContainSubstring(`"session_id":"session-42"`))
	})

	It("renders the exact current Claw session as a live chat", func() {
		value := richSnapshot()
		value.System.HarnessBackend = candaceosv1.HarnessBackend_HARNESS_BACKEND_OPENCODE
		value.System.HarnessModel = "opencode/big-pickle"
		value.System.HarnessCapabilities = []candaceosv1.HarnessCapability{
			candaceosv1.HarnessCapability_HARNESS_CAPABILITY_ACTIVE_TURN_STEERING,
		}
		server := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return value, nil
		}))
		defer server.Close()

		response, body := get(server, browserroutes.ClawChatPath("session-42"))
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(response.Header.Get("Content-Security-Policy")).To(ContainSubstring("connect-src 'self'"))
		for _, label := range []string{
			"Live Claw session",
			"Build a private garden notes app",
			"OpenCode",
			"opencode/big-pickle",
			"Running",
			"Build me a garden notes app",
			"Running focused tests",
			"go test ./services/garden/...",
			"GPU worker unavailable",
			"Send after current",
			"Steer now",
			"Depending on the provider, it may interject or restart its work",
			"Stop this run",
		} {
			Expect(body).To(ContainSubstring(label), "missing chat content %q", label)
		}
		Expect(body).To(ContainSubstring(fmt.Sprintf(`action=%q`, browserroutes.ClawMessagePath("session-42"))))
		Expect(body).To(ContainSubstring(`data-expected-run-id="run-42"`))
		Expect(body).To(ContainSubstring(fmt.Sprintf(
			`data-chat-abort-url=%q`, browserroutes.ClawRunAbortPath("session-42", "run-42"),
		)))
		for _, delivery := range []candaceosv1.HarnessDelivery{
			candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE,
			candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE,
		} {
			Expect(body).To(ContainSubstring(fmt.Sprintf(`data-delivery=%q`, delivery.String())))
		}
		Expect(body).To(ContainSubstring(fmt.Sprintf(
			`data-enum-harness-backend-embedded=%q`,
			candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED.String(),
		)))
		Expect(body).To(ContainSubstring(fmt.Sprintf(
			`data-enum-harness-capability-active-turn-steering=%q`,
			candaceosv1.HarnessCapability_HARNESS_CAPABILITY_ACTIVE_TURN_STEERING.String(),
		)))
		Expect(body).To(ContainSubstring(`data-chat-session="session-42"`))
	})

	It("hides active-turn steering controls when the harness does not advertise support", func() {
		server := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return richSnapshot(), nil
		}))
		defer server.Close()

		_, body := get(server, browserroutes.ClawChatPath("session-42"))
		Expect(body).To(MatchRegexp(`data-chat-busy-actions hidden`))
		Expect(body).To(MatchRegexp(`data-chat-steer-note hidden`))
		Expect(body).To(MatchRegexp(`(?s)<textarea[^>]*id="chat-prompt"[^>]* disabled>`))
	})

	It("does not substitute a different current run into a Claw chat URL", func() {
		server := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return richSnapshot(), nil
		}))
		defer server.Close()

		response, body := get(server, browserroutes.ClawChatPath("a-different-session"))
		Expect(response.StatusCode).To(Equal(http.StatusNotFound))
		Expect(body).NotTo(ContainSubstring("Build a private garden notes app"))
	})

	It("renders the idle follow-up action without destructive busy controls", func() {
		value := richSnapshot()
		value.Run.Status = "succeeded"
		value.Run.CanAbort = false
		server := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return value, nil
		}))
		defer server.Close()

		_, body := get(server, browserroutes.ClawChatPath("session-42"))
		Expect(body).To(ContainSubstring(">Send follow-up</button>"))
		Expect(body).To(MatchRegexp(`data-chat-busy-actions hidden`))
		Expect(body).To(MatchRegexp(`data-chat-stop[^>]* hidden`))
	})

	It("HTML-escapes transcript text in the chat markup and inert snapshot", func() {
		value := richSnapshot()
		value.Run.Entries[0].Text = `</script><script>alert("no")</script>`
		server := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return value, nil
		}))
		defer server.Close()

		_, body := get(server, browserroutes.ClawChatPath("session-42"))
		Expect(body).NotTo(ContainSubstring(`</script><script>alert`))
		Expect(body).To(ContainSubstring(`\u003c/script\u003e`))
		Expect(strings.Count(body, `<script`)).To(Equal(2))
	})

	It("advertises app creation only for a backend that can create apps", func() {
		cases := []struct {
			name           string
			backend        candaceosv1.HarnessBackend
			implementation string
			capabilities   []candaceosv1.HarnessCapability
			createCapable  bool
		}{
			{
				name: "Copilot CLI", backend: candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI,
				capabilities:  []candaceosv1.HarnessCapability{candaceosv1.HarnessCapability_HARNESS_CAPABILITY_WORKSPACE_WRITE},
				createCapable: true,
			},
			{
				name: "embedded", backend: candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED,
				implementation: "external-agent",
				capabilities:   []candaceosv1.HarnessCapability{candaceosv1.HarnessCapability_HARNESS_CAPABILITY_WORKSPACE_WRITE},
				createCapable:  true,
			},
			{
				name: "OpenCode", backend: candaceosv1.HarnessBackend_HARNESS_BACKEND_OPENCODE,
				capabilities:  []candaceosv1.HarnessCapability{candaceosv1.HarnessCapability_HARNESS_CAPABILITY_WORKSPACE_WRITE},
				createCapable: true,
			},
			{name: "Ollama", backend: candaceosv1.HarnessBackend_HARNESS_BACKEND_OLLAMA},
			{name: "demo", backend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO},
		}

		for _, testCase := range cases {
			value := richSnapshot()
			value.System.HarnessBackend = testCase.backend
			value.System.HarnessModel = ""
			value.System.HarnessImplementation = testCase.implementation
			value.System.HarnessCapabilities = testCase.capabilities
			server := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
				return value, nil
			}))
			_, body := get(server, browserroutes.Index)
			server.Close()

			if testCase.createCapable {
				if testCase.implementation != "" {
					Expect(body).To(ContainSubstring(testCase.implementation), testCase.name)
				}
				Expect(body).To(ContainSubstring(`placeholder="Build me a tiny app that tracks the tomatoes in my garden…"`), testCase.name)
				Expect(body).To(ContainSubstring(`>Deploy a notes app</button>`), testCase.name)
				Expect(body).To(ContainSubstring(`>Create an app</button>`), testCase.name)
				Expect(body).NotTo(ContainSubstring(`>Check fleet readiness</button>`), testCase.name)
				continue
			}

			Expect(body).To(ContainSubstring(`placeholder="Show me the current fleet status and placement options…"`), testCase.name)
			Expect(body).To(ContainSubstring(`>Review fleet status</button>`), testCase.name)
			Expect(body).To(ContainSubstring(`>Check fleet readiness</button>`), testCase.name)
			Expect(body).NotTo(ContainSubstring(`>Deploy a notes app</button>`), testCase.name)
			Expect(body).NotTo(ContainSubstring(`>Create an app</button>`), testCase.name)
		}
	})

	It("serves its CSS and browser client from the embedded filesystem", func() {
		server := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return &candaceosv1.WebUISnapshot{}, nil
		}))
		defer server.Close()

		cssResponse, css := get(server, browserroutes.AssetPath("app.css"))
		Expect(cssResponse.StatusCode).To(Equal(http.StatusOK))
		Expect(cssResponse.Header.Get("Content-Type")).To(HavePrefix("text/css"))
		Expect(cssResponse.Header.Get("Cache-Control")).To(Equal("public, max-age=3600"))
		Expect(css).To(ContainSubstring(".prompt-box"))
		Expect(css).To(ContainSubstring("prefers-reduced-motion"))

		jsResponse, javascript := get(server, browserroutes.AssetPath("app.js"))
		Expect(jsResponse.StatusCode).To(Equal(http.StatusOK))
		Expect(jsResponse.Header.Get("Content-Type")).To(ContainSubstring("javascript"))
		for _, path := range []string{
			browserroutes.Events,
			browserroutes.Snapshot,
			browserroutes.Prompts,
			browserroutes.CurrentRunAbort,
			browserroutes.Approval,
		} {
			Expect(javascript).NotTo(ContainSubstring(path), "browser client must receive route %s from its page", path)
		}
		Expect(javascript).NotTo(ContainSubstring("https://"))
		Expect(javascript).NotTo(ContainSubstring("http://"))
		Expect(javascript).To(ContainSubstring(`expected_run_id`))
		for _, enumLiteral := range []string{
			candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED.String(),
			candaceosv1.HarnessCapability_HARNESS_CAPABILITY_ACTIVE_TURN_STEERING.String(),
			candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE.String(),
			candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE.String(),
		} {
			Expect(javascript).NotTo(ContainSubstring(enumLiteral), "browser enum %s must come from its page", enumLiteral)
		}
		Expect(javascript).To(ContainSubstring(`data-enum-harness-delivery-enqueue`))
		Expect(javascript).To(ContainSubstring(`data-enum-harness-capability-active-turn-steering`))
		Expect(javascript).To(ContainSubstring(`data-chat-enqueue`))
		Expect(javascript).To(ContainSubstring(`data-route-claw-messages`))
		Expect(javascript).To(ContainSubstring(`"Steering " + agentName() + " now`),
			"the steering copy stays in the client, with the agent's name supplied by the snapshot")
		Expect(javascript).NotTo(ContainSubstring(`Stopping the active work`))
		Expect(javascript).NotTo(ContainSubstring(`Interrupted and steered Claw`))
	})

	It("keeps chat controls locked across snapshot renders while a message is submitting", func() {
		server := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return richSnapshot(), nil
		}))
		defer server.Close()

		_, javascript := get(server, browserroutes.AssetPath("app.js"))
		_, bindChatAndAfter, found := strings.Cut(javascript, "function bindChat()")
		Expect(found).To(BeTrue())
		bindChat, _, found := strings.Cut(bindChatAndAfter, "function bindActions()")
		Expect(found).To(BeTrue())
		_, renderChatAndAfter, found := strings.Cut(javascript, "function renderChat(run, system)")
		Expect(found).To(BeTrue())
		renderChat, _, found := strings.Cut(renderChatAndAfter, "function runEntry(entry)")
		Expect(found).To(BeTrue())

		lock := strings.Index(bindChat, `form.setAttribute("data-chat-submitting", "true")`)
		post := strings.Index(bindChat, `await postJSON`)
		unlock := strings.Index(bindChat, `form.removeAttribute("data-chat-submitting")`)
		Expect(lock).To(BeNumerically(">=", 0))
		Expect(post).To(BeNumerically(">", lock))
		Expect(unlock).To(BeNumerically(">", post))
		Expect(bindChat).To(ContainSubstring(`if (form.getAttribute("data-chat-submitting") === "true") return`))
		Expect(strings.Count(bindChat, `renderChat(snapshot.run, snapshot.system)`)).To(Equal(2),
			"both acquiring and releasing the form lock must apply the shared control state")
		Expect(renderChat).To(ContainSubstring(`var submitting = form.getAttribute("data-chat-submitting") === "true"`))
		Expect(renderChat).To(ContainSubstring(`var canSteer = canSteerActiveTurn(system)`))
		Expect(renderChat).To(ContainSubstring(`button.disabled = !matches || submitting || (busy && !canSteer)`))
		Expect(renderChat).To(ContainSubstring(`input.disabled = !matches || submitting || (busy && !canSteer)`))
		Expect(renderChat).To(ContainSubstring(`busyActions.hidden = !busy || !canSteer`))
	})

	It("returns the compact snapshot contract with iterable empty lists", func() {
		server := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return &candaceosv1.WebUISnapshot{
				System: &candaceosv1.WebUISystem{Name: "CandaceOS"},
				Fleet:  &candaceosv1.WebUIFleet{Term: 12},
			}, nil
		}))
		defer server.Close()

		response, body := get(server, browserroutes.Snapshot)
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(response.Header.Get("Content-Type")).To(Equal("application/json; charset=utf-8"))

		var payload map[string]any
		Expect(json.Unmarshal([]byte(body), &payload)).To(Succeed())
		for _, field := range []string{"attention", "apps", "activity"} {
			Expect(payload[field]).To(Equal([]any{}), "%s must be [] rather than null", field)
		}
		Expect(payload["run"]).To(BeNil())
		fleet, ok := payload["fleet"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(fleet["term"]).To(Equal(float64(12)), "the existing browser contract exposes terms as JSON numbers")
		Expect(body).NotTo(ContainSubstring(`"term":"12"`))
		Expect(fleet["nodes"]).To(Equal([]any{}))
		Expect(fleet["quorum"]).To(Equal(map[string]any{
			"healthy":  false,
			"online":   float64(0),
			"required": float64(0),
		}))
	})

	It("keeps the SSR shell usable when the provider is temporarily unavailable", func() {
		server := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return nil, errors.New("backend down")
		}))
		defer server.Close()

		pageResponse, page := get(server, browserroutes.Index)
		Expect(pageResponse.StatusCode).To(Equal(http.StatusOK))
		Expect(page).To(ContainSubstring("What do you want?"))
		Expect(page).To(ContainSubstring("Waiting for the local control plane"))
		Expect(page).To(ContainSubstring("Reconnecting"))

		apiResponse, apiBody := get(server, browserroutes.Snapshot)
		Expect(apiResponse.StatusCode).To(Equal(http.StatusServiceUnavailable))
		Expect(apiBody).To(MatchJSON(`{"error":"snapshot unavailable"}`))
	})

	It("HTML-escapes provider text inside markup and the initial JSON snapshot", func() {
		value := richSnapshot()
		value.Attention[0].Title = `</script><script>alert("no")</script>`
		server := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return value, nil
		}))
		defer server.Close()

		_, body := get(server, browserroutes.Index)
		Expect(body).NotTo(ContainSubstring(`</script><script>alert`))
		Expect(body).To(ContainSubstring(`\u003c/script\u003e`))
		Expect(strings.Count(body, `<script`)).To(Equal(2), "only the external client and inert JSON scripts should exist")
	})

	It("uses exact GET-only routes", func() {
		server := newServer(webui.SnapshotFunc(func(ctx context.Context) (*candaceosv1.WebUISnapshot, error) {
			return &candaceosv1.WebUISnapshot{}, nil
		}))
		defer server.Close()

		unknownResponse, _ := get(server, "/not-a-view")
		Expect(unknownResponse.StatusCode).To(Equal(http.StatusNotFound))

		request, err := http.NewRequest(http.MethodPost, server.URL+browserroutes.Snapshot, nil)
		Expect(err).NotTo(HaveOccurred())
		response, err := http.DefaultClient.Do(request)
		Expect(err).NotTo(HaveOccurred())
		defer response.Body.Close()
		Expect(response.StatusCode).To(Equal(http.StatusMethodNotAllowed))
	})
})

var _ = Describe("construction", func() {
	It("rejects a nil snapshot provider", func() {
		handler, err := webui.New(nil)
		Expect(handler).To(BeNil())
		Expect(err).To(MatchError(webui.ErrNilSnapshotProvider))
	})
})
