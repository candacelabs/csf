package workcontinuity

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"text/template"

	"google.golang.org/protobuf/encoding/protojson"

	workv1 "github.com/candacelabs/csf/proto/candace/work/v1"
)

const (
	githubIssuesPath     = "/issues"
	githubTitleField     = "title="
	githubTypedFieldFlag = "--field"
	githubIssueIDField   = "issue_id="
	githubBlockedByPath  = "/dependencies/blocked_by"
	maxTicketBody        = 65536
	maxTicketItems       = 100
	// GitHub permits at most 50 issues per native dependency relationship.
	// https://github.blog/changelog/2025-08-21-dependencies-on-issues/
	maxGitHubBlockers       = 50
	maxTicketItemLength     = 8192
	ticketExampleRepository = "example/project"
	ticketExampleDependency = "https://github.com/example/project/issues/1"
	ticketScopeField        = "scope"
	ticketAcceptanceField   = "acceptance"
	githubURLPrefix         = "https://github.com/"
)

//go:embed ticket.md.tmpl
var ticketTemplateText string

var ticketTemplate = template.Must(template.New("ticket").Funcs(template.FuncMap{
	"condition": dependencyConditionText,
	"stackable": func(condition workv1.DependencyCondition) bool {
		return condition == workv1.DependencyCondition_DEPENDENCY_CONDITION_STACKED_OR_MERGED
	},
}).Parse(ticketTemplateText))

// TicketTemplate supplies editable input for the same contract creation uses.
func TicketTemplate() *workv1.TicketSpec {
	return &workv1.TicketSpec{
		SchemaVersion: 1,
		Repository:    ticketExampleRepository,
		Title:         "Describe the requested change",
		Goal:          "Describe the resulting behavior.",
		Scope:         []string{"Describe the work included."},
		Acceptance:    []string{"Describe an observable completion criterion."},
		Dependencies: []*workv1.TicketDependency{{
			IssueUrl:  ticketExampleDependency,
			Condition: workv1.DependencyCondition_DEPENDENCY_CONDITION_STACKED_OR_MERGED,
		}},
	}
}

// RenderTicket validates author input and applies the repository's ticket template.
// The template renders only the development paths selected by the typed input.
func RenderTicket(spec *workv1.TicketSpec) (string, error) {
	if err := workv1.ValidateTicketSpec(spec); err != nil {
		return "", err
	}
	// Liquid Proto currently cannot refine repeated fields. The consuming
	// boundary owns list cardinality, item text and duplicate dependencies.
	for _, list := range []struct {
		name  string
		items []string
	}{
		{ticketScopeField, spec.Scope}, {ticketAcceptanceField, spec.Acceptance},
	} {
		if len(list.items) == 0 || len(list.items) > maxTicketItems {
			return "", fmt.Errorf("%s must contain 1 to %d items", list.name, maxTicketItems)
		}
		for _, item := range list.items {
			if strings.TrimSpace(item) == "" || len(item) > maxTicketItemLength {
				return "", fmt.Errorf("%s contains a blank or oversized item", list.name)
			}
		}
	}
	if len(spec.Dependencies) > maxTicketItems {
		return "", fmt.Errorf("too many dependencies")
	}
	seen := make(map[string]bool, len(spec.Dependencies))
	blockers := 0
	for _, dependency := range spec.Dependencies {
		if err := workv1.ValidateTicketDependency(dependency); err != nil {
			return "", err
		}
		if _, err := issueEndpoint(dependency.IssueUrl); err != nil {
			return "", err
		}
		identity := strings.ToLower(dependency.IssueUrl)
		if seen[identity] {
			return "", fmt.Errorf("duplicate dependency %s", dependency.IssueUrl)
		}
		seen[identity] = true
		if dependency.Condition != workv1.DependencyCondition_DEPENDENCY_CONDITION_STACKED_OR_MERGED {
			blockers++
		}
	}
	if blockers > maxGitHubBlockers {
		return "", fmt.Errorf("ticket exceeds GitHub's limit of %d native blockers", maxGitHubBlockers)
	}
	var body bytes.Buffer
	if err := ticketTemplate.Execute(&body, spec); err != nil {
		return "", err
	}
	if body.Len() > maxTicketBody {
		return "", fmt.Errorf("rendered ticket exceeds %d bytes", maxTicketBody)
	}
	return body.String(), nil
}

