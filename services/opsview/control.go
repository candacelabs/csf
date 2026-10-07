// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"net/url"
	"path"
	"slices"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/pkg/gotth/live"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
)

// IWorkbenchOperations is the control plane the Workbench acts through: the
// generated harness operations of the CSF service, the same ones the csf CLI
// calls over HTTP and an agent calls over MCP. The page holds no action of its
// own; every button is one of these calls. *csf.Service satisfies it in
// process and *csf.Client over HTTP.
//
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=control.go -destination=mock_operations_test.go -package=opsview_test
type IWorkbenchOperations interface {
	SubmitAgentSession(ctx context.Context, request *harnessv1.SubmitAgentSessionRequest) (*harnessv1.SubmitAgentSessionResponse, error)
	SendAgentSessionMessage(ctx context.Context, request *harnessv1.SendAgentSessionMessageRequest) (*harnessv1.SendAgentSessionMessageResponse, error)
	CancelAgentSession(ctx context.Context, request *harnessv1.CancelAgentSessionRequest) (*harnessv1.CancelAgentSessionResponse, error)
	CheckAgentSessionAdmission(ctx context.Context, request *harnessv1.CheckAgentSessionAdmissionRequest) (*harnessv1.CheckAgentSessionAdmissionResponse, error)
	ReadyAgentSessionPullRequest(ctx context.Context, request *harnessv1.ReadyAgentSessionPullRequestRequest) (*harnessv1.ReadyAgentSessionPullRequestResponse, error)
	MergeAgentSessionPullRequest(ctx context.Context, request *harnessv1.MergeAgentSessionPullRequestRequest) (*harnessv1.MergeAgentSessionPullRequestResponse, error)
	SetAgentExecutorDefault(ctx context.Context, request *harnessv1.SetAgentExecutorDefaultRequest) (*harnessv1.SetAgentExecutorDefaultResponse, error)
	ListAgentSessions(ctx context.Context, request *harnessv1.ListAgentSessionsRequest) (*harnessv1.ListAgentSessionsResponse, error)
	// GetWorkbenchTheme is the operator's workbench-theme.css, applied after
	// the page's token set so it can override any token.
	GetWorkbenchTheme(ctx context.Context, request *pb.GetWorkbenchThemeRequest) (*pb.GetWorkbenchThemeResponse, error)
}

// The control events on the wire. A card's actions and the launch form are
// browser events; what their effects report back is internal.
const (
	EventSend   = "opsview.send"
	EventCancel = "opsview.cancel"
	EventReady  = "opsview.ready"
	EventMerge  = "opsview.merge"
	// FieldExecutor and FieldModel are the default executor form's fields.
	FieldExecutor = "executor"
	FieldModel    = "model"
	// EventExecutor switches the default executor and model, from the launch
	// panel; EventExecutorSet reports the switch back.
	EventExecutor    = "opsview.executor"
	EventExecutorSet = "opsview.executor-set"
	// EventActed carries an action's outcome back to its card, in FieldNotice.
	EventActed   = "opsview.acted"
	FieldMessage = "message"
	FieldNotice  = "notice"

	// EventCheck is the launch form's submission: FieldTicket and
	// FieldTemplate. Its effect prepares the recipe and runs the admission
	// check; EventLaunch submits the recipe that was checked.
	EventCheck    = "opsview.check"
	EventLaunch   = "opsview.launch"
	EventChecked  = "opsview.checked"
	EventLaunched = "opsview.launched"
	EventRefused  = "opsview.refused"
	// EventTemplates lists the recipe templates, one name per line.
	EventTemplates = "opsview.templates"
	FieldTicket    = "ticket"
	FieldTemplate  = "template"
	FieldRecipe    = "recipe"
	FieldCheck     = "check"
	FieldHeld      = "held"
	FieldTemplates = "templates"
	FieldAssigned  = "assignment"

	// RecipeFile and the brief it may name are a template's files, under
	// <templates>/<name>/: the agent.json + brief.md format csf submit reads.
	RecipeFile = "agent.json"

	branchSuffixLength = 8
	schemeHTTPS        = "https"
	schemeHTTP         = "http"
	templateSeparator  = "\n"
)

