package agent_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/app/candaceos-agent/internal/agent"
	"github.com/candacelabs/csf/pkg/telemetry"
	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

var _ = Describe("source lifecycle telemetry", func() {
	It("records repository, synchronization, and materialization boundaries", func() {
		workspace := GinkgoT().TempDir()
		Expect(os.Mkdir(filepath.Join(workspace, "notes"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workspace, "notes", "compose.yaml"), []byte("services: {}\n"), 0o600)).To(Succeed())
		revision, digest := commitAgentWorkspace(workspace)
		remote := filepath.Join(GinkgoT().TempDir(), "source.git")
		runAgentGitCommand("clone", "--bare", workspace, remote)

		revisionRoot := GinkgoT().TempDir()
		DeferCleanup(removeSealedAgentTestTree, revisionRoot)
		var events bytes.Buffer
		logger, err := telemetry.NewJSONLLogger(&events, "candaceos-agent", "node-executor")
		Expect(err).NotTo(HaveOccurred())
		runner, err := agent.NewDockerComposeRunnerWithSourceSync(
			"docker", workspace, revisionRoot, testRevisionLimits(),
			&candaceosv1.SourceSync{
				Remote: remote, Repository: filepath.Join(GinkgoT().TempDir(), "cache.git"),
				FetchTimeoutNanoseconds: int64(5 * time.Second),
			}, logger, true,
		)
		Expect(err).NotTo(HaveOccurred())
		assignment := testAssignment("notes", "candace-notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_RUNNING)
		assignment.SourceRevision, assignment.ContentSha256 = revision, digest

		_, err = runner.Plan(context.Background(), assignment)
		Expect(err).NotTo(HaveOccurred())
		_, err = runner.Plan(context.Background(), assignment)
		Expect(err).NotTo(HaveOccurred())

		Expect(events.String()).To(And(
			ContainSubstring(`"event":"agent.source_repository.initialized"`),
			ContainSubstring(`"event":"agent.source_sync.started"`),
			ContainSubstring(`"event":"agent.source_sync.completed"`),
			ContainSubstring(`"event":"agent.source_materialization.installed"`),
			ContainSubstring(`"event":"agent.source_materialization.reused"`),
		))
	})

	DescribeTable("keeps arbitrary fetch stderr inside the node process",
		func(remote string) {
			const diagnostic = "Authorization: Bearer opaque-token; Cookie: private-cookie"
			err, events := planSourceFailure(remote, diagnostic)

			Expect(err).To(MatchError(And(
				ContainSubstring("fetching configured source remote"),
				Not(ContainSubstring("opaque-token")),
				Not(ContainSubstring("private-cookie")),
			)))
			Expect(events).To(And(
				ContainSubstring(`"event":"agent.source_sync.failed"`),
				ContainSubstring(`"operation":"fetch"`),
				Not(ContainSubstring("opaque-token")),
				Not(ContainSubstring("private-cookie")),
			))
		},
		Entry("a credential-bearing URL", "https://source-user:p%40ss@example.invalid/private/repository.git?access_token=a%2Fb#private"),
		Entry("an SCP-style remote", "git@example.invalid:private/repository.git"),
	)
})

func planSourceFailure(remote, diagnostic string) (error, string) {
	workspace := GinkgoT().TempDir()
	Expect(os.Mkdir(filepath.Join(workspace, "notes"), 0o755)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(workspace, "notes", "compose.yaml"), []byte("services: {}\n"), 0o600)).To(Succeed())
	revision, digest := commitAgentWorkspace(workspace)
	realGit, err := exec.LookPath("git")
	Expect(err).NotTo(HaveOccurred())
	gitFixtureDirectory := GinkgoT().TempDir()
	failingGitCLI := filepath.Join(gitFixtureDirectory, "git")
	Expect(os.WriteFile(failingGitCLI, []byte(`#!/bin/sh
for argument do
  if [ "$argument" = fetch ]; then
    printf '%s\n' "$CANDACEOS_TEST_GIT_DIAGNOSTIC" >&2
    exit 1
  fi
done
exec "$CANDACEOS_TEST_REAL_GIT" "$@"
`), 0o700)).To(Succeed())
	GinkgoT().Setenv("CANDACEOS_TEST_REAL_GIT", realGit)
	GinkgoT().Setenv("CANDACEOS_TEST_GIT_DIAGNOSTIC", diagnostic)
	GinkgoT().Setenv("PATH", gitFixtureDirectory+string(os.PathListSeparator)+os.Getenv("PATH"))

	revisionRoot := GinkgoT().TempDir()
	DeferCleanup(removeSealedAgentTestTree, revisionRoot)
	var events bytes.Buffer
	logger, err := telemetry.NewJSONLLogger(&events, "candaceos-agent", "node-executor")
	Expect(err).NotTo(HaveOccurred())
	runner, err := agent.NewDockerComposeRunnerWithSourceSync(
		"docker", workspace, revisionRoot, testRevisionLimits(),
		&candaceosv1.SourceSync{
			Remote: remote, Repository: filepath.Join(GinkgoT().TempDir(), "cache.git"),
			FetchTimeoutNanoseconds: int64(5 * time.Second),
		}, logger, true,
	)
	Expect(err).NotTo(HaveOccurred())
	assignment := testAssignment("notes", "candace-notes", "notes", candaceosv1.DesiredState_DESIRED_STATE_RUNNING)
	assignment.SourceRevision, assignment.ContentSha256 = revision, digest

	_, err = runner.Plan(context.Background(), assignment)
	return err, events.String()
}
