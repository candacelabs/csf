// Copyright 2026 Candace Labs

package dispatch

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/candacelabs/csf/csf"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	dispatchv1 "github.com/candacelabs/csf/proto/candace/dispatch/v1"
)

// The slice dispatcher's typed operations: each is one MCP tool on the CSF
// service and one HTTP route on the host's router, and the csf slice verb is
// a client of the routes.
const (
	AddSliceTool         = "AddSlice"
	HoldSliceTool        = "HoldSlice"
	ReleaseSliceTool     = "ReleaseSlice"
	PauseDispatcherTool  = "PauseDispatcher"
	ResumeDispatcherTool = "ResumeDispatcher"
	DispatcherStateTool  = "DispatcherSnapshot"

	SnapshotPath = "/api/dispatch"
	AddPath      = "/api/dispatch/slices"
	HoldPath     = "/api/dispatch/slices/hold"
	ReleasePath  = "/api/dispatch/slices/release"
	PausePath    = "/api/dispatch/pause"
	ResumePath   = "/api/dispatch/resume"

	// ProvenanceSliceAdd is the provenance source of a slice added through
	// AddSlice.
	ProvenanceSliceAdd = "slice add"
)

// AddSliceInput is one slice to add to the graph: the ticket it delivers, the
// recipe its session runs, its edges and its touch-set.
type AddSliceInput struct {
	SliceID   string                    `json:"slice_id" jsonschema:"the slice identifier: letters, digits, dot, dash or underscore, at most 80"`
	Title     string                    `json:"title,omitempty" jsonschema:"defaults to the recipe's pull request title"`
	TicketURL string                    `json:"ticket_url" jsonschema:"the ticket the slice delivers"`
	Recipe    *pb.AgentAssignmentRecipe `json:"recipe" jsonschema:"the agent.json recipe with its brief as the task"`
	DependsOn []string                  `json:"depends_on,omitempty" jsonschema:"slices that must merge before this one starts"`
	Contends  []string                  `json:"contends,omitempty" jsonschema:"slices this one never runs beside"`
	Hotspots  []string                  `json:"hotspots,omitempty" jsonschema:"named hotspots it touches; slices sharing one contend"`
	Paths     []string                  `json:"paths,omitempty" jsonschema:"path globs it touches; slices overlapping one contend"`
	// Source is the provenance the queue shows for the slice: who added it
	// and why it ranks where it does. Empty is ProvenanceSliceAdd.
	Source string `json:"source,omitempty" jsonschema:"who added the slice and why, shown on the queue; defaults to slice add"`
	// Hold, when set, holds the slice in the same step that enqueues it, with
	// this reason, so the dispatcher cannot launch it before it is released.
	Hold string `json:"hold,omitempty" jsonschema:"hold the slice as it is added, with this reason; release it to let the dispatcher launch it"`
}

// enqueueRequest is the graph's typed record of the input.
func (input AddSliceInput) enqueueRequest() (*dispatchv1.EnqueueSliceRequest, error) {
	if strings.TrimSpace(input.TicketURL) == "" {
		return nil, fmt.Errorf("%w: a ticket URL is required", ErrInvalidSlice)
	}
	if input.Recipe == nil {
		return nil, fmt.Errorf("%w: a recipe is required", ErrInvalidSlice)
	}
	recipe := input.Recipe
	if recipe.GetTicketUrl() == "" {
		recipe.TicketUrl = input.TicketURL
	}
	title := input.Title
	if title == "" {
		title = recipe.GetWorkspace().GetPullRequestTitle()
	}
	source := input.Source
	if source == "" {
		source = ProvenanceSliceAdd
	}
	return &dispatchv1.EnqueueSliceRequest{
		Slice:      &dispatchv1.Slice{SliceId: input.SliceID, Title: title, Recipe: recipe},
		Edges:      &dispatchv1.SliceEdges{DependsOn: input.DependsOn, Contends: input.Contends},
		TouchSet:   &dispatchv1.TouchSet{Hotspots: input.Hotspots, Paths: input.Paths},
		Provenance: &dispatchv1.Provenance{Source: source, IssueUrl: input.TicketURL},
	}, nil
}

