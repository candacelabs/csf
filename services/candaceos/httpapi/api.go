// Package httpapi is CandaceOS Core's operator-facing HTTP transport.
//
// The package owns the action, event-stream, and health endpoints: request
// decoding and validation, same-origin enforcement on every mutation, the
// response shapes, and the server-sent-event channel the browser client
// subscribes to. All behavior behind those endpoints is supplied by IBackend,
// which the embedding core implements.
//
// Callers may rely on Register mounting onto a caller-owned gin.IRouter and on
// the package serving no path that browserroutes does not name: it never
// creates an engine, never proxies, and never reaches past IBackend for state.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/browserroutes"
	"github.com/candacelabs/csf/services/candaceos/operator"
	"github.com/candacelabs/csf/services/candaceos/webui"
)

const (
	maxActionBytes         = 16 << 10
	healthCheckTimeout     = 2 * time.Second
	eventRefreshInterval   = 2 * time.Second
	eventKeepalivePeriod   = 15 * time.Second
	approvalDecisionReject = "reject"
)

type responseStatus string

const (
	responseStatusOK          responseStatus = "ok"
	responseStatusUnavailable responseStatus = "unavailable"
	responseStatusAborted     responseStatus = "aborted"
	responseStatusApproved    responseStatus = "approved"
	responseStatusRejected    responseStatus = "rejected"

	sseEventSnapshot = "snapshot"
)

// ErrNilBackend reports an API assembled without its behavior owner.
var ErrNilBackend = errors.New("httpapi: nil backend")

// IBackend is the complete behavior required by the local operator transport.
type IBackend interface {
	webui.ISnapshotProvider
	Send(ctx context.Context, prompt string) (string, error)
	SendToClaw(ctx context.Context, sessionID, expectedRunID, prompt string, delivery candaceosv1.HarnessDelivery) (string, error)
	Abort(ctx context.Context) error
	AbortRun(ctx context.Context, sessionID, runID string) error
	ResolveApproval(id, decision string) error
	Subscribe() (<-chan struct{}, func())
	Health(ctx context.Context) error
}

// API is Core's action, event, and health transport. It registers onto a
// caller-owned Gin router and never creates or proxies another router.
type API struct {
	backend IBackend
}

type statusResponse struct {
	Status responseStatus `json:"status"`
}

