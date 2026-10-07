// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdfs "io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/net/github"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/sessiongate"
)

// The widget operations: how an agent makes a widget for CSF. It lists the
// Workbench's widgets, checks a definition with the same check the Workbench
// runs before installing one, and proposes a checked definition from its
// harness session's worktree, which commits it there and opens the session's
// pull request through the harness. Each is one typed operation, served over
// HTTP here and over MCP by the host app.
const (
	// The operations' names, which are also their MCP tool names.
	ListWidgetsOperation   = "list_widgets"
	CheckWidgetOperation   = "check_widget"
	ProposeWidgetOperation = "propose_widget"

	// WidgetsDirectory is where definitions live in a checkout.
	WidgetsDirectory = "widgets"

	operationsPath = "/api/widgets/"

	gitExecutable = "git"
	gitDirectory  = "-C"
	gitAdd        = "add"
	gitCommit     = "commit"
	gitMessage    = "--message"
	gitRevParse   = "rev-parse"
	gitHead       = "HEAD"
	gitPaths      = "--"

	proposeMessage = "WIDGET %s: %s\n\nChecked by %s: the dialect parses, its refinements hold, every bound field exists in %s and every fixture renders.\n"
)

var (
	// ErrNoDefinitions reports operations built without the Workbench's
	// widgets directory.
	ErrNoDefinitions = errors.New("widget operations: the widgets directory is required")
	// ErrInvalidWidgetName reports a widget name that is not a directory name
	// of the widgets directory.
	ErrInvalidWidgetName = errors.New("widget operations: a widget name is lower-case letters, digits and hyphens")
	// ErrInvalidAssignment reports an assignment identifier that is not one
	// run directory's name.
	ErrInvalidAssignment = errors.New("widget operations: not an assignment identifier")
	// ErrNoSessions reports a session-scoped operation on operations granted
	// no harness state directory or no launcher.
	ErrNoSessions = errors.New("widget operations: proposing needs the harness state directory and the launcher")
	// ErrNoNeed reports a proposal that does not say what the widget shows.
	ErrNoNeed = errors.New("widget operations: say what the widget shows in need")
)

var (
	widgetName     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	assignmentName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
)

// WidgetStatus is one definition and its check.
type WidgetStatus struct {
	Name string `json:"name" jsonschema:"the definition's directory name under widgets/"`
	// Installable is true when every check passed.
	Installable bool     `json:"installable" jsonschema:"true when every check passed and the Workbench draws it"`
	Source      string   `json:"source,omitempty" jsonschema:"the source its stream binds"`
	Reasons     []string `json:"reasons,omitempty" jsonschema:"every reason the check refused it"`
}

// ListWidgetsInput takes nothing.
type ListWidgetsInput struct{}

// ListWidgetsOutput is every definition in the Workbench's widgets directory.
type ListWidgetsOutput struct {
	Widgets []WidgetStatus `json:"widgets"`
	// Sources are the source names a widget's stream may bind.
	Sources []string `json:"sources"`
}

// CheckWidgetInput names one definition, in a session's worktree or, with no
// assignment, the Workbench's own checkout.
type CheckWidgetInput struct {
	Name         string `json:"name" jsonschema:"the definition's directory name under widgets/"`
	AssignmentID string `json:"assignment_id,omitempty" jsonschema:"the harness assignment whose worktree holds widgets/<name>; omit to check the Workbench's own checkout"`
}

// ProposeWidgetInput names one definition in a session's worktree and what
// it shows.
type ProposeWidgetInput struct {
	Name         string `json:"name" jsonschema:"the definition's directory name under widgets/"`
	AssignmentID string `json:"assignment_id" jsonschema:"the harness assignment whose worktree holds widgets/<name>"`
	Need         string `json:"need" jsonschema:"what the widget shows, one line; it becomes the commit subject"`
}

