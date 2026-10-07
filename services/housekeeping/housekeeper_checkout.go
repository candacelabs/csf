// Copyright 2026 Candace Labs

package housekeeping

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/candacelabs/csf/io/ipc/proc"
	cronservice "github.com/candacelabs/csf/services/cron"
)

const (
	liveRemote = "origin"
	liveBranch = "main"
	liveTarget = "refs/remotes/" + liveRemote + "/" + liveBranch
	// notAncestor is git merge-base --is-ancestor's status for "no".
	notAncestor    = 1
	rangeSeparator = ".."
	gitDirectory   = "-C"
)

// The git commands live_checkout runs, each after -C <checkout>.
var (
	gitCurrentBranch = []string{"symbolic-ref", "--quiet", "--short", "HEAD"}
	gitFetchMain     = []string{"fetch", "--quiet", liveRemote, liveBranch}
	gitHead          = []string{"rev-parse", "HEAD"}
	gitTarget        = []string{"rev-parse", liveTarget}
	gitIsAncestor    = []string{"merge-base", "--is-ancestor", "HEAD", liveTarget}
	gitFastForward   = []string{"merge", "--ff-only", "--quiet", liveTarget}
)

// LiveCheckout fast-forwards the checkout the Workbench installs widgets
// from to origin/main, so a merged widget appears with nobody pulling. It
// only ever fast-forwards: a checkout on another branch, one whose main has
// commits origin/main lacks, or one whose local changes the fast-forward
// would overwrite is left exactly as it is and recorded as a finding.
func (housekeeper *Housekeeper) LiveCheckout(ctx context.Context, occurrence cronservice.Occurrence) error {
	at := passOf(occurrence)
	checkout := housekeeper.liveCheckout
	branch, err := housekeeper.git(ctx, checkout, gitCurrentBranch...)
	if err != nil || branch != liveBranch {
		return housekeeper.checkoutFinding(at, fmt.Sprintf("the checkout is on %q, not %s; nothing was changed", branch, liveBranch))
	}
	if _, err := housekeeper.git(ctx, checkout, gitFetchMain...); err != nil {
		return err
	}
	head, err := housekeeper.git(ctx, checkout, gitHead...)
	if err != nil {
		return err
	}
	target, err := housekeeper.git(ctx, checkout, gitTarget...)
	if err != nil || head == target {
		return err
	}
	_, err = housekeeper.git(ctx, checkout, gitIsAncestor...)
	if exitError := (*proc.ExitError)(nil); errors.As(err, &exitError) && exitError.Code == notAncestor {
		return housekeeper.checkoutFinding(at, fmt.Sprintf("%s at %s has commits %s at %s lacks; not a fast-forward, nothing was changed", liveBranch, head, liveTarget, target))
	}
	if err != nil {
		return err
	}
	if housekeeper.dryRun {
		return housekeeper.record(at, Record{Type: RecordFastForward, What: checkout, Detail: head + rangeSeparator + target, DryRun: true})
	}
	if _, err := housekeeper.git(ctx, checkout, gitFastForward...); err != nil {
		return housekeeper.checkoutFinding(at, fmt.Sprintf("the fast-forward to %s was refused, nothing was changed: %v", target, err))
	}
	return housekeeper.record(at, Record{Type: RecordFastForward, What: checkout, Detail: head + rangeSeparator + target})
}

func (housekeeper *Housekeeper) checkoutFinding(at pass, reason string) error {
	return housekeeper.record(at, Record{Type: RecordFinding, Kind: KindLiveCheckout, What: housekeeper.liveCheckout, Detail: reason})
}

// git runs one git command in directory and returns its trimmed output.
func (housekeeper *Housekeeper) git(ctx context.Context, directory string, arguments ...string) (string, error) {
	result, err := housekeeper.launcher.Run(ctx, proc.Command{Executable: programGit, Arguments: append([]string{gitDirectory, directory}, arguments...)})
	return strings.TrimSpace(string(result.Stdout)), err
}