var (
	// ErrBriefConflict reports a template with both an inline task and a brief.
	ErrBriefConflict = errors.New("ops view: the template sets both task and workspace.brief_path; keep one")
	// ErrBriefOutside reports a template whose brief is not beside it.
	ErrBriefOutside = errors.New("ops view: a template's brief must be a relative path inside its directory")
	// ErrNoOperations reports an option given a nil control plane.
	ErrNoOperations = errors.New("ops view: the operations are required")
	// ErrNoTemplates reports an option given a nil template capability.
	ErrNoTemplates = errors.New("ops view: the template capability is required")
)

// WithOperations makes the page the Workbench: every card gains its actions
// and the page its launch form, each a call to operations. Without it the
// page is a read-only ops view.
func WithOperations(operations IWorkbenchOperations) Option {
	return func(view *OpsView) error {
		if operations == nil {
			return ErrNoOperations
		}
		view.operations = operations
		return nil
	}
}

// WithRecipeTemplates grants the directory the launch form's templates are
// read from: one <name>/agent.json (and the brief it names) per template.
func WithRecipeTemplates(templates iofs.IFiles) Option {
	return func(view *OpsView) error {
		if templates == nil {
			return ErrNoTemplates
		}
		view.templates = templates
		return nil
	}
}

// cardAction is one card button: what the card says while the call runs, and
// the call, which reports what the card says after. refusal is set instead
// when the event cannot be acted on as sent.
type cardAction func(operations IWorkbenchOperations, assignment string, fields live.Fields) (pending string, call func(ctx context.Context) (string, error), refusal string)

// cardActions are the card's operations, by the browser event that asks for
// each. The card registers exactly these events, beside expand.
var cardActions = map[string]cardAction{
	EventSend: func(operations IWorkbenchOperations, assignment string, fields live.Fields) (string, func(ctx context.Context) (string, error), string) {
		message := strings.TrimSpace(fields.Get(FieldMessage))
		if message == "" {
			return "", nil, "Write a message first."
		}
		return "Sending…", func(ctx context.Context) (string, error) {
			// The Workbench is the operator's console: what it sends is the
			// operator's own words, so the harness vets their terms.
			response, err := operations.SendAgentSessionMessage(ctx, &harnessv1.SendAgentSessionMessageRequest{AssignmentId: assignment, Message: message, OperatorAuthored: true})
			return "Sent: queued as turn " + response.GetTurnId() + ".", err
		}, ""
	},
	EventCancel: func(operations IWorkbenchOperations, assignment string, _ live.Fields) (string, func(ctx context.Context) (string, error), string) {
		return "Canceling…", func(ctx context.Context) (string, error) {
			response, err := operations.CancelAgentSession(ctx, &harnessv1.CancelAgentSessionRequest{AssignmentId: assignment})
			return "Canceled: the session is " + phaseLabel(response.GetSession().GetPhase()) + ".", err
		}, ""
	},
	EventReady: func(operations IWorkbenchOperations, assignment string, _ live.Fields) (string, func(ctx context.Context) (string, error), string) {
		return "Marking ready…", func(ctx context.Context) (string, error) {
			_, err := operations.ReadyAgentSessionPullRequest(ctx, &harnessv1.ReadyAgentSessionPullRequestRequest{AssignmentId: assignment})
			return "Pull request marked ready.", err
		}, ""
	},
	EventMerge: func(operations IWorkbenchOperations, assignment string, _ live.Fields) (string, func(ctx context.Context) (string, error), string) {
		return "Merging: the merge path runs its checks first. It keeps going if you close this page.", func(ctx context.Context) (string, error) {
			_, err := operations.MergeAgentSessionPullRequest(ctx, &harnessv1.MergeAgentSessionPullRequestRequest{AssignmentId: assignment})
			return "Merged.", err
		}, ""
	},
}

// confirmedActions are the actions a card asks the operator to confirm, by
// name, before it runs them: the two that cannot be taken back.
var confirmedActions = []string{EventCancel, EventMerge}