// ProposeWidgetOutput is the proposal: the commit and the pull request, or
// every reason the check refused it, in which case nothing was committed.
type ProposeWidgetOutput struct {
	WidgetStatus
	Commit         string `json:"commit,omitempty"`
	PullRequestURL string `json:"pull_request_url,omitempty"`
}

// WidgetOperations is the three operations over the capabilities they need.
type WidgetOperations struct {
	definitions iofs.IFiles
	state       iofs.IFiles
	launcher    proc.ILauncher
	github      *github.GitHubClient
	sources     widget.Sources
}

// WidgetOperationsOption configures [WidgetOperations].
type WidgetOperationsOption func(operations *WidgetOperations) error

// WithInstalledDefinitions grants the Workbench checkout's widgets directory.
// Required.
func WithInstalledDefinitions(files iofs.IFiles) WidgetOperationsOption {
	return func(operations *WidgetOperations) error {
		operations.definitions = files
		return nil
	}
}

// WithSessionWorktrees grants the harness state directory, whose run
// directories hold each session's record and worktree, and the launcher git
// and gh start through. Without it propose and a session-scoped check are
// refused.
func WithSessionWorktrees(state iofs.IFiles, launcher proc.ILauncher) WidgetOperationsOption {
	return func(operations *WidgetOperations) error {
		operations.state, operations.launcher = state, launcher
		return nil
	}
}

// NewWidgetOperations validates the option set before building the
// operations.
// WithGitHubClient grants the GitHub protocol client a proposal's draft
// pull request is opened through.
func WithGitHubClient(client *github.GitHubClient) WidgetOperationsOption {
	return func(operations *WidgetOperations) error {
		operations.github = client
		return nil
	}
}

func NewWidgetOperations(options ...WidgetOperationsOption) (*WidgetOperations, error) {
	operations := &WidgetOperations{sources: WidgetSources()}
	for _, option := range options {
		if err := option(operations); err != nil {
			return nil, err
		}
	}
	if operations.definitions == nil {
		return nil, ErrNoDefinitions
	}
	return operations, nil
}

// List checks every definition the Workbench would install.
func (operations *WidgetOperations) List(ctx context.Context, input ListWidgetsInput) (ListWidgetsOutput, error) {
	definitions, err := ReadDefinitions(operations.definitions)
	if err != nil && !errors.Is(err, stdfs.ErrNotExist) {
		return ListWidgetsOutput{}, err
	}
	output := ListWidgetsOutput{Widgets: []WidgetStatus{}, Sources: operations.sources.Names()}
	for _, installed := range InstallWidgets(nil, definitions, operations.sources) {
		output.Widgets = append(output.Widgets, statusOf(installed.Name, installed.checked, installed.Reasons))
	}
	return output, nil
}

// Check runs the Workbench's install check on one definition.
func (operations *WidgetOperations) Check(ctx context.Context, input CheckWidgetInput) (WidgetStatus, error) {
	definitions, err := operations.definitionsOf(input.Name, input.AssignmentID)
	if err != nil {
		return WidgetStatus{}, err
	}
	checked, reasons := widget.CheckDefinition[live.AnonymousIdentity](definitions, input.Name, operations.sources)
	return statusOf(input.Name, checked, reasons), nil
}