func dependencyConditionText(condition workv1.DependencyCondition) (string, error) {
	switch condition {
	case workv1.DependencyCondition_DEPENDENCY_CONDITION_COMPLETED:
		return "Completed", nil
	case workv1.DependencyCondition_DEPENDENCY_CONDITION_IMPLEMENTATION_MERGED:
		return "Implementation merged", nil
	case workv1.DependencyCondition_DEPENDENCY_CONDITION_STACKED_OR_MERGED:
		return "Stack in a new worktree or wait for merge", nil
	default:
		return "", fmt.Errorf("unknown dependency condition %s", condition)
	}
}

// CreateTicket validates and resolves dependencies before creating an issue,
// then attaches GitHub's native blocked-by relationships for strict prerequisites.
// Stackable dependencies remain linked in the body without a native blocker.
// GitHub offers no transaction across these operations; a partial receipt
// retains the issue URL.
// Conditions describe the requested outcome; this method does not schedule work
// or decide whether a dependency has been satisfied.
func (source *GitHubSource) CreateTicket(ctx context.Context, spec *workv1.TicketSpec) (*workv1.TicketReceipt, error) {
	body, err := RenderTicket(spec)
	if err != nil {
		return nil, err
	}
	dependencies := make([]*workv1.SourceIssue, 0, len(spec.Dependencies))
	for _, dependency := range spec.Dependencies {
		endpoint, err := issueEndpoint(dependency.IssueUrl)
		if err != nil {
			return nil, err
		}
		data, err := source.command(ctx, githubAPICommand, endpoint)
		if err != nil {
			return nil, fmt.Errorf("resolve dependency %s: %w", dependency.IssueUrl, err)
		}
		issue := &workv1.SourceIssue{}
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, issue); err != nil {
			return nil, fmt.Errorf("decode dependency: %w", err)
		}
		if !strings.EqualFold(issue.HtmlUrl, dependency.IssueUrl) || issue.Id <= 0 || issue.Number <= 0 || (issue.State != IssueOpen && issue.State != IssueClosed) {
			return nil, fmt.Errorf("dependency identity/state mismatch: %s", dependency.IssueUrl)
		}
		dependencies = append(dependencies, issue)
	}
	endpoint := githubRepositoryPrefix + "/" + spec.Repository + githubIssuesPath
	data, err := source.command(ctx, githubAPICommand, endpoint, githubMethodFlag, http.MethodPost,
		githubRawFieldFlag, githubTitleField+spec.Title, githubRawFieldFlag, githubBodyField+body)
	if err != nil {
		return nil, fmt.Errorf("create ticket: %w; inspect repository issues before retrying", err)
	}
	receipt := &workv1.TicketReceipt{Issue: &workv1.SourceIssue{}}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(data, receipt.Issue); err != nil {
		return nil, fmt.Errorf("decode created issue: %w; inspect repository issues before retrying", err)
	}
	expectedURL := githubURLPrefix + spec.Repository + githubIssuesPath + "/" + strconv.FormatInt(receipt.Issue.Number, 10)
	if receipt.Issue.Number <= 0 || !strings.EqualFold(receipt.Issue.HtmlUrl, expectedURL) || receipt.Issue.Title != spec.Title || receipt.Issue.Body != body {
		return receipt, fmt.Errorf("created issue identity/content mismatch; inspect source before retrying")
	}
	for index, dependency := range dependencies {
		if spec.Dependencies[index].Condition == workv1.DependencyCondition_DEPENDENCY_CONDITION_STACKED_OR_MERGED {
			continue
		}
		_, err := source.command(ctx, githubAPICommand, endpoint+"/"+strconv.FormatInt(receipt.Issue.Number, 10)+githubBlockedByPath,
			githubMethodFlag, http.MethodPost, githubTypedFieldFlag, githubIssueIDField+strconv.FormatInt(dependency.Id, 10))
		if err != nil {
			return receipt, fmt.Errorf("ticket created at %s; attach dependency %s failed: %w; reconcile dependencies on that issue", receipt.Issue.HtmlUrl, dependency.HtmlUrl, err)
		}
		receipt.LinkedDependencies = append(receipt.LinkedDependencies, spec.Dependencies[index])
	}
	return receipt, nil
}
