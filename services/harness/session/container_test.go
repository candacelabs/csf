// Copyright 2026 Candace Labs

package session_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/docker"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/io/kernel/sandbox"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/session/mocks"
)

var _ = Describe("Container sessions", func() {
	const (
		containerID = "c0ffee"
		image       = "sha256:5e55"
		bazelCache  = "CANDACE_BAZEL_CACHE"
		diskCache   = "CANDACE_BAZEL_DISK_CACHE"
	)
	var (
		ctx        context.Context
		controller *gomock.Controller
		launcher   *MockILauncher
		containers *mocks.MockISessionContainers
		executor   *mocks.MockIOpenTurnExecutor
		spec       session.TurnExecutorSpec
		state      string
		repo       string
		home       string
		run        string
	)

	BeforeEach(func() {
		ctx = context.Background()
		controller = gomock.NewController(GinkgoT())
		launcher = NewMockILauncher(controller)
		containers = mocks.NewMockISessionContainers(controller)
		executor = mocks.NewMockIOpenTurnExecutor(controller)
		state, repo, home = GinkgoT().TempDir(), GinkgoT().TempDir(), GinkgoT().TempDir()
		run = filepath.Join(state, assignmentID)
		Expect(os.MkdirAll(filepath.Join(home, ".config", "gh"), 0o700)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(home, ".claude"), 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(home, ".claude", ".credentials.json"), []byte("{}"), 0o600)).To(Succeed())
	})

	newRunner := func(settings session.ContainerSettings, options ...session.AgentSessionRunnerOption) (*session.AgentSessionRunner, error) {
		return session.NewAgentSessionRunner(append([]session.AgentSessionRunnerOption{
			session.WithLauncher(launcher),
			session.WithStateDirectory(state),
			session.WithGateCommand("csf", gateVerb),
			session.WithContainerSessions(containers, settings),
			session.WithOpenTurnExecutors(func(ctx context.Context, opened session.TurnExecutorSpec) (session.IOpenTurnExecutor, error) {
				spec = opened
				return executor, nil
			}),
		}, options...)...)
	}
	credentials := func() []session.Credential {
		return []session.Credential{
			{Source: filepath.Join(home, ".claude", ".credentials.json"), Home: ".claude/.credentials.json"},
			{Source: filepath.Join(home, ".config", "gh"), Home: ".config/gh"},
		}
	}
	recipe := func() *pb.AgentAssignmentRecipe {
		built := newRecipe()
		built.Workspace.RepositoryPath = repo
		return built
	}

	// receiptsOf are the run's launch receipts.
	receiptsOf := func() []map[string]any {
		receipts := []map[string]any{}
		for _, record := range records(run) {
			if record[session.KeyEventType] == session.EventTypeLaunchReceipt {
				receipts = append(receipts, record)
			}
		}
		return receipts
	}

	Context("opening and closing a session", func() {
		var (
			started docker.SandboxSpec
			// pinned, when set, is the session image the created worktree pins.
			pinned string
		)

		BeforeEach(func() {
			pinned = ""
			launcher.EXPECT().Run(gomock.Any(), launched("git")).DoAndReturn(func(ctx context.Context, command proc.Command) (proc.Result, error) {
				worktree := filepath.Join(run, session.WorktreeDirectory)
				if pinned != "" && slices.Contains(command.Arguments, "worktree") {
					Expect(os.MkdirAll(filepath.Join(worktree, "bazel"), 0o700)).To(Succeed())
					Expect(os.WriteFile(filepath.Join(worktree, session.SessionImageFile), []byte(pinned+"\n"), 0o600)).To(Succeed())
				}
				return proc.Result{Stdout: []byte("CSF Agent <agent@example.invalid> 1700000000 +0000\n")}, nil
			}).AnyTimes()
			containers.EXPECT().StartDetached(gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, requested docker.SandboxSpec) (docker.StartedContainer, error) {
					started = requested
					return docker.StartedContainer{ID: containerID, Pid: 42}, nil
				})
		})

		It("starts one container with the session's user, network, derived limits and exactly its mounts", func() {
			runner, err := newRunner(session.ContainerSettings{Image: image, Credentials: credentials()})
			Expect(err).NotTo(HaveOccurred())
			executor.EXPECT().Close(gomock.Any()).Return(nil)
			containers.EXPECT().ContainerUsage(gomock.Any()).Return(sandbox.Usage{CPU: 3 * time.Second, MemoryPeakBytes: 1 << 20}, nil)
			containers.EXPECT().RemoveContainer(gomock.Any(), containerID).Return(nil)

			opened, err := runner.Open(ctx, recipe(), diskCache+"="+filepath.Join(state, "bazel-disk-cache"), bazelCache+"="+filepath.Join(run, "bazel"))
			Expect(err).NotTo(HaveOccurred())

			Expect(started.Image).To(Equal(image))
			Expect(started.User).To(Equal(session.ContainerUser))
			Expect(started.Network).To(Equal(session.ContainerNetwork))
			Expect(started.MemoryBytes).To(Equal(session.DefaultContainerMemoryBytes))
			Expect(started.NanoCPUs).To(Equal(session.DefaultContainerNanoCPUs))
			Expect(started.PidsLimit).To(Equal(session.DefaultContainerPidsLimit))
			Expect(started.Mounts).To(ConsistOf(
				docker.Mount{Source: run, Target: run},
				docker.Mount{Source: filepath.Join(repo, ".git"), Target: filepath.Join(repo, ".git")},
				docker.Mount{Source: filepath.Join(state, "endpoints"), Target: filepath.Join(state, "endpoints"), ReadOnly: true},
				docker.Mount{Source: filepath.Join(state, "bazel-disk-cache"), Target: filepath.Join(state, "bazel-disk-cache")},
				docker.Mount{Source: filepath.Join(home, ".claude", ".credentials.json"), Target: filepath.Join(run, "home", ".claude", ".credentials.json"), ReadOnly: true},
				docker.Mount{Source: filepath.Join(home, ".config", "gh"), Target: filepath.Join(run, "home", ".config", "gh"), ReadOnly: true},
			))
			Expect(started.Environment).To(ContainElements(
				"HOME="+filepath.Join(run, "home"),
				bazelCache+"="+filepath.Join(run, "bazel"),
				"GIT_CONFIG_KEY_1=credential.https://github.com.helper",
				"GIT_CONFIG_VALUE_1=!gh auth git-credential",
				"GIT_CONFIG_VALUE_2=CSF Agent",
				"GIT_CONFIG_VALUE_3=agent@example.invalid",
			))
			By("creating every mount point as the harness's user before Docker would")
			Expect(filepath.Join(run, "home", ".claude", ".credentials.json")).To(BeARegularFile())
			Expect(filepath.Join(run, "home", ".config", "gh")).To(BeADirectory())
			Expect(filepath.Join(state, "bazel-disk-cache")).To(BeADirectory())
			Expect(filepath.Join(state, "endpoints")).To(BeADirectory())

			By("attaching the executor to the container, inside the worktree")
			Expect(spec.LaunchPrefix[:5]).To(Equal([]string{session.DefaultDockerExecutable, "exec", "-i", "--workdir", filepath.Join(run, session.WorktreeDirectory)}))
			Expect(spec.LaunchPrefix[len(spec.LaunchPrefix)-1]).To(Equal(containerID))
			Expect(spec.LaunchPrefix).To(ContainElement("HOME=" + filepath.Join(run, "home")))

			Expect(opened.Close(ctx)).To(Succeed())
			receipts := receiptsOf()
			Expect(receipts).To(HaveLen(1))
			Expect(receipts[0]).To(HaveKeyWithValue(session.KeyLaunch, session.LaunchContainer))
			Expect(receipts[0]).To(HaveKeyWithValue("cpu_usec", BeNumerically("==", 3_000_000)))
			Expect(receipts[0]).To(HaveKeyWithValue("memory_peak_bytes", BeNumerically("==", 1<<20)))
		})

		It("gives a Copilot session's executor its own environment through the attach prefix", func() {
			runner, err := newRunner(session.ContainerSettings{Image: image})
			Expect(err).NotTo(HaveOccurred())
			executor.EXPECT().Close(gomock.Any()).Return(nil)
			containers.EXPECT().ContainerUsage(gomock.Any()).Return(sandbox.Usage{}, nil)
			containers.EXPECT().RemoveContainer(gomock.Any(), containerID).Return(nil)
			copilot := recipe()
			copilot.Executor = string(session.ExecutorCopilot)

			opened, err := runner.Open(ctx, copilot)
			Expect(err).NotTo(HaveOccurred())

			Expect(spec.Executor).To(Equal(session.ExecutorCopilot))
			instructions := "COPILOT_CUSTOM_INSTRUCTIONS_DIRS=" + filepath.Join(run, "copilot", "instructions")
			Expect(started.Environment).To(ContainElement(instructions))
			Expect(spec.LaunchPrefix).To(ContainElement(instructions), "a variable set only on the docker exec process would never reach Copilot")
			Expect(opened.Close(ctx)).To(Succeed())
		})

		It("runs the image the worktree pins over the fallback", func() {
			runner, err := newRunner(session.ContainerSettings{Image: image})
			Expect(err).NotTo(HaveOccurred())
			pinned = "sha256:9177ed"
			executor.EXPECT().Close(gomock.Any()).Return(nil)
			containers.EXPECT().ContainerUsage(gomock.Any()).Return(sandbox.Usage{}, nil)
			containers.EXPECT().RemoveContainer(gomock.Any(), containerID).Return(nil)
			opened, err := runner.Open(ctx, recipe())
			Expect(err).NotTo(HaveOccurred())
			Expect(started.Image).To(Equal(pinned))
			Expect(opened.Close(ctx)).To(Succeed())
		})

		It("runs a worktree that pins no image, such as one created before the pin existed, in the default session image", func() {
			runner, err := newRunner(session.ContainerSettings{})
			Expect(err).NotTo(HaveOccurred())
			executor.EXPECT().Close(gomock.Any()).Return(nil)
			containers.EXPECT().ContainerUsage(gomock.Any()).Return(sandbox.Usage{}, nil)
			containers.EXPECT().RemoveContainer(gomock.Any(), containerID).Return(nil)
			opened, err := runner.Open(ctx, recipe())
			Expect(err).NotTo(HaveOccurred())
			Expect(started.Image).To(Equal(session.DefaultSessionImage))
			Expect(opened.Close(ctx)).To(Succeed())
		})

		It("mounts the worktree's directory of the executor's conversation store into the session's home, read-write", func() {
			store := GinkgoT().TempDir()
			runner, err := newRunner(session.ContainerSettings{Image: image, ConversationStore: store})
			Expect(err).NotTo(HaveOccurred())
			executor.EXPECT().Close(gomock.Any()).Return(nil)
			containers.EXPECT().ContainerUsage(gomock.Any()).Return(sandbox.Usage{}, nil)
			containers.EXPECT().RemoveContainer(gomock.Any(), containerID).Return(nil)
			opened, err := runner.Open(ctx, recipe())
			Expect(err).NotTo(HaveOccurred())
			projects := filepath.Join(run, "home", ".claude", "projects")
			var conversations []docker.Mount
			for _, mount := range started.Mounts {
				if filepath.Dir(mount.Target) == projects {
					conversations = append(conversations, mount)
				}
			}
			Expect(conversations).To(HaveLen(1))
			Expect(conversations[0].ReadOnly).To(BeFalse())
			Expect(conversations[0].Source).To(Equal(filepath.Join(store, filepath.Base(conversations[0].Target))))
			Expect(conversations[0].Source).To(BeADirectory(), "created as the harness's user before Docker would")
			Expect(conversations[0].Target).To(BeADirectory())
			Expect(opened.Close(ctx)).To(Succeed())
		})

		It("removes the container when the executor cannot open, and still records the receipt", func() {
			runner, err := session.NewAgentSessionRunner(
				session.WithLauncher(launcher),
				session.WithStateDirectory(state),
				session.WithGateCommand("csf", gateVerb),
				session.WithContainerSessions(containers, session.ContainerSettings{Image: image}),
				session.WithOpenTurnExecutors(func(ctx context.Context, opened session.TurnExecutorSpec) (session.IOpenTurnExecutor, error) {
					return nil, errors.New("executor refused")
				}),
			)
			Expect(err).NotTo(HaveOccurred())
			containers.EXPECT().ContainerUsage(gomock.Any()).Return(sandbox.Usage{}, errors.New("cgroup gone"))
			containers.EXPECT().RemoveContainer(gomock.Any(), containerID).Return(nil)
			_, err = runner.Open(ctx, recipe())
			Expect(err).To(MatchError(ContainSubstring("executor refused")))
			Expect(receiptsOf()).To(ConsistOf(HaveKeyWithValue(session.KeyLaunch, session.LaunchContainer)))
		})
	})

	It("records a host session's launch on its receipt", func() {
		launcher.EXPECT().Run(gomock.Any(), launched("git")).Return(proc.Result{}, nil).AnyTimes()
		runner, err := session.NewAgentSessionRunner(
			session.WithLauncher(launcher),
			session.WithStateDirectory(state),
			session.WithGateCommand(gateBinary, gateVerb),
			session.WithOpenTurnExecutors(func(ctx context.Context, opened session.TurnExecutorSpec) (session.IOpenTurnExecutor, error) {
				return executor, nil
			}),
		)
		Expect(err).NotTo(HaveOccurred())
		executor.EXPECT().Close(gomock.Any()).Return(nil)
		opened, err := runner.Open(ctx, recipe())
		Expect(err).NotTo(HaveOccurred())
		Expect(opened.Close(ctx)).To(Succeed())
		Expect(receiptsOf()).To(ConsistOf(HaveKeyWithValue(session.KeyLaunch, session.LaunchHost)))
	})

	Context("undefined usage", func() {
		It("refuses a nil container capability", func() {
			_, err := session.NewAgentSessionRunner(session.WithContainerSessions(nil, session.ContainerSettings{}))
			Expect(err).To(MatchError(session.ErrInvalidOption))
		})

		It("refuses negative limits", func() {
			_, err := newRunner(session.ContainerSettings{Image: image, PidsLimit: -1})
			Expect(err).To(MatchError(session.ErrInvalidOption))
		})

		It("refuses container sessions together with the host sandbox", func() {
			manager, err := sandbox.NewManager("/opt/csf/launcher")
			Expect(err).NotTo(HaveOccurred())
			_, err = newRunner(session.ContainerSettings{Image: image}, session.WithSandbox(manager, ""))
			Expect(err).To(MatchError(session.ErrInvalidOption))
		})

	})
})