// AddSlice enqueues one slice in the graph, holds it in the same step when
// the input asks, and publishes the snapshot; the dispatcher launches it once
// it is ready, released and admitted.
func (service *DispatchService) AddSlice(ctx context.Context, input AddSliceInput) (SliceView, error) {
	request, err := input.enqueueRequest()
	if err != nil {
		return SliceView{}, err
	}
	if err := validateEnqueue(request); err != nil {
		return SliceView{}, err
	}
	hold := Control{Action: ControlHold, SliceID: input.SliceID, Reason: strings.TrimSpace(input.Hold)}
	if input.Hold != "" && (hold.Reason == "" || len(hold.Reason) > maxReasonBytes) {
		return SliceView{}, fmt.Errorf("%w: a hold reason of 1 to %d bytes is required", ErrInvalidControl, maxReasonBytes)
	}
	var view SliceView
	err = service.command(ctx, func(ctx context.Context, store *graph) error {
		if _, err := service.enqueue(ctx, store, request); err != nil {
			return err
		}
		if hold.Reason != "" {
			if err := service.persistControl(ctx, store, hold); err != nil {
				return err
			}
			store.apply(hold)
		}
		service.schedule(ctx, store)
		ranks := store.ranks()
		view = store.sliceView(input.SliceID, ranks, store.frontier(ranks))
		if store.state(input.SliceID) == dispatchv1.SliceState_SLICE_STATE_QUEUED {
			view.Waiting = store.waiting(input.SliceID, store.runningIDs())
		}
		service.publish(store)
		return nil
	})
	return view, err
}

// operations is every typed operation of the slice dispatcher.
func (service *DispatchService) operations() []csf.Operation {
	return []csf.Operation{
		csf.NewOperation(AddSliceTool, "Add one slice to the slice graph: the ticket it delivers, the agent.json recipe its session runs (the brief as its task), the slices it depends on and contends with, and its touch-set. The slice dispatcher launches it once every slice it depends on has merged, nothing it contends with runs, it is not held, and the admission allows.",
			http.MethodPost, AddPath, service.AddSlice),
		csf.NewOperation(HoldSliceTool, "Hold one slice: the slice dispatcher does not launch it until it is released. A held slice that runs keeps running. The reason is recorded.",
			http.MethodPost, HoldPath, service.Hold),
		csf.NewOperation(ReleaseSliceTool, "Release a held slice so the slice dispatcher may launch it again. The reason is recorded.",
			http.MethodPost, ReleasePath, service.Release),
		csf.NewOperation(PauseDispatcherTool, "Pause the slice dispatcher: it launches nothing until resumed. Running sessions are not touched. The reason is recorded.",
			http.MethodPost, PausePath, service.Pause),
		csf.NewOperation(ResumeDispatcherTool, "Resume the slice dispatcher; it launches at once within the latest admission. The reason is recorded.",
			http.MethodPost, ResumePath, service.Resume),
		csf.NewOperation(DispatcherStateTool, "Read the slice dispatcher's snapshot: paused or not, the admission and each limit it is the minimum of with its derivation (harness check, daily budget, rate-limit headroom), the queue in dispatch order with why each slice waits, what runs, what is held, and the next launch.",
			http.MethodGet, SnapshotPath, service.CurrentSnapshot),
	}
}

// Tools is every operation as an MCP tool for the CSF service:
// csf.New(append(options, dispatcher.Tools()...)...).
func (service *DispatchService) Tools() []csf.Option {
	return csf.OperationTools(service.operations())
}

// Register mounts every operation's HTTP route on the caller's router.
func (service *DispatchService) Register(router gin.IRouter) {
	csf.RegisterOperations(router, service.operations())
}
