// Copyright 2026 Candace Labs

package ouroboros

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/services/harness"
)

// The merge path and how the train reads its report.
const (
	bashExecutable = "bash"
	// mergeScript is the one path a pull request takes into main, relative
	// to the repository.
	mergeScript = "tools/merge-pr.sh"
	// languageTitlePrefix marks the operator-only language pull requests the
	// train never merges (NO-SELF-MERGE's exception for LANG).
	languageTitlePrefix = "LANG"
	// trainBazelDirectory is the train's own Bazel output base under the
	// state directory, beside the sessions' shared disk cache.
	trainBazelDirectory = "ouroboros-bazel"
	reasonLines         = 5
	reasonMaxBytes      = 4000
	reasonSelfMerge     = "NO-SELF-MERGE (#249): the pull request's commits carry the merger's own session"
	reasonMerged        = "merged"
	refusalFormat       = "Merge train (ouroboros): `tools/merge-pr.sh %d` exit %d on %s. Fix and keep the pull request ready; the train retries it after the next push.\n\n```\n%s\n```"
	refusalSelfFormat   = "Merge train (ouroboros): pull request %d refused: %s."
	mainRevisionFormat  = "%s"
	gitRevParse         = "rev-parse"
	gitShort            = "--short"
	gitOriginMain       = "origin/main"
	noMainRevision      = "unknown"
	exitCodeNotRun      = -1
)

// reasonMarkers are the lines of a merge report that name the refusal.
var reasonMarkers = []string{"REFUSED", "REGRESSION", "rose", "could not complete", "does not merge cleanly", "FAILED", "drift", "merge-pr:"}

// MergeTrain runs the merge path over every ready pull request, oldest
// first, exactly once per head: a refused head is commented on and retried
// only after its author pushes a new one. The merger is never an author
// session: a pull request whose commits carry the merger's identity is
// refused without running the path.
func (loop *Loop) MergeTrain(ctx context.Context) error {
	ready, err := loop.tickets.ListReadyPullRequests(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, pull := range ready {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.HasPrefix(pull.Title, languageTitlePrefix) {
			continue
		}
		_, seen, err := loop.store.Merge(ctx, pull.Number, pull.HeadSHA)
		if err != nil {
			return err
		}
		if seen {
			continue
		}
		if err := loop.mergeOne(ctx, pull); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (loop *Loop) mergeOne(ctx context.Context, pull PullRequest) error {
	sessions, err := loop.tickets.PullRequestSessions(ctx, pull.Number)
	if err != nil {
		return err
	}
	merge := Merge{PullRequest: pull.Number, HeadSHA: pull.HeadSHA, AuthorSessions: sessions, Merger: loop.merger}
	if slices.Contains(sessions, loop.merger) {
		merge.ExitCode = exitCodeNotRun
		merge.Reason = reasonSelfMerge
		merge.RecordedAt = loop.clock.Now().UTC()
		if err := loop.store.RecordMerge(ctx, merge); err != nil {
			return err
		}
		return errors.Join(ErrSelfMerge, loop.tickets.Comment(ctx, pull.Number, fmt.Sprintf(refusalSelfFormat, pull.Number, reasonSelfMerge)))
	}
	result, runErr := loop.launcher.Run(ctx, proc.Command{
		Executable: bashExecutable,
		Arguments:  []string{mergeScript, strconv.FormatInt(pull.Number, 10)},
		Directory:  loop.repository.directory,
		ExtraEnvironment: []string{
			harness.BazelOutputVariable + "=" + filepath.Join(loop.state.directory, trainBazelDirectory),
			harness.BazelDiskCacheVariable + "=" + filepath.Join(loop.state.directory, harness.BazelDiskCacheDirectory),
			harness.OCamlToolchainCacheVariable + "=" + filepath.Join(loop.state.directory, harness.OCamlToolchainCacheDirectory),
		},
	})
	report := string(result.Stdout) + string(result.Stderr)
	merge.ExitCode = result.ExitCode
	merge.Merged = runErr == nil
	merge.RecordedAt = loop.clock.Now().UTC()
	merge.Reason = reasonMerged
	if runErr != nil {
		var exit *proc.ExitError
		if !errors.As(runErr, &exit) {
			return runErr
		}
		merge.Reason = Refusal(report)
	}
	if err := loop.store.RecordMerge(ctx, merge); err != nil {
		return err
	}
	loop.logger.Info("ouroboros: merge train", "pull_request", pull.Number, "head", pull.HeadSHA, "merged", merge.Merged, "exit", merge.ExitCode)
	if merge.Merged {
		if loop.merged != nil {
			loop.merged(ctx, pull)
		}
		return nil
	}
	return loop.tickets.Comment(ctx, pull.Number, fmt.Sprintf(refusalFormat, pull.Number, merge.ExitCode, loop.mainRevision(ctx), merge.Reason))
}

// Refusal is the refusal reason a merge report carries: its first lines
// naming a refusal, regression, drift or failure, bounded.
func Refusal(report string) string {
	var reasons []string
	for _, line := range strings.Split(report, "\n") {
		for _, marker := range reasonMarkers {
			if strings.Contains(line, marker) {
				reasons = append(reasons, strings.TrimSpace(line))
				break
			}
		}
		if len(reasons) == reasonLines {
			break
		}
	}
	if len(reasons) == 0 {
		lines := strings.Split(strings.TrimRight(report, "\n"), "\n")
		reasons = lines[max(0, len(lines)-reasonLines):]
	}
	reason := strings.Join(reasons, "\n")
	if len(reason) > reasonMaxBytes {
		reason = reason[:reasonMaxBytes]
	}
	return reason
}

// mainRevision names the main the path was checked against, for the
// comment; it is a label, so a failure to read it is not a failure.
func (loop *Loop) mainRevision(ctx context.Context) string {
	result, err := loop.launcher.Run(ctx, proc.Command{Executable: gitExecutable, Arguments: []string{
		gitDirectory, loop.repository.directory, gitRevParse, gitShort, gitOriginMain,
	}})
	if err != nil {
		return noMainRevision
	}
	return fmt.Sprintf(mainRevisionFormat, strings.TrimSpace(string(result.Stdout)))
}
