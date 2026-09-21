package agent_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/app/candaceos-agent/internal/agent"
	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos"
)

func TestAgent(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "candaceos-agent core suite")
}

var (
	testSourceRevision = strings.Repeat("a", 40)
	testContentSHA256  = "sha256:" + strings.Repeat("b", 64)
	testDockerCLI      = struct {
		Configured string
		Resolved   string
	}{
		Configured: "docker",
		Resolved:   "/test/docker",
	}
)

func testAssignment(app, project, path string, state candaceosv1.DesiredState) *candaceosv1.Assignment {
	return &candaceosv1.Assignment{
		App: app, Project: project, Path: path, DesiredState: state,
		SourceRevision: testSourceRevision, ContentSha256: testContentSHA256,
	}
}

func testRevisionLimits() *candaceosv1.RevisionLimits {
	return &candaceosv1.RevisionLimits{MaxEntries: 16, MaxBytes: 1 << 20}
}

func commitAgentWorkspace(workspace string) (string, string) {
	runAgentGit(workspace, "init", "-q", "-b", "main")
	runAgentGit(workspace, "config", "user.name", "CandaceOS Test")
	runAgentGit(workspace, "config", "user.email", "candaceos-test@example.invalid")
	runAgentGit(workspace, "add", ".")
	runAgentGit(workspace, "commit", "-q", "-m", "test: app source")
	revision := runAgentGit(workspace, "rev-parse", "HEAD")
	digest, err := candaceos.DigestAppSource(context.Background(), filepath.Join(workspace, "notes"))
	Expect(err).NotTo(HaveOccurred())
	return revision, digest
}

func runAgentGit(workspace string, args ...string) string {
	output, err := exec.Command("git", append([]string{"-C", workspace}, args...)...).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "git %s failed: %s", strings.Join(args, " "), output)
	return strings.TrimSpace(string(output))
}

func runAgentGitCommand(args ...string) string {
	output, err := exec.Command("git", args...).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "git %s failed: %s", strings.Join(args, " "), output)
	return strings.TrimSpace(string(output))
}

func removeSealedAgentTestTree(root string) error {
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.RemoveAll(root)
}

