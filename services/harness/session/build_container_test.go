// Copyright 2026 Candace Labs

package session_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/net/model/claudecode"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/session/mocks"
)

var _ = Describe("the build container", func() {
	const (
		buildImage  = "example.invalid/bazel-execution:1"
		containerID = "4f9c2d1e7a03"
		idVariable  = "CANDACE_BUILD_CONTAINER_ID="
	)
	var (
		ctx      context.Context
		executor *mocks.MockITurnExecutor
		opener   *mocks.MockIOpenTurnExecutor
		runner   *session.AgentSessionRunner
		run      string
		specs    []session.TurnExecutorSpec
		// pinned is whether the checkout the next worktree add makes pins a
		// Bazel execution image, as every checkout of this repository does.
		pinned bool
		// docker is every docker command the runner started.
		docker []proc.Command
	)
	proposal := &model.Proposal[claudecode.Event]{Provider: claudecode.ProviderName}

	BeforeEach(func() {
		ctx = context.Background()
		// The runner keeps its Bazel caches under the temporary directory.
		GinkgoT().Setenv("TMPDIR", GinkgoT().TempDir())
		controller := gomock.NewController(GinkgoT())
		launcher := NewMockILauncher(controller)
		executor = mocks.NewMockITurnExecutor(controller)
		opener = mocks.NewMockIOpenTurnExecutor(controller)
		specs, docker, pinned = nil, nil, false
		state := GinkgoT().TempDir()
		run = session.RunDirectory(state, assignmentID)
		worktree := filepath.Join(run, session.WorktreeDirectory)
		launcher.EXPECT().Run(gomock.Any(), launched("git", "-C", repository, "worktree", "add")).DoAndReturn(
			func(ctx context.Context, command proc.Command) (proc.Result, error) {
				if pinned {
					Expect(os.MkdirAll(filepath.Join(worktree, "bazel"), 0o700)).To(Succeed())
					Expect(os.WriteFile(filepath.Join(worktree, "bazel", "execution_image.txt"), []byte(buildImage+"\n"), 0o600)).To(Succeed())
				}
				return proc.Result{}, nil
			})
		launcher.EXPECT().Run(gomock.Any(), launched("git")).Return(proc.Result{}, nil).AnyTimes()
		launcher.EXPECT().Run(gomock.Any(), launched("gh")).Return(proc.Result{}, nil).AnyTimes()
		launcher.EXPECT().Run(gomock.Any(), launched("docker")).DoAndReturn(
			func(_ context.Context, command proc.Command) (proc.Result, error) {
				docker = append(docker, command)
				return proc.Result{Stdout: []byte(containerID + "\n")}, nil
			}).AnyTimes()
		var err error
		runner, err = session.NewAgentSessionRunner(
			session.WithLauncher(launcher),
			session.WithStateDirectory(state),
			session.WithGateCommand(gateBinary, gateVerb),
			session.WithTurnExecutors(func(spec session.TurnExecutorSpec) (session.ITurnExecutor, error) {
				specs = append(specs, spec)
				return executor, nil
			}),
			session.WithOpenTurnExecutors(func(_ context.Context, spec session.TurnExecutorSpec) (session.IOpenTurnExecutor, error) {
				specs = append(specs, spec)
				return opener, nil
			}),
		)
		Expect(err).NotTo(HaveOccurred())
	})

	Describe("of a one-shot Run", func() {
		BeforeEach(func() {
			executor.EXPECT().Propose(gomock.Any(), gomock.Any()).Return(proposal, nil)
		})

		It("runs detached from the image the worktree pins, is handed to the turn and is removed when the run ends", func() {
			pinned = true
			_, err := runner.Run(ctx, newRecipe())
			Expect(err).NotTo(HaveOccurred())

			Expect(docker).To(HaveLen(2), "one container started, then removed")
			worktree := filepath.Join(run, session.WorktreeDirectory)
			Expect(docker[0].Arguments[:2]).To(Equal([]string{"run", "--detach"}),
				"docker exec refuses a container that was created and never started")
			Expect(docker[0].Arguments).To(ContainElements(buildImage, "--network", "none", fmt.Sprintf("%s:%s", worktree, worktree)))
			Expect(docker[0].Arguments).To(ContainElements("--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())),
				"the caller's numeric ids, which a CI container's passwd does not name")
			Expect(docker[1].Arguments).To(Equal([]string{"rm", "-f", containerID}))
			Expect(specs).To(HaveLen(1))
			Expect(specs[0].Environment).To(ContainElement(idVariable + containerID))

			recorded, err := session.ReadRunState(run)
			Expect(err).NotTo(HaveOccurred())
			Expect(recorded.BuildContainerID).To(BeEmpty(), "a Resume is never handed the ID of a removed container")
			Expect(eventTypes(records(run))).To(ContainElement(session.EventTypeBuildContainerReady))
		})

		It("is not started for a worktree that pins no image", func() {
			_, err := runner.Run(ctx, newRecipe())
			Expect(err).NotTo(HaveOccurred())

			Expect(docker).To(BeEmpty(), "its builds start their own container")
			Expect(specs).To(HaveLen(1))
			Expect(specs[0].Environment).NotTo(ContainElement(HavePrefix(idVariable)))
			Expect(eventTypes(records(run))).NotTo(ContainElement(session.EventTypeBuildContainerReady))
		})
	})

	It("is never started for a session opened with Open, which nothing would remove it for", func() {
		pinned = true
		opener.EXPECT().Close(gomock.Any()).Return(nil)
		opened, err := runner.Open(ctx, newRecipe())
		Expect(err).NotTo(HaveOccurred())
		Expect(opened.Close(ctx)).To(Succeed())

		Expect(docker).To(BeEmpty())
		Expect(specs).To(HaveLen(1))
		Expect(specs[0].Environment).NotTo(ContainElement(HavePrefix(idVariable)))
	})
})
