// Copyright 2026 Candace Labs

package views

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/candacelabs/csf/io/ipc/proc"
)

// The gh invocation that lists main's merged pull requests.
const (
	ghExecutable   = "gh"
	ghPullRequest  = "pr"
	ghList         = "list"
	ghRepository   = "--repo"
	ghState        = "--state"
	ghStateMerged  = "merged"
	ghBase         = "--base"
	ghBaseMain     = "main"
	ghLimit        = "--limit"
	ghJSON         = "--json"
	ghPullFields   = "number,title,mergedAt,body"
	signalTableRow = "|"
	// mergedPullLimit is above every pull request the repository has had, so
	// the history is whole; gh pages through them.
	mergedPullLimit = "2000"
)

var (
	// ErrNoRepository reports a pull request source built without the
	// repository it lists.
	ErrNoRepository = errors.New("views: the repository whose merges are measured is required, as OWNER/NAME")

	// sliceExpression reads a title's leading slice name: "CSF-METRICS (#354): ...".
	sliceExpression = regexp.MustCompile(`^([A-Z][A-Z0-9]*(?:-[A-Z0-9]+)*)\b`)
	// scoreExpression reads a body's "**Ontology score:** 0.5392 → 0.5390" line.
	scoreExpression = regexp.MustCompile(`\*\*Ontology score:\*\*\s*([0-9.]+)\s*→\s*([0-9.]+)`)
	// signalExpression reads one row of the signal table: | `id` | before | after | Δ |.
	signalExpression = regexp.MustCompile("^\\|\\s*`([a-z0-9-]+)`\\s*\\|\\s*([0-9.]+)\\s*\\|\\s*([0-9.]+)\\s*\\|")
)

// MergedPull is one pull request merged into main, as its title and body
// report it.
type MergedPull struct {
	Number   int
	MergedAt time.Time
	// Slice is the title's leading slice name, or empty.
	Slice string
	// Score is the ontology alignment score after the change, when the body
	// reports one; Signals are the signal table's after column.
	Score   *float64
	Signals map[string]float64
}

// PullSource lists main's merged pull requests, oldest first.
type PullSource func(ctx context.Context) ([]MergedPull, error)

// pullRow is the part of gh's pull request JSON the history reads.
type pullRow struct {
	Number   int       `json:"number"`
	Title    string    `json:"title"`
	MergedAt time.Time `json:"mergedAt"`
	Body     string    `json:"body"`
}

// GitHubPulls lists a repository's merged pull requests through gh, run by
// the process capability.
type GitHubPulls struct {
	launcher   proc.ILauncher
	repository string
}

// NewGitHubPulls grants the merged pull requests of repository, OWNER/NAME.
func NewGitHubPulls(launcher proc.ILauncher, repository string) (*GitHubPulls, error) {
	if launcher == nil {
		return nil, fmt.Errorf("%w: nil launcher", ErrInvalidOption)
	}
	if strings.Count(repository, "/") != 1 {
		return nil, ErrNoRepository
	}
	return &GitHubPulls{launcher: launcher, repository: repository}, nil
}

// Merged lists main's merged pull requests, oldest first.
func (pulls *GitHubPulls) Merged(ctx context.Context) ([]MergedPull, error) {
	result, err := pulls.launcher.Run(ctx, proc.Command{Executable: ghExecutable, Arguments: []string{
		ghPullRequest, ghList, ghRepository, pulls.repository, ghState, ghStateMerged, ghBase, ghBaseMain,
		ghLimit, mergedPullLimit, ghJSON, ghPullFields,
	}})
	if err != nil {
		return nil, fmt.Errorf("views: %s %s %s: %w", ghExecutable, ghPullRequest, ghList, err)
	}
	var rows []pullRow
	if err := json.Unmarshal(result.Stdout, &rows); err != nil {
		return nil, fmt.Errorf("views: decode merged pull requests: %w", err)
	}
	merged := make([]MergedPull, 0, len(rows))
	for _, row := range rows {
		merged = append(merged, ParsePull(row.Number, row.Title, row.MergedAt, row.Body))
	}
	slices.SortFunc(merged, func(left MergedPull, right MergedPull) int { return left.MergedAt.Compare(right.MergedAt) })
	return merged, nil
}

// ParsePull reads a merged pull request's slice name from its title, and the
// ontology alignment score and signal table its body reports.
func ParsePull(number int, title string, mergedAt time.Time, body string) MergedPull {
	pull := MergedPull{Number: number, MergedAt: mergedAt.UTC(), Signals: map[string]float64{}}
	if match := sliceExpression.FindStringSubmatch(title); match != nil {
		pull.Slice = match[1]
	}
	if match := scoreExpression.FindStringSubmatch(body); match != nil {
		if after, err := strconv.ParseFloat(match[2], 64); err == nil {
			pull.Score = &after
		}
	}
	scanner := bufio.NewScanner(strings.NewReader(body))
	inTable := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		match := signalExpression.FindStringSubmatch(line)
		if match == nil {
			// The table ends at its first line that is not a table row.
			if inTable && !strings.HasPrefix(line, signalTableRow) {
				break
			}
			continue
		}
		if after, err := strconv.ParseFloat(match[3], 64); err == nil {
			pull.Signals[match[1]] = after
			inTable = true
		}
	}
	return pull
}
