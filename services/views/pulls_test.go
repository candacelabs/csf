// Copyright 2026 Candace Labs

package views_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/services/views"
)

// pullBody is a merged pull request's body in the shape the template renders.
const pullBody = "## Summary\n\n**Verdict:** done.\n\n" +
	"**Ontology score:** 0.5392 → 0.5390 (Δ -0.0002; lower is better)\n\n" +
	"| Ontology signal | Before | After | Δ |\n|---|---|---|---|\n" +
	"| `csfc-check` | 0 | 0 | +0 |\n| `cs-16` | 43 | 42 | -1 |\n| `unlinked-terms` | 739 | 739 | +0 |\n\n" +
	"| Area | Before | After | Why |\n|---|---|---|---|\n| `ipc/docker` | 1 | 2 | shared |\n"

var _ = Describe("ParsePull", func() {
	It("reads the slice from the title and the after score and signals from the body, and nothing past the signal table", func() {
		pull := views.ParsePull(319, "LABELER (#318): the idle GPU proposes real instances", at(1, 37), pullBody)
		Expect(pull.Slice).To(Equal("LABELER"))
		Expect(pull.Score).To(HaveValue(BeNumerically("~", 0.5390, 1e-9)))
		Expect(pull.Signals).To(Equal(map[string]float64{"csfc-check": 0, "cs-16": 42, "unlinked-terms": 739}))
	})

	It("reads a hyphenated slice name, and none from a lower-case title or a body without a score", func() {
		Expect(views.ParsePull(1, "CSF-METRICS (#354): dashboards", at(1, 0), "").Slice).To(Equal("CSF-METRICS"))
		pull := views.ParsePull(2, "docs: polish", at(1, 0), "no score here")
		Expect(pull.Slice).To(BeEmpty())
		Expect(pull.Score).To(BeNil())
		Expect(pull.Signals).To(BeEmpty())
	})
})

var _ = Describe("GitHubPulls", func() {
	It("lists main's merged pull requests through gh, oldest first", func(ctx SpecContext) {
		launcher := NewMockILauncher(gomock.NewController(GinkgoT()))
		var command proc.Command
		launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, given proc.Command) (proc.Result, error) {
			command = given
			return proc.Result{Stdout: []byte(`[
				{"number":2,"title":"B (#2): later","mergedAt":"2026-10-04T12:00:00Z","body":""},
				{"number":1,"title":"A (#1): earlier","mergedAt":"2026-10-04T10:00:00Z","body":""}]`)}, nil
		})
		pulls, err := views.NewGitHubPulls(launcher, "owner/name")
		Expect(err).NotTo(HaveOccurred())

		merged, err := pulls.Merged(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(merged).To(HaveLen(2))
		Expect(merged[0].Number).To(Equal(1))
		Expect(merged[1].Slice).To(Equal("B"))
		Expect(command.Executable).To(Equal("gh"))
		Expect(command.Arguments).To(ContainElements("--repo", "owner/name", "--state", "merged", "--base", "main"))
	})

	It("refuses a repository that is not OWNER/NAME and a nil launcher", func() {
		_, err := views.NewGitHubPulls(NewMockILauncher(gomock.NewController(GinkgoT())), "name")
		Expect(err).To(MatchError(views.ErrNoRepository))
		_, err = views.NewGitHubPulls(nil, "owner/name")
		Expect(err).To(MatchError(views.ErrInvalidOption))
	})
})
