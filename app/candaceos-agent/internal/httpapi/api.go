// Package httpapi exposes the node-local JSON control API.
package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/candacelabs/csf/app/candaceos-agent/internal/agent"
	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const maxRequestBytes = 64 << 10

// API is an authenticated JSON-only HTTP handler.
type API struct {
	nodeID     string
	token      string
	reconciler *agent.Reconciler
	handler    http.Handler
}

// New constructs the complete HTTP surface.
func New(nodeID, token string, reconciler *agent.Reconciler) *API {
	api := &API{nodeID: nodeID, token: token, reconciler: reconciler}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.healthHandler)
	mux.HandleFunc("GET /v1/status", api.statusHandler)
	mux.HandleFunc("PUT /v1/assignment", api.assignmentHandler)
	api.handler = api.authorize(mux)
	return api
}

// ServeHTTP implements http.Handler.
func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.handler.ServeHTTP(w, r)
}

func (a *API) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.token == "" {
			next.ServeHTTP(w, r)
			return
		}
		const prefix = "Bearer "
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, prefix) || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(strings.TrimPrefix(header, prefix))), []byte(a.token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="candaceos-agent"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) healthHandler(w http.ResponseWriter, _ *http.Request) {
	response := healthToProto(a.nodeID, a.reconciler.DryRun())
	if err := candaceosv1.ValidateHealthResponse(response); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "invalid health response"})
		return
	}
	writeProto(w, http.StatusOK, response)
}

func (a *API) statusHandler(w http.ResponseWriter, _ *http.Request) {
	response := statusToProto(a.nodeID, a.reconciler.DryRun(), a.reconciler.Workspace(), a.reconciler.Snapshot())
	if err := candaceosv1.ValidateAgentStatus(response); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "invalid agent status"})
		return
	}
	writeProto(w, http.StatusOK, response)
}

func (a *API) assignmentHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSON(w, status, map[string]string{"error": fmt.Sprintf("reading request body: %v", err)})
		return
	}
	var wireRequest candaceosv1.ReconcileRequest
	if err := (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(body, &wireRequest); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid protobuf JSON request: %v", err)})
		return
	}
	result, err := a.reconciler.Reconcile(r.Context(), &wireRequest)
	if err != nil {
		status := http.StatusUnprocessableEntity
		switch {
		case errors.Is(err, agent.ErrStaleFence), errors.Is(err, agent.ErrFenceConflict):
			status = http.StatusConflict
		case errors.Is(err, agent.ErrExecution):
			status = http.StatusBadGateway
		case errors.Is(err, agent.ErrPersistence):
			status = http.StatusInternalServerError
		}
		var commands []*candaceosv1.Command
		if result != nil {
			commands = result.GetCommands()
		}
		writeJSON(w, status, map[string]any{
			"error":    err.Error(),
			"commands": commands,
		})
		return
	}
	writeProto(w, http.StatusOK, result)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeProto(w http.ResponseWriter, status int, message proto.Message) {
	data, err := (protojson.MarshalOptions{
		UseProtoNames:     true,
		EmitDefaultValues: true,
	}).Marshal(message)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "encoding protobuf JSON response"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}
