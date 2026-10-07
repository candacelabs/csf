// Copyright 2026 Candace Labs

package evaluate

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	// ErrUncited reports a pull request body that claims an improvement and
	// carries no citation matching a recorded score.
	ErrUncited = errors.New("evaluate: an improvement is claimed without a suite citation that matches a recorded score")
	// ErrNoRate reports a score with no tool call, which has nothing to cite.
	ErrNoRate = errors.New("evaluate: the score has no tool call to rate")

	// citationPattern reads one citation line as Citation writes it.
	citationPattern = regexp.MustCompile(`eval-suite: build=([0-9a-f]{12}) suite=v([0-9]+) score=[0-9]+\.[0-9] \[[0-9]+\.[0-9], [0-9]+\.[0-9]\] delta=[+-][0-9]+\.[0-9] vs=([0-9a-f]{12})`)
	// claimLines are the summary lines of the pull request template that
	// state the change's result: the verdict and what is possible now. The
	// before line describes the status quo and claims nothing.
	claimLines = []string{"**Verdict:**", "**Now:**"}
	// claimPattern is the comparative wording that claims an improvement.
	claimPattern = regexp.MustCompile(`(?i)\b(improv\w*|faster|fewer|reduc\w*|lower(ed|s)?|better|speed-?ups?)\b`)
)

// Citation is the line a pull request cites score with, measured against
// the score of build vs on the same suite version.
func Citation(score Score, versus Score) (string, error) {
	if score.PerK == nil || versus.PerK == nil {
		return "", ErrNoRate
	}
	return fmt.Sprintf("eval-suite: build=%s suite=v%d score=%.1f [%.1f, %.1f] delta=%+.1f vs=%s",
		score.Build, score.SuiteVersion, *score.PerK, *score.Low, *score.High, *score.PerK-*versus.PerK, versus.Build), nil
}

// Cited is one citation found in a body: what it names, and the line.
type Cited struct {
	Build   string
	Version int
	Versus  string
	Line    string
}

// Citations reads every citation line in body.
func Citations(body string) []Cited {
	var cited []Cited
	for _, match := range citationPattern.FindAllStringSubmatch(body, -1) {
		version, err := strconv.Atoi(match[2])
		if err != nil {
			continue
		}
		cited = append(cited, Cited{Build: match[1], Version: version, Versus: match[3], Line: match[0]})
	}
	return cited
}

// Claims lists the summary lines of body that claim an improvement.
func Claims(body string) []string {
	var claims []string
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		for _, prefix := range claimLines {
			if strings.HasPrefix(trimmed, prefix) && claimPattern.MatchString(trimmed) {
				claims = append(claims, trimmed)
			}
		}
	}
	return claims
}

// Recompute is the citation line the records give for a build on a suite
// version measured against build vs, as Citation writes it.
type Recompute func(ctx context.Context, build string, version int, versus string) (string, error)

// CheckClaims is the proof check (#416 Build 5): a body that claims an
// improvement must carry at least one citation whose line equals the one
// the records give. A body that claims nothing passes.
func CheckClaims(ctx context.Context, body string, recompute Recompute) error {
	claims := Claims(body)
	if len(claims) == 0 {
		return nil
	}
	var reasons []string
	for _, cited := range Citations(body) {
		recorded, err := recompute(ctx, cited.Build, cited.Version, cited.Versus)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf("%q: %v", cited.Line, err))
			continue
		}
		if recorded == cited.Line {
			return nil
		}
		reasons = append(reasons, fmt.Sprintf("%q does not match the records, which give %q", cited.Line, recorded))
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "no eval-suite citation line")
	}
	return fmt.Errorf("%w: claim %q; %s", ErrUncited, claims[0], strings.Join(reasons, "; "))
}
