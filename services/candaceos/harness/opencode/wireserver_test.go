package opencode

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

// Hand-rolled wire types, permitted only where the pinned SDK has no generated
// type for the endpoint. Each names its gap; sdk_contract_test.go pins the same
// shapes against a live server.

// wireHealth mirrors global/health. SDK gap: v0.19.2 generates no health
// endpoint, so sdkAdapter reaches it through the client's generic Get.
type wireHealth struct {
	Healthy bool   `json:"healthy"`
	Version string `json:"version"`
}

// wireStatus mirrors one entry of session/status. SDK gap: v0.19.2 generates no
// session-status endpoint, so sdkAdapter reaches it through the generic Get.
type wireStatus struct {
	Type sessionPhase `json:"type"`
}

// wireServer is the only HTTP in this suite. It exists for the sdkAdapter
// contract specs alone - authentication, workspace scoping, the three endpoints
// the SDK lacks, error mapping, and SSE decoding - because those are the only
// behaviors that are actually about the wire. Everything above the provider
// seam is exercised against a scripted provider instead.
//
// Handlers never assert: they record under one mutex and tolerate a client that
// has gone away, and specs assert on the record inside Ginkgo nodes.
type wireServer struct {
	mu sync.Mutex

	version      string
	phases       []sessionPhase
	messages     []providerMessage
	abortApplied bool
	abortStatus  int

	paths        []string
	unauthorized int
	unscoped     int
	promptBodies []json.RawMessage

	events chan string
}

func newWireServer() *wireServer {
	return &wireServer{
		version: PinnedServerVersion, abortApplied: true, events: make(chan string, 8),
	}
}

// listen starts the server and reports its base URL.
func (w *wireServer) listen() string {
	GinkgoHelper()
	server := httptest.NewServer(w)
	DeferCleanup(server.Close)
	return server.URL
}

func (w *wireServer) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	username, password, supplied := request.BasicAuth()
	w.mu.Lock()
	w.paths = append(w.paths, request.URL.Path)
	if !supplied || username != "opencode" || password != "secret" {
		w.unauthorized++
	}
	if request.URL.Path != "/global/health" && request.URL.Query().Get("directory") != "/workspace" {
		w.unscoped++
	}
	w.mu.Unlock()

	switch {
	case request.URL.Path == "/global/health":
		w.write(response, wireHealth{Healthy: true, Version: w.reportedVersion()})
	case request.URL.Path == "/session/status":
		w.write(response, w.statusBody())
	case request.URL.Path == "/session/"+fixtureSessionID+"/message":
		w.write(response, w.transcript())
	case request.URL.Path == "/session/"+fixtureSessionID+"/prompt_async":
		w.recordPrompt(request)
		response.WriteHeader(http.StatusNoContent)
	case request.URL.Path == "/session/"+fixtureSessionID+"/abort":
		w.serveAbort(response)
	case request.URL.Path == "/session" || request.URL.Path == "/session/"+fixtureSessionID:
		w.write(response, providerSession{
			ID: fixtureSessionID, Directory: "/workspace", Version: PinnedServerVersion,
		})
	case request.URL.Path == "/event":
		w.serveEvents(response, request)
	default:
		http.NotFound(response, request)
	}
}

func (w *wireServer) serveAbort(response http.ResponseWriter) {
	w.mu.Lock()
	status, applied := w.abortStatus, w.abortApplied
	w.mu.Unlock()
	if status != 0 {
		http.Error(response, "abort failed", status)
		return
	}
	w.write(response, applied)
}

func (w *wireServer) serveEvents(response http.ResponseWriter, request *http.Request) {
	flusher, streaming := response.(http.Flusher)
	if !streaming {
		http.Error(response, "not streaming", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.WriteHeader(http.StatusOK)
	flusher.Flush()
	for {
		select {
		case <-request.Context().Done():
			return
		case payload, open := <-w.events:
			if !open {
				return
			}
			if _, err := fmt.Fprintf(response, "data: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (w *wireServer) recordPrompt(request *http.Request) {
	body, err := io.ReadAll(request.Body)
	w.mu.Lock()
	defer w.mu.Unlock()
	if err != nil {
		return
	}
	w.promptBodies = append(w.promptBodies, body)
}

// write encodes and writes one body, discarding a write error because a client
// that has gone away is expected whenever the runtime cancels a request.
func (w *wireServer) write(response http.ResponseWriter, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		http.Error(response, "unencodable", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "application/json")
	_, _ = response.Write(encoded)
}

func (w *wireServer) reportedVersion() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.version
}

// statusBody reports the session-status map, consuming the scripted phase
// sequence one entry per read. An idle session is absent from the map.
func (w *wireServer) statusBody() map[string]wireStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	phase := phaseIdle
	if len(w.phases) != 0 {
		phase, w.phases = w.phases[0], w.phases[1:]
	}
	if phase == phaseIdle {
		return map[string]wireStatus{}
	}
	return map[string]wireStatus{fixtureSessionID: {Type: phase}}
}

func (w *wireServer) transcript() []providerMessage {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]providerMessage(nil), w.messages...)
}

// publish queues one raw SSE payload for delivery.
func (w *wireServer) publish(payload string) { w.events <- payload }

// requestPaths reports every path the adapter called, in order.
func (w *wireServer) requestPaths() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.paths...)
}

// authFailures reports how many requests arrived without valid credentials.
func (w *wireServer) authFailures() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.unauthorized
}

// scopeFailures reports how many non-health requests omitted the workspace.
func (w *wireServer) scopeFailures() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.unscoped
}

// submittedBodies reports the raw prompt payloads the adapter posted.
func (w *wireServer) submittedBodies() []json.RawMessage {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]json.RawMessage(nil), w.promptBodies...)
}

// newWireAdapter builds an sdkAdapter pointed at a fresh wire server.
func newWireAdapter(server *wireServer) *sdkAdapter {
	GinkgoHelper()
	adapter, err := newSDKAdapter(&candaceosv1.OpenCodeConfig{
		Url: server.listen(), Username: "opencode", Password: "secret",
		RequestTimeout: int64(2 * time.Second), PollInterval: int64(20 * time.Millisecond),
		QueueCapacity: 2, Model: "openrouter/openai/gpt-5.4-nano",
	}, "/workspace", nil)
	Expect(err).NotTo(HaveOccurred())
	return adapter
}