// cardEvents are the browser events a card registers: its view events and
// its actions.
func cardEvents() []string {
	events := []string{EventExpand, EventInternals, EventCompose, EventConfirm}
	for name := range cardActions {
		events = append(events, name)
	}
	slices.Sort(events)
	return events
}

func phaseLabel(phase harnessv1.AgentSessionPhase) string {
	return strings.ToLower(strings.TrimPrefix(phase.String(), "AGENT_SESSION_PHASE_"))
}

// actionNames are the actions as a notice names them.
var actionNames = map[string]string{EventSend: "Send", EventCancel: "Cancel", EventReady: "Ready", EventMerge: "Merge"}

// launchState is the launch form: the templates on offer, the recipe last
// checked and its admission check, and what happened last.
type launchState struct {
	Templates []string
	Ticket    string
	Template  string
	// Recipe is the checked recipe as protojson: what Launch submits, so the
	// session launched is exactly the one whose check was shown.
	Recipe  string
	Check   *harnessv1.LaunchCheck
	Held    string
	Pending bool
	Notice  string
}

// executorLabel is a default as the page says it.
func executorLabel(chosen *harnessv1.AgentExecutorDefault) string {
	if chosen.GetModel() == "" {
		return chosen.GetExecutor() + " with each recipe's model"
	}
	return chosen.GetExecutor() + " (" + chosen.GetModel() + ")"
}

func emitRefusal(emit live.Emitter, err error) error {
	return emit(live.Event{Name: EventRefused, FragmentID: LaunchRegion, Fields: live.NewFields(map[string]string{FieldNotice: firstLine(err.Error())})})
}

// RecipeFromTemplate reads the template <name>/agent.json, and the brief it
// names, from templates, and makes it this ticket's recipe: the ticket URL,
// the assignment identifier, and a work branch of the template's own with the
// assignment's first characters appended, so two launches from one template
// never share a branch.
func RecipeFromTemplate(templates stdfs.FS, name string, ticket string, assignment string) (*pb.AgentAssignmentRecipe, error) {
	parsed, err := url.Parse(ticket)
	if err != nil || (parsed.Scheme != schemeHTTPS && parsed.Scheme != schemeHTTP) || parsed.Host == "" {
		return nil, fmt.Errorf("ops view: %q is not a ticket URL", ticket)
	}
	if name == "" || !stdfs.ValidPath(name) || strings.Contains(name, "/") {
		return nil, fmt.Errorf("ops view: %q is not a template name", name)
	}
	content, err := stdfs.ReadFile(templates, path.Join(name, RecipeFile))
	if err != nil {
		return nil, fmt.Errorf("ops view: read template %s: %w", name, err)
	}
	recipe := &pb.AgentAssignmentRecipe{}
	if err := protojson.Unmarshal(content, recipe); err != nil {
		return nil, fmt.Errorf("ops view: decode template %s: %w", name, err)
	}
	if brief := recipe.GetWorkspace().GetBriefPath(); brief != "" {
		if recipe.GetTask() != "" {
			return nil, ErrBriefConflict
		}
		briefPath := path.Join(name, brief)
		if path.IsAbs(brief) || !stdfs.ValidPath(briefPath) || !strings.HasPrefix(briefPath, name+"/") {
			return nil, ErrBriefOutside
		}
		task, err := stdfs.ReadFile(templates, briefPath)
		if err != nil {
			return nil, fmt.Errorf("ops view: read the brief of %s: %w", name, err)
		}
		recipe.Task = string(task)
	}
	recipe.AssignmentId = assignment
	recipe.TicketUrl = ticket
	if workspace := recipe.GetWorkspace(); workspace.GetBranch() != "" {
		workspace.Branch += "-" + assignment[:min(branchSuffixLength, len(assignment))]
	}
	return recipe, nil
}

// ListTemplates names every directory under templates that holds a recipe.
func ListTemplates(templates stdfs.FS) ([]string, error) {
	entries, err := stdfs.ReadDir(templates, rootName)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := stdfs.Stat(templates, path.Join(entry.Name(), RecipeFile)); err == nil {
			names = append(names, entry.Name())
		}
	}
	return names, nil
}