var _ = Describe("DockerComposeRunner", func() {
	var workspace, revisionRoot, sourceRevision, sourceDigest string

	BeforeEach(func() {
		workspace = GinkgoT().TempDir()
		revisionRoot = GinkgoT().TempDir()
		DeferCleanup(removeSealedAgentTestTree, revisionRoot)
		Expect(os.Mkdir(filepath.Join(workspace, "notes"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workspace, "notes", "compose.yaml"), []byte("services: {}\n"), 0o600)).To(Succeed())
		sourceRevision, sourceDigest = commitAgentWorkspace(workspace)
	})

	It("plans preflight then a non-destructive running convergence", func() {
		composeProcess := NewMockIComposeProcessExecutor(gomock.NewController(GinkgoT()))
		runner, err := agent.NewDockerComposeRunner(
			testDockerCLI.Configured, workspace, revisionRoot, testRevisionLimits(), true,
			agent.WithComposeProcessExecutor(composeProcess),
		)
		Expect(err).NotTo(HaveOccurred())
		assignment := testAssignment("notes", "candace-notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_RUNNING)
		assignment.SourceRevision, assignment.ContentSha256 = sourceRevision, sourceDigest
		plan, err := runner.Plan(context.Background(), assignment)
		Expect(err).NotTo(HaveOccurred())
		Expect(plan.Commands).To(HaveLen(2))
		Expect(plan.Commands[0].Argv[len(plan.Commands[0].Argv)-2:]).To(Equal([]string{"config", "--quiet"}))
		Expect(plan.Commands[1].Argv[len(plan.Commands[1].Argv)-4:]).To(Equal([]string{"up", "-d", "--remove-orphans", "notes"}))
		Expect(plan.Commands[1].Argv).NotTo(ContainElement("down"))
		Expect(argumentAfter(plan.Commands[1].Argv, "--project-directory")).To(HavePrefix(revisionRoot + string(filepath.Separator)))
		composeProcess.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, invocation agent.ComposeInvocation) (string, error) {
				Expect(invocation.Argv).To(Equal(plan.Commands[0].Argv))
				return "", nil
			},
		)
		Expect(runner.Execute(context.Background(), plan)).To(Succeed())
	})

	It("stops by Compose identity even after the app source is removed", func() {
		composeProcess := NewMockIComposeProcessExecutor(gomock.NewController(GinkgoT()))
		runner, err := agent.NewDockerComposeRunner(
			testDockerCLI.Configured, workspace, revisionRoot, testRevisionLimits(), true,
			agent.WithComposeProcessExecutor(composeProcess),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.RemoveAll(filepath.Join(workspace, "notes"))).To(Succeed())

		plan, err := runner.Plan(context.Background(), testAssignment("notes", "candace-notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_STOPPED))

		Expect(err).NotTo(HaveOccurred())
		Expect(plan.Commands[0].Argv[len(plan.Commands[0].Argv)-2:]).To(Equal([]string{"config", "--quiet"}))
		Expect(plan.Commands[1].Argv[len(plan.Commands[1].Argv)-2:]).To(Equal([]string{"stop", "notes"}))
		Expect(plan.Commands[1].Argv).NotTo(ContainElement("down"))
		composePath := plan.Commands[0].Argv[len(plan.Commands[0].Argv)-3]
		info, err := os.Stat(composePath)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		contents, err := os.ReadFile(composePath)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(contents)).To(MatchJSON(`{"services":{"notes":{"image":"scratch"}}}`))
		composeProcess.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, invocation agent.ComposeInvocation) (string, error) {
				Expect(invocation.Argv).To(Equal(plan.Commands[0].Argv))
				return "", nil
			},
		)
		Expect(runner.Execute(context.Background(), plan)).To(Succeed())
	})

	It("executes the exact source-independent stop plan in live mode", func() {
		composeProcess := NewMockIComposeProcessExecutor(gomock.NewController(GinkgoT()))
		composeProcess.EXPECT().Resolve(testDockerCLI.Configured).Return(testDockerCLI.Resolved, nil)
		runner, err := agent.NewDockerComposeRunner(
			testDockerCLI.Configured, workspace, revisionRoot, testRevisionLimits(), false,
			agent.WithComposeProcessExecutor(composeProcess),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.RemoveAll(filepath.Join(workspace, "notes"))).To(Succeed())
		plan, err := runner.Plan(context.Background(), testAssignment("notes", "candace-notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_STOPPED))
		Expect(err).NotTo(HaveOccurred())
		var invocations []agent.ComposeInvocation
		composeProcess.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, invocation agent.ComposeInvocation) (string, error) {
				invocations = append(invocations, invocation)
				return "", nil
			},
		).Times(2)

		Expect(runner.Execute(context.Background(), plan)).To(Succeed())

		Expect(invocations[0].Argv).To(Equal(plan.Commands[0].Argv))
		Expect(invocations[1].Argv).To(Equal(plan.Commands[1].Argv))
	})

	It("rejects symbolic links from an approved Git tree", func() {
		outside := GinkgoT().TempDir()
		Expect(os.WriteFile(filepath.Join(outside, "compose.yaml"), []byte("services: {}\n"), 0o600)).To(Succeed())
		Expect(os.Symlink(outside, filepath.Join(workspace, "notes", "escape"))).To(Succeed())
		runAgentGit(workspace, "add", "notes/escape")
		runAgentGit(workspace, "commit", "-q", "-m", "test: add unsupported link")
		sourceRevision = runAgentGit(workspace, "rev-parse", "HEAD")
		runner, err := agent.NewDockerComposeRunner(testDockerCLI.Configured, workspace, revisionRoot, testRevisionLimits(), true)
		Expect(err).NotTo(HaveOccurred())
		assignment := testAssignment("notes", "candace-notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_RUNNING)
		assignment.SourceRevision = sourceRevision
		_, err = runner.Plan(context.Background(), assignment)
		Expect(err).To(MatchError(ContainSubstring("unsupported entry")))
	})

	It("executes the approved commit after the mutable workspace changes", func() {
		Expect(os.WriteFile(filepath.Join(workspace, "notes", "compose.yaml"), []byte("services:\n  unapproved: {}\n"), 0o600)).To(Succeed())
		runner, err := agent.NewDockerComposeRunner(testDockerCLI.Configured, workspace, revisionRoot, testRevisionLimits(), true)
		Expect(err).NotTo(HaveOccurred())
		assignment := testAssignment("notes", "candace-notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_RUNNING)
		assignment.SourceRevision, assignment.ContentSha256 = sourceRevision, sourceDigest

		plan, err := runner.Plan(context.Background(), assignment)

		Expect(err).NotTo(HaveOccurred())
		staged := argumentAfter(plan.Commands[1].Argv, "--project-directory")
		contents, err := os.ReadFile(filepath.Join(staged, "compose.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(contents)).To(Equal("services: {}\n"))
		Expect(string(contents)).NotTo(ContainSubstring("unapproved"))
		digest, err := candaceos.DigestAppSource(context.Background(), staged)
		Expect(err).NotTo(HaveOccurred())
		Expect(digest).To(Equal(sourceDigest))
	})

	It("fetches a missing approved commit without changing the mutable checkout", func() {
		remote := filepath.Join(GinkgoT().TempDir(), "source.git")
		runAgentGitCommand("clone", "--bare", workspace, remote)
		publisher := filepath.Join(GinkgoT().TempDir(), "publisher")
		runAgentGitCommand("clone", remote, publisher)
		runAgentGit(publisher, "config", "user.name", "CandaceOS Publisher")
		runAgentGit(publisher, "config", "user.email", "publisher@example.invalid")
		updatedCompose := []byte("services:\n  notes:\n    image: busybox\n")
		Expect(os.WriteFile(filepath.Join(publisher, "notes", "compose.yaml"), updatedCompose, 0o600)).To(Succeed())
		runAgentGit(publisher, "add", "notes/compose.yaml")
		runAgentGit(publisher, "commit", "-q", "-m", "test: remote app revision")
		remoteRevision := runAgentGit(publisher, "rev-parse", "HEAD")
		remoteDigest, err := candaceos.DigestAppSource(context.Background(), filepath.Join(publisher, "notes"))
		Expect(err).NotTo(HaveOccurred())
		runAgentGit(publisher, "push", "-q", "origin", "main")
		runAgentGit(workspace, "remote", "add", "source", remote)
		_, err = exec.Command("git", "-C", workspace, "rev-parse", "--verify", remoteRevision+"^{commit}").CombinedOutput()
		Expect(err).To(HaveOccurred(), "the worker checkout must begin without the approved commit")

		sourceRepository := filepath.Join(GinkgoT().TempDir(), "source.git")
		runner, err := agent.NewDockerComposeRunnerWithSourceSync(
			testDockerCLI.Configured, workspace, revisionRoot, testRevisionLimits(),
			&candaceosv1.SourceSync{Remote: "source", Repository: sourceRepository, FetchTimeoutNanoseconds: int64(5 * time.Second)}, nil, true,
		)
		Expect(err).NotTo(HaveOccurred())
		assignment := testAssignment("notes", "candace-notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_RUNNING)
		assignment.SourceRevision, assignment.ContentSha256 = remoteRevision, remoteDigest

		plan, err := runner.Plan(context.Background(), assignment)

		Expect(err).NotTo(HaveOccurred())
		staged := argumentAfter(plan.Commands[1].Argv, "--project-directory")
		Expect(os.ReadFile(filepath.Join(staged, "compose.yaml"))).To(Equal(updatedCompose))
		Expect(os.ReadFile(filepath.Join(workspace, "notes", "compose.yaml"))).To(Equal([]byte("services: {}\n")))
		_, err = exec.Command("git", "-C", workspace, "rev-parse", "--verify", remoteRevision+"^{commit}").CombinedOutput()
		Expect(err).To(HaveOccurred(), "source synchronization must not write into the mounted workspace")
		Expect(runAgentGit(sourceRepository, "rev-parse", "--is-bare-repository")).To(Equal("true"))

		assignment.ContentSha256 = sourceDigest
		_, err = runner.Plan(context.Background(), assignment)
		Expect(err).To(MatchError(ContainSubstring("materialized app revision digest mismatch")),
			"remote acquisition must not replace independent content verification")

		Expect(os.RemoveAll(remote)).To(Succeed())
		assignment.ContentSha256 = remoteDigest
		_, err = runner.Plan(context.Background(), assignment)
		Expect(err).NotTo(HaveOccurred(), "an already verified snapshot must remain usable while the remote is unavailable")
	})

	It("bounds source synchronization with the configured timeout", func() {
		runner, err := agent.NewDockerComposeRunnerWithSourceSync(
			testDockerCLI.Configured, workspace, revisionRoot, testRevisionLimits(),
			&candaceosv1.SourceSync{
				Remote: "origin", Repository: filepath.Join(GinkgoT().TempDir(), "source.git"),
				FetchTimeoutNanoseconds: int64(time.Nanosecond),
			}, nil, true,
		)
		Expect(err).NotTo(HaveOccurred())
		assignment := testAssignment("notes", "candace-notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_RUNNING)
		assignment.SourceRevision, assignment.ContentSha256 = sourceRevision, sourceDigest

		started := time.Now()
		_, err = runner.Plan(context.Background(), assignment)

		Expect(err).To(MatchError(ContainSubstring("configured source remote exceeded 1ns")))
		Expect(time.Since(started)).To(BeNumerically("<", 500*time.Millisecond))
	})

	It("keeps the writable source repository outside the workspace and revision cache", func() {
		for _, sourceRepository := range []string{
			filepath.Join(workspace, "source.git"),
			filepath.Join(revisionRoot, "source.git"),
		} {
			_, err := agent.NewDockerComposeRunnerWithSourceSync(
				testDockerCLI.Configured, workspace, revisionRoot, testRevisionLimits(),
				&candaceosv1.SourceSync{
					Remote: "origin", Repository: sourceRepository,
					FetchTimeoutNanoseconds: int64(time.Second),
				}, nil, true,
			)
			Expect(err).To(MatchError(ContainSubstring("source repository must be separate")))
			_, statErr := os.Stat(sourceRepository)
			Expect(os.IsNotExist(statErr)).To(BeTrue(), "a rejected path must not be created")
		}
	})

	It("self-heals a corrupted materialization cache", func() {
		runner, err := agent.NewDockerComposeRunner(testDockerCLI.Configured, workspace, revisionRoot, testRevisionLimits(), true)
		Expect(err).NotTo(HaveOccurred())
		assignment := testAssignment("notes", "candace-notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_RUNNING)
		assignment.SourceRevision, assignment.ContentSha256 = sourceRevision, sourceDigest
		plan, err := runner.Plan(context.Background(), assignment)
		Expect(err).NotTo(HaveOccurred())
		staged := argumentAfter(plan.Commands[1].Argv, "--project-directory")
		compose := filepath.Join(staged, "compose.yaml")
		Expect(os.Chmod(compose, 0o600)).To(Succeed())
		Expect(os.WriteFile(compose, []byte("corrupted\n"), 0o600)).To(Succeed())

		repaired, err := runner.Plan(context.Background(), assignment)

		Expect(err).NotTo(HaveOccurred())
		Expect(argumentAfter(repaired.Commands[1].Argv, "--project-directory")).To(Equal(staged))
		contents, err := os.ReadFile(compose)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(contents)).To(Equal("services: {}\n"))
		info, err := os.Stat(compose)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm() & 0o222).To(BeZero())
	})

	It("does not share a cache path between identical subtrees in one commit", func() {
		for _, name := range []string{"first", "second"} {
			Expect(os.Mkdir(filepath.Join(workspace, name), 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(workspace, name, "compose.yaml"), []byte("services: {}\n"), 0o644)).To(Succeed())
		}
		runAgentGit(workspace, "add", "first", "second")
		runAgentGit(workspace, "commit", "-q", "-m", "test: identical app subtrees")
		revision := runAgentGit(workspace, "rev-parse", "HEAD")
		firstDigest, err := candaceos.DigestAppSource(context.Background(), filepath.Join(workspace, "first"))
		Expect(err).NotTo(HaveOccurred())
		secondDigest, err := candaceos.DigestAppSource(context.Background(), filepath.Join(workspace, "second"))
		Expect(err).NotTo(HaveOccurred())
		Expect(firstDigest).To(Equal(secondDigest))
		runner, err := agent.NewDockerComposeRunner(testDockerCLI.Configured, workspace, revisionRoot, testRevisionLimits(), true)
		Expect(err).NotTo(HaveOccurred())
		first := testAssignment("first", "first", "first", candaceosv1.DesiredState_DESIRED_STATE_RUNNING)
		first.SourceRevision, first.ContentSha256 = revision, firstDigest
		second := testAssignment("second", "second", "second", candaceosv1.DesiredState_DESIRED_STATE_RUNNING)
		second.SourceRevision, second.ContentSha256 = revision, secondDigest

		firstPlan, err := runner.Plan(context.Background(), first)
		Expect(err).NotTo(HaveOccurred())
		secondPlan, err := runner.Plan(context.Background(), second)
		Expect(err).NotTo(HaveOccurred())

		Expect(argumentAfter(firstPlan.Commands[1].Argv, "--project-directory")).NotTo(Equal(
			argumentAfter(secondPlan.Commands[1].Argv, "--project-directory"),
		))
	})

	It("self-heals a cache whose executable bit was changed", func() {
		runner, err := agent.NewDockerComposeRunner(testDockerCLI.Configured, workspace, revisionRoot, testRevisionLimits(), true)
		Expect(err).NotTo(HaveOccurred())
		assignment := testAssignment("notes", "candace-notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_RUNNING)
		assignment.SourceRevision, assignment.ContentSha256 = sourceRevision, sourceDigest
		plan, err := runner.Plan(context.Background(), assignment)
		Expect(err).NotTo(HaveOccurred())
		compose := filepath.Join(argumentAfter(plan.Commands[1].Argv, "--project-directory"), "compose.yaml")
		Expect(os.Chmod(compose, 0o555)).To(Succeed())

		_, err = runner.Plan(context.Background(), assignment)

		Expect(err).NotTo(HaveOccurred())
		info, err := os.Stat(compose)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o444)))
	})

	It("fails closed without overshooting the revision byte quota", func() {
		Expect(os.Mkdir(filepath.Join(workspace, "large"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workspace, "large", "compose.yaml"), []byte("services:\n  large:\n    image: busybox\n"), 0o644)).To(Succeed())
		runAgentGit(workspace, "add", "large")
		runAgentGit(workspace, "commit", "-q", "-m", "test: second app")
		revision := runAgentGit(workspace, "rev-parse", "HEAD")
		notesDigest, err := candaceos.DigestAppSource(context.Background(), filepath.Join(workspace, "notes"))
		Expect(err).NotTo(HaveOccurred())
		largeDigest, err := candaceos.DigestAppSource(context.Background(), filepath.Join(workspace, "large"))
		Expect(err).NotTo(HaveOccurred())
		runner, err := agent.NewDockerComposeRunner(
			testDockerCLI.Configured, workspace, revisionRoot,
			&candaceosv1.RevisionLimits{MaxEntries: 4, MaxBytes: 20}, true,
		)
		Expect(err).NotTo(HaveOccurred())
		notes := testAssignment("notes", "notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_RUNNING)
		notes.SourceRevision, notes.ContentSha256 = revision, notesDigest
		large := testAssignment("large", "large", "large", candaceosv1.DesiredState_DESIRED_STATE_RUNNING)
		large.SourceRevision, large.ContentSha256 = revision, largeDigest
		_, err = runner.Plan(context.Background(), notes)
		Expect(err).NotTo(HaveOccurred())

		_, err = runner.Plan(context.Background(), large)

		Expect(err).To(MatchError(ContainSubstring("cache bytes remaining")))
		entries, readErr := os.ReadDir(revisionRoot)
		Expect(readErr).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1), "failed staging directories must be removed")
	})

	It("reuses an existing snapshot but rejects a new one at the entry quota", func() {
		Expect(os.Mkdir(filepath.Join(workspace, "second"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workspace, "second", "compose.yaml"), []byte("services: {}\n"), 0o644)).To(Succeed())
		runAgentGit(workspace, "add", "second")
		runAgentGit(workspace, "commit", "-q", "-m", "test: quota app")
		revision := runAgentGit(workspace, "rev-parse", "HEAD")
		notesDigest, err := candaceos.DigestAppSource(context.Background(), filepath.Join(workspace, "notes"))
		Expect(err).NotTo(HaveOccurred())
		secondDigest, err := candaceos.DigestAppSource(context.Background(), filepath.Join(workspace, "second"))
		Expect(err).NotTo(HaveOccurred())
		runner, err := agent.NewDockerComposeRunner(
			testDockerCLI.Configured, workspace, revisionRoot,
			&candaceosv1.RevisionLimits{MaxEntries: 1, MaxBytes: 1 << 20}, true,
		)
		Expect(err).NotTo(HaveOccurred())
		notes := testAssignment("notes", "notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_RUNNING)
		notes.SourceRevision, notes.ContentSha256 = revision, notesDigest
		second := testAssignment("second", "second", "second", candaceosv1.DesiredState_DESIRED_STATE_RUNNING)
		second.SourceRevision, second.ContentSha256 = revision, secondDigest

		firstPlan, err := runner.Plan(context.Background(), notes)
		Expect(err).NotTo(HaveOccurred())
		reusedPlan, err := runner.Plan(context.Background(), notes)
		Expect(err).NotTo(HaveOccurred())
		_, secondErr := runner.Plan(context.Background(), second)

		Expect(argumentAfter(reusedPlan.Commands[1].Argv, "--project-directory")).To(Equal(
			argumentAfter(firstPlan.Commands[1].Argv, "--project-directory"),
		))
		Expect(secondErr).To(MatchError(ContainSubstring("revision cache capacity exhausted")))
	})
})

var _ = Describe("Fenced reconciliation", func() {
	It("isolates caller, executor, store, response, and snapshot ownership", func() {
		controller := gomock.NewController(GinkgoT())
		loaded := agent.Snapshot{Fence: &candaceosv1.Fence{Term: 2, LeaderId: "warden-a"}}
		var saved agent.Snapshot
		store := NewMockIStore(controller)
		store.EXPECT().Load().Return(loaded, true, nil)
		store.EXPECT().Save(gomock.Any()).DoAndReturn(func(snapshot agent.Snapshot) error {
			saved = snapshot
			return nil
		}).Times(2)
		executor := NewMockIExecutor(controller)
		executor.EXPECT().Plan(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, assignment *candaceosv1.Assignment) (agent.Plan, error) {
				assignment.App = "executor-mutated"
				return stubPlan(), nil
			},
		)
		executor.EXPECT().DryRun().Return(true)
		executor.EXPECT().Execute(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, plan agent.Plan) error {
				plan.Commands[0].Argv[0] = "executor-mutated"
				return nil
			},
		)
		reconciler, err := agent.NewReconciler(store, executor)
		Expect(err).NotTo(HaveOccurred())

		loaded.Fence.Term = 99
		Expect(reconciler.Snapshot().Fence.GetTerm()).To(Equal(uint64(2)))
		request := &candaceosv1.ReconcileRequest{
			Fence:      &candaceosv1.Fence{Term: 3, LeaderId: "warden-a"},
			Assignment: testAssignment("notes", "notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_RUNNING),
		}
		response, err := reconciler.Reconcile(context.Background(), request)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.GetAssignment().GetApp()).To(Equal("notes"))
		Expect(response.GetCommands()[0].GetArgv()[0]).To(Equal(testDockerCLI.Configured))

		request.Fence.Term = 100
		request.Assignment.App = "caller-mutated"
		Expect(response.GetFence().GetTerm()).To(Equal(uint64(3)))
		Expect(response.GetAssignment().GetApp()).To(Equal("notes"))
		Expect(saved.Fence.GetTerm()).To(Equal(uint64(3)))
		Expect(saved.Assignment.GetApp()).To(Equal("notes"))
		response.Fence.Term = 101
		response.Assignment.App = "response-mutated"
		response.Commands[0].Argv[0] = "response-mutated"
		saved.Fence.Term = 102
		saved.Assignment.App = "store-mutated"
		saved.Commands[0].Argv[0] = "store-mutated"

		snapshot := reconciler.Snapshot()
		Expect(snapshot.Fence.GetTerm()).To(Equal(uint64(3)))
		Expect(snapshot.Assignment.GetApp()).To(Equal("notes"))
		Expect(snapshot.Commands[0].Argv[0]).To(Equal(testDockerCLI.Configured))
		snapshot.Fence.Term = 103
		snapshot.Assignment.App = "snapshot-mutated"
		snapshot.Commands[0].Argv[0] = "snapshot-mutated"
		isolated := reconciler.Snapshot()
		Expect(isolated.Fence.GetTerm()).To(Equal(uint64(3)))
		Expect(isolated.Assignment.GetApp()).To(Equal("notes"))
		Expect(isolated.Commands[0].Argv[0]).To(Equal(testDockerCLI.Configured))
	})

	It("does not publish a fence that failed its durable write", func() {
		controller := gomock.NewController(GinkgoT())
		store := NewMockIStore(controller)
		store.EXPECT().Load().Return(
			agent.Snapshot{Fence: &candaceosv1.Fence{Term: 4, LeaderId: "warden-a"}}, true, nil,
		)
		store.EXPECT().Save(gomock.Any()).Return(errors.New("simulated durable write failure"))
		executor := NewMockIExecutor(controller)
		reconciler, err := agent.NewReconciler(store, executor)
		Expect(err).NotTo(HaveOccurred())
		request := &candaceosv1.ReconcileRequest{
			Fence:      &candaceosv1.Fence{Term: 5, LeaderId: "warden-b"},
			Assignment: testAssignment("notes", "notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_RUNNING),
		}

		_, err = reconciler.Reconcile(context.Background(), request)

		Expect(err).To(MatchError(And(
			ContainSubstring("agent state persistence failed"),
			ContainSubstring("simulated durable write failure"),
		)))
		Expect(reconciler.Snapshot().Fence.GetTerm()).To(Equal(uint64(4)))
		Expect(reconciler.Snapshot().Fence.GetLeaderId()).To(Equal("warden-a"))
	})

	It("retains the last durable assignment when the completed write fails", func() {
		previous := testAssignment("previous", "previous", "previous", candaceosv1.DesiredState_DESIRED_STATE_RUNNING)
		controller := gomock.NewController(GinkgoT())
		store := NewMockIStore(controller)
		store.EXPECT().Load().Return(agent.Snapshot{
			Fence:      &candaceosv1.Fence{Term: 4, LeaderId: "warden-a"},
			Assignment: previous,
		}, true, nil)
		gomock.InOrder(
			store.EXPECT().Save(gomock.Any()).Return(nil),
			store.EXPECT().Save(gomock.Any()).Return(errors.New("simulated completed-state failure")),
		)
		executor := NewMockIExecutor(controller)
		executor.EXPECT().Plan(gomock.Any(), gomock.Any()).Return(stubPlan(), nil)
		executor.EXPECT().DryRun().Return(true)
		executor.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(nil)
		reconciler, err := agent.NewReconciler(store, executor)
		Expect(err).NotTo(HaveOccurred())
		request := &candaceosv1.ReconcileRequest{
			Fence:      &candaceosv1.Fence{Term: 5, LeaderId: "warden-b"},
			Assignment: testAssignment("notes", "notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_RUNNING),
		}

		_, err = reconciler.Reconcile(context.Background(), request)

		Expect(err).To(MatchError(And(
			ContainSubstring("agent state persistence failed"),
			ContainSubstring("simulated completed-state failure"),
		)))
		snapshot := reconciler.Snapshot()
		Expect(snapshot.Fence.GetTerm()).To(Equal(uint64(5)))
		Expect(snapshot.Fence.GetLeaderId()).To(Equal("warden-b"))
		Expect(snapshot.Assignment.GetApp()).To(Equal("previous"))
	})

	It("durably accepts a new fence before attempting Compose", func() {
		store := &agent.MemoryStore{}
		executor := NewMockIExecutor(gomock.NewController(GinkgoT()))
		executor.EXPECT().Plan(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, assignment *candaceosv1.Assignment) (agent.Plan, error) {
				persisted, ok, err := store.Load()
				Expect(err).NotTo(HaveOccurred())
				Expect(ok).To(BeTrue())
				Expect(persisted.Fence.GetTerm()).To(Equal(uint64(8)))
				Expect(persisted.Fence.GetLeaderId()).To(Equal("warden-b"))
				return stubPlan(), nil
			},
		)
		executor.EXPECT().DryRun().Return(true)
		executor.EXPECT().Execute(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, plan agent.Plan) error {
				persisted, ok, err := store.Load()
				Expect(err).NotTo(HaveOccurred())
				Expect(ok).To(BeTrue())
				Expect(persisted.Fence.GetTerm()).To(Equal(uint64(8)))
				Expect(persisted.Fence.GetLeaderId()).To(Equal("warden-b"))
				return errors.New("simulated Compose failure")
			},
		)
		reconciler, err := agent.NewReconciler(store, executor)
		Expect(err).NotTo(HaveOccurred())
		request := &candaceosv1.ReconcileRequest{
			Fence:      &candaceosv1.Fence{Term: 8, LeaderId: "warden-b"},
			Assignment: testAssignment("notes", "notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_RUNNING),
		}
		_, err = reconciler.Reconcile(context.Background(), request)
		Expect(err).To(MatchError(ContainSubstring("simulated Compose failure")))
		Expect(errors.Is(err, agent.ErrExecution)).To(BeTrue())

		restartedExecutor := NewMockIExecutor(gomock.NewController(GinkgoT()))
		restarted, err := agent.NewReconciler(store, restartedExecutor)
		Expect(err).NotTo(HaveOccurred())
		request.Fence.Term = 7
		_, err = restarted.Reconcile(context.Background(), request)
		Expect(err).To(MatchError(ContainSubstring("stale leader fence")))
	})

	It("rejects stale and conflicting fences before planning", func() {
		store := &agent.MemoryStore{}
		Expect(store.Save(agent.Snapshot{Fence: &candaceosv1.Fence{Term: 4, LeaderId: "warden-a"}})).To(Succeed())
		executor := NewMockIExecutor(gomock.NewController(GinkgoT()))
		reconciler, err := agent.NewReconciler(store, executor)
		Expect(err).NotTo(HaveOccurred())
		assignment := testAssignment("notes", "notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_STOPPED)

		_, staleErr := reconciler.Reconcile(context.Background(), &candaceosv1.ReconcileRequest{
			Fence: &candaceosv1.Fence{Term: 3, LeaderId: "warden-a"}, Assignment: assignment,
		})
		_, conflictErr := reconciler.Reconcile(context.Background(), &candaceosv1.ReconcileRequest{
			Fence: &candaceosv1.Fence{Term: 4, LeaderId: "warden-b"}, Assignment: assignment,
		})

		Expect(staleErr).To(MatchError(ContainSubstring("stale leader fence")))
		Expect(conflictErr).To(MatchError(ContainSubstring("conflicting leader fence")))
	})

	It("persists the highest term and rejects stale or split-brain requests after restart", func() {
		workspace := GinkgoT().TempDir()
		Expect(os.Mkdir(filepath.Join(workspace, "notes"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workspace, "notes", "compose.yaml"), []byte("services: {}\n"), 0o600)).To(Succeed())
		sourceRevision, sourceDigest := commitAgentWorkspace(workspace)
		composeProcess := NewMockIComposeProcessExecutor(gomock.NewController(GinkgoT()))
		composeProcess.EXPECT().Run(gomock.Any(), gomock.Any()).Return("", nil)
		revisionRoot := GinkgoT().TempDir()
		DeferCleanup(removeSealedAgentTestTree, revisionRoot)
		runner, err := agent.NewDockerComposeRunner(
			testDockerCLI.Configured, workspace, revisionRoot, testRevisionLimits(), true,
			agent.WithComposeProcessExecutor(composeProcess),
		)
		Expect(err).NotTo(HaveOccurred())
		statePath := filepath.Join(GinkgoT().TempDir(), "state.json")
		store := agent.NewFileStore(statePath)
		reconciler, err := agent.NewReconciler(store, runner)
		Expect(err).NotTo(HaveOccurred())

		assignment := testAssignment("notes", "candace-notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_RUNNING)
		assignment.SourceRevision, assignment.ContentSha256 = sourceRevision, sourceDigest
		result, err := reconciler.Reconcile(context.Background(), &candaceosv1.ReconcileRequest{Fence: &candaceosv1.Fence{Term: 4, LeaderId: "warden-a"}, Assignment: assignment})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.GetDryRun()).To(BeTrue())
		Expect(result.GetCommands()).To(HaveLen(2))

		restarted, err := agent.NewReconciler(store, runner)
		Expect(err).NotTo(HaveOccurred())
		_, err = restarted.Reconcile(context.Background(), &candaceosv1.ReconcileRequest{Fence: &candaceosv1.Fence{Term: 3, LeaderId: "warden-a"}, Assignment: assignment})
		Expect(err).To(MatchError(ContainSubstring("stale leader fence")))
		_, err = restarted.Reconcile(context.Background(), &candaceosv1.ReconcileRequest{Fence: &candaceosv1.Fence{Term: 4, LeaderId: "warden-b"}, Assignment: assignment})
		Expect(err).To(MatchError(ContainSubstring("conflicting leader fence")))

		entries, err := os.ReadDir(filepath.Dir(statePath))
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1), "atomic saves must not leave temporary files")
	})

	It("accepts an idempotent retry from the same leader and term", func() {
		workspace := GinkgoT().TempDir()
		Expect(os.Mkdir(filepath.Join(workspace, "notes"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workspace, "notes", "compose.yaml"), []byte("services: {}\n"), 0o600)).To(Succeed())
		composeProcess := NewMockIComposeProcessExecutor(gomock.NewController(GinkgoT()))
		composeProcess.EXPECT().Run(gomock.Any(), gomock.Any()).Return("", nil).Times(2)
		revisionRoot := GinkgoT().TempDir()
		DeferCleanup(removeSealedAgentTestTree, revisionRoot)
		runner, err := agent.NewDockerComposeRunner(
			testDockerCLI.Configured, workspace, revisionRoot, testRevisionLimits(), true,
			agent.WithComposeProcessExecutor(composeProcess),
		)
		Expect(err).NotTo(HaveOccurred())
		reconciler, err := agent.NewReconciler(&agent.MemoryStore{}, runner)
		Expect(err).NotTo(HaveOccurred())
		request := &candaceosv1.ReconcileRequest{
			Fence:      &candaceosv1.Fence{Term: 1, LeaderId: "warden-a"},
			Assignment: testAssignment("notes", "notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_STOPPED),
		}
		_, err = reconciler.Reconcile(context.Background(), request)
		Expect(err).NotTo(HaveOccurred())
		_, err = reconciler.Reconcile(context.Background(), request)
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("FileStore compatibility", func() {
	It("adopts a legacy snapshot by retaining its fence and clearing its unbound assignment", func() {
		statePath := filepath.Join(GinkgoT().TempDir(), "state.json")
		legacy := `{"fence":{"term":4,"leader_id":"warden-a"},"assignment":{"app":"notes","project":"candace-notes","path":"notes","desired_state":"running"},"commands":[{"argv":["docker","compose","up","-d"]}],"updated_at":"2026-08-18T12:00:00Z"}` + "\n"
		Expect(os.WriteFile(statePath, []byte(legacy), 0o600)).To(Succeed())
		store := agent.NewFileStore(statePath)

		snapshot, ok, err := store.Load()

		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(snapshot.Fence.GetTerm()).To(Equal(uint64(4)))
		Expect(snapshot.Fence.GetLeaderId()).To(Equal("warden-a"))
		Expect(snapshot.Assignment).To(BeNil())
		Expect(snapshot.Commands).To(BeEmpty())
		Expect(snapshot.UpdatedAt).To(BeZero())
		reloaded, ok, err := store.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(reloaded).To(Equal(snapshot), "legacy adoption must be persisted atomically")
		persisted, err := os.ReadFile(statePath)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(persisted)).NotTo(ContainSubstring(`"assignment"`))
	})

	It("round-trips the established durable JSON field names and values", func() {
		statePath := filepath.Join(GinkgoT().TempDir(), "state.json")
		store := agent.NewFileStore(statePath)
		updatedAt := time.Date(2026, time.August, 19, 12, 34, 56, 0, time.UTC)
		snapshot := agent.Snapshot{
			Fence: &candaceosv1.Fence{Term: 7, LeaderId: "warden-b"},
			Assignment: testAssignment(
				"notes", "candace-notes", "notes",
				candaceosv1.DesiredState_DESIRED_STATE_RUNNING,
			),
			Commands:  []agent.Command{{Argv: []string{"docker", "compose", "up", "-d"}}},
			UpdatedAt: updatedAt,
		}

		Expect(store.Save(snapshot)).To(Succeed())
		persisted, err := os.ReadFile(statePath)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(persisted)).To(MatchJSON(`{
			"fence":{"term":7,"leader_id":"warden-b"},
			"assignment":{"app":"notes","project":"candace-notes","path":"notes","desired_state":"running","source_revision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","content_sha256":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
			"commands":[{"argv":["docker","compose","up","-d"]}],
			"updated_at":"2026-08-19T12:34:56Z"
		}`))

		restored, found, err := store.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(restored.Fence.GetLeaderId()).To(Equal("warden-b"))
		Expect(restored.Assignment.GetDesiredState()).To(Equal(candaceosv1.DesiredState_DESIRED_STATE_RUNNING))
		Expect(restored.UpdatedAt).To(Equal(updatedAt))
	})

	It("loads a pre-fence snapshot without inventing fence presence", func() {
		statePath := filepath.Join(GinkgoT().TempDir(), "state.json")
		Expect(os.WriteFile(statePath, []byte("{\"fence\":null}\n"), 0o600)).To(Succeed())

		snapshot, found, err := agent.NewFileStore(statePath).Load()

		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(snapshot.Fence).To(BeNil())
	})
})

func stubPlan() agent.Plan {
	return agent.Plan{Commands: []agent.Command{
		{Argv: []string{testDockerCLI.Configured, "compose", "config", "--quiet"}},
		{Argv: []string{testDockerCLI.Configured, "compose", "up", "-d"}},
	}}
}

func argumentAfter(argv []string, name string) string {
	for index := 0; index+1 < len(argv); index++ {
		if argv[index] == name {
			return argv[index+1]
		}
	}
	Fail("missing argument " + name)
	return ""
}