// Propose checks one definition in a session's worktree and, when it is
// clean, commits widgets/<name> there and publishes the session's branch
// through the harness, which opens its pull request when it has none.
func (operations *WidgetOperations) Propose(ctx context.Context, input ProposeWidgetInput) (ProposeWidgetOutput, error) {
	if strings.TrimSpace(input.Need) == "" || strings.Contains(input.Need, "\n") {
		return ProposeWidgetOutput{}, ErrNoNeed
	}
	if input.AssignmentID == "" {
		return ProposeWidgetOutput{}, fmt.Errorf("%w: a proposal names the session whose worktree holds it", ErrInvalidAssignment)
	}
	status, err := operations.Check(ctx, CheckWidgetInput{Name: input.Name, AssignmentID: input.AssignmentID})
	if err != nil {
		return ProposeWidgetOutput{}, err
	}
	output := ProposeWidgetOutput{WidgetStatus: status}
	if !status.Installable {
		return output, nil
	}
	state, err := operations.runState(input.AssignmentID)
	if err != nil {
		return output, err
	}
	run := func(arguments ...string) (string, error) {
		result, err := operations.launcher.Run(ctx, proc.Command{Executable: gitExecutable, Arguments: append([]string{gitDirectory, state.Worktree}, arguments...), Directory: state.Worktree})
		return strings.TrimSpace(string(result.Stdout)), err
	}
	definition := path.Join(WidgetsDirectory, input.Name)
	message := fmt.Sprintf(proposeMessage, input.Name, input.Need, CheckWidgetOperation, status.Source)
	if _, err := run(gitAdd, gitPaths, definition); err != nil {
		return output, fmt.Errorf("widget operations: stage %s: %w", definition, err)
	}
	if _, err := run(gitCommit, gitMessage, message, gitPaths, definition); err != nil {
		return output, fmt.Errorf("widget operations: commit %s: %w", definition, err)
	}
	if output.Commit, err = run(gitRevParse, gitHead); err != nil {
		return output, err
	}
	output.PullRequestURL, _, err = sessiongate.PublishBranch(ctx, operations.launcher, operations.github, state)
	return output, err
}

// definitionsOf is the widgets directory a check reads: a session's
// worktree's, or the Workbench's own.
func (operations *WidgetOperations) definitionsOf(name string, assignment string) (stdfs.FS, error) {
	if !widgetName.MatchString(name) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidWidgetName, name)
	}
	if assignment == "" {
		return operations.definitions, nil
	}
	if !assignmentName.MatchString(assignment) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidAssignment, assignment)
	}
	if operations.state == nil || operations.launcher == nil {
		return nil, ErrNoSessions
	}
	return stdfs.Sub(operations.state, path.Join(assignment, session.WorktreeDirectory, WidgetsDirectory))
}

// runState reads one session's run record from its run directory.
func (operations *WidgetOperations) runState(assignment string) (*session.RunState, error) {
	content, err := operations.state.ReadFile(path.Join(assignment, session.RunStateFile))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalidAssignment, assignment, err)
	}
	state := &session.RunState{}
	if err := json.Unmarshal(content, state); err != nil {
		return nil, fmt.Errorf("widget operations: decode %s's run record: %w", assignment, err)
	}
	return state, nil
}

func statusOf(name string, checked *widget.CheckedDefinition[live.AnonymousIdentity], reasons []string) WidgetStatus {
	status := WidgetStatus{Name: name, Installable: checked != nil, Reasons: reasons}
	if checked != nil {
		status.Source = checked.Source
	}
	return status
}

// Register serves the three operations over HTTP, each a POST of its input
// as JSON answered with its output.
func (operations *WidgetOperations) Register(router gin.IRouter) {
	router.POST(operationsPath+ListWidgetsOperation, handle(operations.List))
	router.POST(operationsPath+CheckWidgetOperation, handle(operations.Check))
	router.POST(operationsPath+ProposeWidgetOperation, handle(operations.Propose))
}

// handle adapts one typed operation to HTTP: an empty body is the zero
// input, a body that is not its input a 400, a failed operation a 422
// carrying the error.
func handle[In any, Out any](operation func(ctx context.Context, input In) (Out, error)) gin.HandlerFunc {
	return func(request *gin.Context) {
		var input In
		if err := request.ShouldBindJSON(&input); err != nil && request.Request.ContentLength != 0 {
			request.JSON(http.StatusBadRequest, gin.H{errorKey: err.Error()})
			return
		}
		output, err := operation(request.Request.Context(), input)
		if err != nil {
			request.JSON(http.StatusUnprocessableEntity, gin.H{errorKey: err.Error()})
			return
		}
		request.JSON(http.StatusOK, output)
	}
}

const errorKey = "error"
