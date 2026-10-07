// Copyright 2026 Candace Labs

package bootstrap_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/csf/prod"
	"github.com/candacelabs/csf/services/bootstrap"
)

// The fixture is a real repository on disk: the measurer reads a marker file
// per chief count, and the fixer's scripted steps create and remove markers.
// The loop runs locally (no AWS) against this tree. The measurer is the shared
// bootstrap.Measure, the same one the merge quality gate reads through, so the
// loop and the gate can never disagree on what a violation is.

// scriptedFixer applies one tree mutation per call, then does nothing.
type scriptedFixer struct {
	steps []func(repo string) error
	index int
}

func (s *scriptedFixer) fix(_ context.Context, run *bootstrap.Run) error {
	if s.index >= len(s.steps) {
		return nil
	}
	step := s.steps[s.index]
	s.index++
	return step(run.Repo)
}

// marker creates an empty file, a chief-count marker.
func marker(name string) func(repo string) error {
	return func(repo string) error {
		return os.WriteFile(filepath.Join(repo, name), nil, 0o644)
	}
}

// clear removes a marker, driving its chief count to zero.
func clear(name string) func(repo string) error {
	return func(repo string) error {
		return os.Remove(filepath.Join(repo, name))
	}
}

var _ = Describe("the bootstrap loop", func() {
	var (
		dir  string
		repo string
	)

	write := func(name string) {
		path := filepath.Join(repo, name)
		Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
		Expect(os.WriteFile(path, nil, 0o644)).To(Succeed())
	}

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		repo = filepath.Join(dir, "repo")
		Expect(os.MkdirAll(repo, 0o755)).To(Succeed())
	})

	It("reaches a fixpoint locally and emits the archive", func() {
		write("a/keep")
		write("b/fanout_at_most_16")

		fixer := &scriptedFixer{steps: []func(repo string) error{clear("b/fanout_at_most_16")}}
		loop := bootstrap.NewLoop(bootstrap.Measure, fixer.fix)

		result, err := loop.Run(context.Background(), repo)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Done).To(BeTrue())
		Expect(result.Archive).To(BeARegularFile())
		Expect(result.Kept).To(HaveLen(1))
		Expect(result.Final.AllHold()).To(BeTrue())
		Expect(result.Refusals).To(BeEmpty())
	})

	It("keeps only changes that raised the seed", func() {
		write("a/keep")
		write("b/fanout_at_most_16")
		write("c/grammar_decision_fits_one_pick")

		// Clean b (kept), dirty a (refused and rolled back), clean c (kept).
		fixer := &scriptedFixer{steps: []func(repo string) error{
			clear("b/fanout_at_most_16"),
			marker("a/cli_purpose_declared"),
			clear("c/grammar_decision_fits_one_pick"),
		}}
		loop := bootstrap.NewLoop(bootstrap.Measure, fixer.fix)

		result, err := loop.Run(context.Background(), repo)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Done).To(BeTrue())

		// Every kept reading raised the seed over the one before it.
		Expect(result.Kept).To(HaveLen(2))
		Expect(prod.Grew(result.Initial, result.Kept[0])).To(BeTrue())
		Expect(prod.Grew(result.Kept[0], result.Kept[1])).To(BeTrue())

		// The dirty-a change was refused as a new violation in seed "a", and the
		// violation was rolled back out of the tree.
		Expect(result.Refusals).To(ContainElement(prod.Refusal{
			Gate:      prod.GateNewViolationInSeed,
			Signal:    "cli_purpose_declared",
			Directory: "a",
			Before:    0,
			After:     1,
		}))
		_, statErr := os.Stat(filepath.Join(repo, "a", "cli_purpose_declared"))
		Expect(statErr).To(HaveOccurred())
	})

	It("logs every refusal", func() {
		write("a/keep")
		write("b/fanout_at_most_16")

		fixer := &scriptedFixer{steps: []func(repo string) error{marker("a/cli_purpose_declared")}}
		var logs bytes.Buffer
		loop := bootstrap.NewLoop(bootstrap.Measure, fixer.fix).
			WithLogger(slog.New(slog.NewTextHandler(&logs, nil)))

		result, err := loop.Run(context.Background(), repo)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Refusals).NotTo(BeEmpty())
		Expect(logs.String()).To(ContainSubstring("gate=new_violation_in_seed"))
	})

	It("stops at the cap without reaching the fixpoint", func() {
		write("b/fanout_at_most_16")

		fixer := &scriptedFixer{steps: []func(repo string) error{clear("b/fanout_at_most_16")}}
		loop := bootstrap.NewLoop(bootstrap.Measure, fixer.fix).WithCap(1)

		result, err := loop.Run(context.Background(), repo)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Done).To(BeFalse())
		Expect(result.Iterations).To(Equal(1))
		Expect(result.Archive).To(BeARegularFile())
	})
})
