// Copyright 2026 Candace Labs

package verbs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/csf/prod"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/services/bootstrap"
)

// launched matches a program by executable and leading arguments, the same
// matcher the session runner's suite uses to script a launcher.
func launched(executable string, arguments ...string) gomock.Matcher {
	return gomock.Cond(func(command proc.Command) bool {
		return command.Executable == executable && len(command.Arguments) >= len(arguments) &&
			slices.Equal(command.Arguments[:len(arguments)], arguments)
	})
}

var _ = Describe("the golden measurer", func() {
	const (
		repository = "/repository"
		revision   = "6f7e8d9a"
	)

	// cleanDirectory marks a top-level directory that holds every chief row;
	// chiefViolation marks one with a marker file named after a chief row id,
	// the convention bootstrap.Measure reads.
	cleanDirectory := func(worktree, directory string) {
		Expect(os.MkdirAll(filepath.Join(worktree, directory), 0o700)).To(Succeed())
	}
	chiefViolation := func(worktree, directory, row string) {
		Expect(os.MkdirAll(filepath.Join(worktree, directory), 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(worktree, directory, row), nil, 0o600)).To(Succeed())
	}

	It("refuses a merge raising chief violations", func() {
		main := GinkgoT().TempDir()
		after := GinkgoT().TempDir()
		cleanDirectory(main, "alpha")
		cleanDirectory(after, "alpha")
		chiefViolation(main, "beta", "fanout_at_most_16")
		chiefViolation(after, "beta", "fanout_at_most_16")
		chiefViolation(after, "beta", "cli_purpose_declared")

		mainReading, err := bootstrap.Measure(context.Background(), main)
		Expect(err).NotTo(HaveOccurred())
		afterReading, err := bootstrap.Measure(context.Background(), after)
		Expect(err).NotTo(HaveOccurred())

		metrics := prod.Metrics(mainReading, afterReading)
		Expect(metrics.Refused()).To(BeTrue())
		Expect(metrics.Reasons()).To(ContainElement(prod.MergeGateChiefViolationsRise))
	})

	It("measures the derived share by reproduction, not by a generated header", func() {
		worktree := GinkgoT().TempDir()
		cleanDirectory(worktree, "csf")
		// A source file, a reproduced file, and a hand-written file: the reading
		// classifies each by the program that owns it, so the derived share is
		// 2/3 even though none carries a generated header.
		Expect(os.MkdirAll(filepath.Join(worktree, "pkg"), 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(worktree, "csf", "a.csf"), []byte("artifact"), 0o600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(worktree, "pkg", "BUILD.bazel"), []byte("go_library()"), 0o600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(worktree, "pkg", "hand.go"), nil, 0o600)).To(Succeed())

		reading, err := bootstrap.Measure(context.Background(), worktree)
		Expect(err).NotTo(HaveOccurred())
		Expect(reading.DerivedShare()).To(BeNumerically("~", 2.0/3.0))
	})

	It("allows a clean merge", func() {
		main := GinkgoT().TempDir()
		after := GinkgoT().TempDir()
		cleanDirectory(main, "alpha")
		cleanDirectory(after, "alpha")
		cleanDirectory(after, "beta")

		mainReading, err := bootstrap.Measure(context.Background(), main)
		Expect(err).NotTo(HaveOccurred())
		afterReading, err := bootstrap.Measure(context.Background(), after)
		Expect(err).NotTo(HaveOccurred())

		metrics := prod.Metrics(mainReading, afterReading)
		Expect(metrics.Refused()).To(BeFalse())
		Expect(metrics.Reasons()).To(BeEmpty())
	})

	It("measures the merge result, not HEAD", func() {
		controller := gomock.NewController(GinkgoT())
		launcher := NewMockILauncher(controller)
		var addArguments []string
		var readPath string
		launcher.EXPECT().Run(gomock.Any(), launched(gitExecutable, gitDirectory, repository, gitWorktree, gitWorktreeAdd)).
			DoAndReturn(func(_ context.Context, command proc.Command) (proc.Result, error) {
				addArguments = append([]string(nil), command.Arguments...)
				return proc.Result{}, nil
			})
		launcher.EXPECT().Run(gomock.Any(), launched(gitExecutable, gitDirectory, repository, gitWorktree, gitWorktreeRemove)).
			Return(proc.Result{}, nil)

		measurer := newGoldenMeasurer(launcher, repository, func(_ context.Context, worktree string) (prod.Reading, error) {
			readPath = worktree
			return prod.Reading{}, nil
		})
		reading, err := measurer(context.Background(), "owner", "repo", revision)
		Expect(err).NotTo(HaveOccurred())
		Expect(reading).To(Equal(prod.Reading{}))

		// git -C <repository> worktree add --detach <worktree> <revision>: the
		// detached checkout is named by the revision, not by HEAD.
		Expect(addArguments).To(ContainElement(gitWorktreeDetach))
		Expect(addArguments[len(addArguments)-1]).To(Equal(revision))
		Expect(readPath).To(Equal(addArguments[5]))
	})

	It("removes the worktree when the read fails", func() {
		controller := gomock.NewController(GinkgoT())
		launcher := NewMockILauncher(controller)
		readErr := errors.New("cannot read the checkout")
		var removeArguments []string
		launcher.EXPECT().Run(gomock.Any(), launched(gitExecutable, gitDirectory, repository, gitWorktree, gitWorktreeAdd)).
			Return(proc.Result{}, nil)
		launcher.EXPECT().Run(gomock.Any(), launched(gitExecutable, gitDirectory, repository, gitWorktree, gitWorktreeRemove)).
			DoAndReturn(func(_ context.Context, command proc.Command) (proc.Result, error) {
				removeArguments = append([]string(nil), command.Arguments...)
				return proc.Result{}, nil
			})

		measurer := newGoldenMeasurer(launcher, repository, func(_ context.Context, _ string) (prod.Reading, error) {
			return prod.Reading{}, readErr
		})
		_, err := measurer(context.Background(), "owner", "repo", revision)
		Expect(err).To(MatchError(readErr))

		// The detached worktree is removed even though the read failed, and the
		// temporary parent no longer exists: nothing of the revision outlives it.
		Expect(removeArguments).To(ContainElement(gitWorktreeForce))
		_, statErr := os.Stat(filepath.Dir(removeArguments[len(removeArguments)-1]))
		Expect(os.IsNotExist(statErr)).To(BeTrue())
	})
})