type runResponse struct {
	RunID string `json:"run_id"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// New constructs Core's action, event, and health transport.
func New(backend IBackend) (*API, error) {
	if backend == nil {
		return nil, ErrNilBackend
	}
	return &API{backend: backend}, nil
}

// Register mounts Core's transport routes on a caller-owned Gin router.
func (a *API) Register(router gin.IRouter) {
	router.GET(browserroutes.Health, a.healthHandler)
	router.POST(browserroutes.Prompts, a.promptHandler)
	router.POST(browserroutes.ClawMessages, a.clawMessageHandler)
	router.POST(browserroutes.CurrentRunAbort, a.abortHandler)
	router.POST(browserroutes.ClawRunAbort, a.clawAbortHandler)
	router.POST(browserroutes.Approval, a.approvalHandler)
	router.GET(browserroutes.Events, a.eventsHandler)
}

func (a *API) clawMessageHandler(c *gin.Context) {
	w := c.Writer
	r := c.Request
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "cross-origin action rejected"})
		return
	}
	var request candaceosv1.WebUIClawMessageRequest
	if err := decodeProtoJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	request.Prompt = strings.TrimSpace(request.Prompt)
	if err := candaceosv1.ValidateWebUIClawMessageRequest(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "prompt, delivery, and expected_run_id are invalid"})
		return
	}
	runID, err := a.backend.SendToClaw(
		r.Context(), c.Param(browserroutes.ParamSessionID), request.ExpectedRunId, request.Prompt, request.Delivery,
	)
	if err != nil {
		status := http.StatusServiceUnavailable
		if errors.Is(err, operator.ErrSessionConflict) ||
			errors.Is(err, operator.ErrRunConflict) ||
			errors.Is(err, operator.ErrRunNotActive) {
			status = http.StatusConflict
		}
		writeJSON(w, status, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, runResponse{RunID: runID})
}

func (a *API) healthHandler(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), healthCheckTimeout)
	defer cancel()
	if err := a.backend.Health(ctx); err != nil {
		writeJSON(c.Writer, http.StatusServiceUnavailable, statusResponse{Status: responseStatusUnavailable})
		return
	}
	writeJSON(c.Writer, http.StatusOK, statusResponse{Status: responseStatusOK})
}

func (a *API) promptHandler(c *gin.Context) {
	w := c.Writer
	r := c.Request
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "cross-origin action rejected"})
		return
	}
	var request candaceosv1.WebUIPromptRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	request.Prompt = strings.TrimSpace(request.Prompt)
	if err := candaceosv1.ValidateWebUIPromptRequest(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "prompt must contain between 1 and 12000 bytes"})
		return
	}
	runID, err := a.backend.Send(r.Context(), request.Prompt)
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, runResponse{RunID: runID})
}

func (a *API) abortHandler(c *gin.Context) {
	w := c.Writer
	r := c.Request
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "cross-origin action rejected"})
		return
	}
	if err := requireEmptyJSON(w, r); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if err := a.backend.Abort(r.Context()); err != nil {
		writeJSON(w, http.StatusConflict, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: responseStatusAborted})
}

func (a *API) clawAbortHandler(c *gin.Context) {
	w := c.Writer
	r := c.Request
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "cross-origin action rejected"})
		return
	}
	if err := requireEmptyJSON(w, r); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if err := a.backend.AbortRun(
		r.Context(), c.Param(browserroutes.ParamSessionID), c.Param(browserroutes.ParamRunID),
	); err != nil {
		writeJSON(w, http.StatusConflict, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: responseStatusAborted})
}

func (a *API) approvalHandler(c *gin.Context) {
	w := c.Writer
	r := c.Request
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "cross-origin action rejected"})
		return
	}
	var request candaceosv1.WebUIApprovalRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	if err := candaceosv1.ValidateWebUIApprovalRequest(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "decision must be approve or reject"})
		return
	}
	if err := a.backend.ResolveApproval(c.Param(browserroutes.ParamApprovalID), request.Decision); err != nil {
		writeJSON(w, http.StatusConflict, errorResponse{Error: err.Error()})
		return
	}
	status := responseStatusApproved
	if request.Decision == approvalDecisionReject {
		status = responseStatusRejected
	}
	writeJSON(w, http.StatusOK, statusResponse{Status: status})
}

func (a *API) eventsHandler(c *gin.Context) {
	w := c.Writer
	r := c.Request
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	controllerUpdates, cancel := a.backend.Subscribe()
	defer cancel()
	refresh := time.NewTicker(eventRefreshInterval)
	defer refresh.Stop()
	keepalive := time.NewTicker(eventKeepalivePeriod)
	defer keepalive.Stop()

	if err := a.writeSnapshotEvent(w, r); err != nil {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case _, ok := <-controllerUpdates:
			if !ok {
				return
			}
			if err := a.writeSnapshotEvent(w, r); err != nil {
				return
			}
		case <-refresh.C:
			if err := a.writeSnapshotEvent(w, r); err != nil {
				return
			}
		case <-keepalive.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			if err := http.NewResponseController(w).Flush(); err != nil {
				return
			}
		}
	}
}

func (a *API) writeSnapshotEvent(w http.ResponseWriter, r *http.Request) error {
	snapshot, err := a.backend.Snapshot(r.Context())
	if err != nil {
		return err
	}
	payload, err := webui.MarshalSnapshot(snapshot)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", sseEventSnapshot, payload); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	if mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])); mediaType != "application/json" {
		return errors.New("Content-Type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxActionBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid JSON request: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain exactly one JSON value")
		}
		return fmt.Errorf("reading request body: %w", err)
	}
	return nil
}

func decodeProtoJSON(w http.ResponseWriter, r *http.Request, destination proto.Message) error {
	if mediaType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])); mediaType != "application/json" {
		return errors.New("Content-Type must be application/json")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxActionBytes)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("reading request body: %w", err)
	}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(payload, destination); err != nil {
		return fmt.Errorf("invalid JSON request: %w", err)
	}
	return nil
}

func requireEmptyJSON(w http.ResponseWriter, r *http.Request) error {
	var request struct{}
	return decodeJSON(w, r, &request)
}

func sameOrigin(r *http.Request) bool {
	raw := r.Header.Get("Origin")
	if raw == "" {
		return true
	}
	origin, err := url.Parse(raw)
	return err == nil && strings.EqualFold(origin.Host, r.Host)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
