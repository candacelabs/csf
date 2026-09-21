package reconcile

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos"
)

func TestReconcile(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CandaceOS Reconcile Suite")
}

func validReconcileInput() *candaceosv1.ReconcileIntent {
	return &candaceosv1.ReconcileIntent{
		App:           "hello",
		Project:       "hello",
		Path:          "apps/hello",
		DesiredState:  candaceosv1.DesiredState_DESIRED_STATE_RUNNING,
		PlacementMode: candaceosv1.PlacementMode_PLACEMENT_MODE_EXACT_NODE,
		NodeId:        "node-a",
	}
}

func validWireRevision() candaceos.AppRevision {
	return candaceos.AppRevision{
		Revision: strings.Repeat("a", 40),
		Digest:   "sha256:" + strings.Repeat("b", 64),
	}
}

func runIntegrationGit(ctx context.Context, workspace string, args ...string) string {
	commandArgs := append([]string{"-C", workspace}, args...)
	output, err := exec.CommandContext(ctx, "git", commandArgs...).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "git %s failed: %s", strings.Join(args, " "), output)
	return strings.TrimSpace(string(output))
}

func writeTestFile(path, contents string) {
	Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
	Expect(os.WriteFile(path, []byte(contents), 0o644)).To(Succeed())
}

var _ = Describe("repository source sanitization", func() {
	DescribeTable("strips standard URL userinfo without rewriting other Git references",
		func(source, expected string) {
			Expect(stripURLUserinfo(source)).To(Equal(expected))
		},
		Entry("HTTPS username and password",
			"https://test-user:test-password@example.invalid/candace/hello.git",
			"https://example.invalid/candace/hello.git"),
		Entry("SSH username",
			"ssh://git@example.invalid/candace/hello.git",
			"ssh://example.invalid/candace/hello.git"),
		Entry("SCP-style remote",
			"git@example.invalid:candace/hello.git",
			"git@example.invalid:candace/hello.git"),
	)
})

var _ = Describe("typed reconciliation boundaries", func() {
	Describe("assignment mapping", func() {
		DescribeTable("maps desired state onto the validated wire contract",
			func(desiredState candaceosv1.DesiredState, expected candaceos.DesiredState) {
				input := validReconcileInput()
				input.DesiredState = desiredState

				assignment, state, err := assignmentFrom(input, validWireRevision())

				Expect(err).NotTo(HaveOccurred())
				Expect(state).To(Equal(expected))
				Expect(assignment.GetApp()).To(Equal(input.GetApp()))
				Expect(assignment.GetProject()).To(Equal(input.GetProject()))
				Expect(assignment.GetPath()).To(Equal(input.GetPath()))
				Expect(assignment.GetSourceRevision()).To(Equal(validWireRevision().Revision))
				Expect(assignment.GetContentSha256()).To(Equal(validWireRevision().Digest))
			},
			Entry("running", candaceosv1.DesiredState_DESIRED_STATE_RUNNING, candaceos.DesiredStateRunning),
			Entry("stopped", candaceosv1.DesiredState_DESIRED_STATE_STOPPED, candaceos.DesiredStateStopped),
		)

		DescribeTable("rejects paths that are not clean workspace-relative slash paths",
			func(path string) {
				input := validReconcileInput()
				input.Path = path
				_, _, err := assignmentFrom(input, validWireRevision())
				Expect(err).To(MatchError(ContainSubstring("candace.candaceos.v1.Assignment.path")))
			},
			Entry("parent traversal", "../outside"),
			Entry("absolute path", "/srv/app"),
			Entry("backslash", `apps\hello`),
			Entry("unclean path", "apps/../hello"),
			Entry("invalid UTF-8", string([]byte{0xff})),
		)

		It("rejects an unsupported desired-state enum before building the assignment", func() {
			input := validReconcileInput()
			input.DesiredState = candaceosv1.DesiredState(99)

			_, _, err := assignmentFrom(input, validWireRevision())

			Expect(err).To(MatchError(ContainSubstring("desired_state must be")))
		})
	})

	Describe("placement mapping", func() {
		It("maps exact-node placement to the persisted node mode", func() {
			input := validReconcileInput()

			placement, mode, nodeID, replicas, err := placementFrom(input)

			Expect(err).NotTo(HaveOccurred())
			Expect(placement.ExactNode).To(Equal(&candaceos.ExactNodePlacement{NodeID: "node-a"}))
			Expect(placement.Leader).To(BeNil())
			Expect(placement.MatchLabels).To(BeNil())
			Expect(mode).To(Equal("node"))
			Expect(nodeID).To(Equal("node-a"))
			Expect(replicas).To(Equal(1))
		})

		It("maps leader placement without an exact node", func() {
			input := validReconcileInput()
			input.PlacementMode = candaceosv1.PlacementMode_PLACEMENT_MODE_LEADER
			input.NodeId = ""

			placement, mode, nodeID, replicas, err := placementFrom(input)

			Expect(err).NotTo(HaveOccurred())
			Expect(placement.Leader).NotTo(BeNil())
			Expect(placement.ExactNode).To(BeNil())
			Expect(mode).To(Equal("leader"))
			Expect(nodeID).To(BeEmpty())
			Expect(replicas).To(Equal(1))
		})

		It("maps and copies label placement", func() {
			input := validReconcileInput()
			input.PlacementMode = candaceosv1.PlacementMode_PLACEMENT_MODE_LABELS
			input.NodeId = ""
			input.Labels = map[string]string{"gpu": "nvidia", "region": "west"}
			input.Replicas = 2

			placement, mode, nodeID, replicas, err := placementFrom(input)
			input.Labels["gpu"] = "changed-after-mapping"

			Expect(err).NotTo(HaveOccurred())
			Expect(placement.MatchLabels).To(Equal(&candaceos.MatchLabelsPlacement{
				Labels:   map[string]string{"gpu": "nvidia", "region": "west"},
				Replicas: 2,
			}))
			Expect(mode).To(Equal("labels"))
			Expect(nodeID).To(BeEmpty())
			Expect(replicas).To(Equal(2))
		})

		DescribeTable("rejects stateful workloads unless placement is exact-node",
			func(mode candaceosv1.PlacementMode, configure func(input *candaceosv1.ReconcileIntent)) {
				input := validReconcileInput()
				input.PlacementMode = mode
				input.Stateful = true
				configure(input)

				placement, _, _, _, err := placementFrom(input)
				Expect(err).NotTo(HaveOccurred())
				deployment := candaceos.Deployment{
					ID:            input.GetProject(),
					AppRevisionID: "hello-revision",
					DesiredState:  candaceos.DesiredStateRunning,
					Placement:     placement,
					Stateful:      input.GetStateful(),
				}
				err = deployment.Validate()
				Expect(errors.Is(err, candaceos.ErrInvalidDeployment)).To(BeTrue())
				Expect(err).To(MatchError(ContainSubstring("stateful workloads require exact_node")))
			},
			Entry("leader", candaceosv1.PlacementMode_PLACEMENT_MODE_LEADER, func(input *candaceosv1.ReconcileIntent) { input.NodeId = "" }),
			Entry("labels", candaceosv1.PlacementMode_PLACEMENT_MODE_LABELS, func(input *candaceosv1.ReconcileIntent) {
				input.NodeId = ""
				input.Labels = map[string]string{"disk": "ssd"}
				input.Replicas = 1
			}),
		)

		It("keeps stateful workloads pinned when mapping exact-node placement", func() {
			input := validReconcileInput()
			input.Stateful = true

			placement, _, _, _, err := placementFrom(input)
			Expect(err).NotTo(HaveOccurred())
			deployment := candaceos.Deployment{
				ID:            input.GetProject(),
				AppRevisionID: "hello-revision",
				DesiredState:  candaceos.DesiredStateRunning,
				Placement:     placement,
				Stateful:      input.GetStateful(),
			}

			Expect(deployment.Validate()).To(Succeed())
		})

		It("rejects unknown placement modes", func() {
			input := validReconcileInput()
			input.PlacementMode = candaceosv1.PlacementMode(99)
			_, _, _, _, err := placementFrom(input)
			Expect(err).To(MatchError("placement_mode must be exact_node, leader, or labels"))
		})
	})
})

var _ = Describe("immutable application revisions", func() {
	It("rejects a symlink that escapes the configured workspace", func() {
		workspace := GinkgoT().TempDir()
		outside := GinkgoT().TempDir()
		Expect(os.Symlink(outside, filepath.Join(workspace, "escape"))).To(Succeed())

		_, err := resolveInside(workspace, "escape")

		Expect(err).To(MatchError("app path escapes the configured workspace"))
	})

	It("refuses dirty app source before creating an immutable revision", func(ctx SpecContext) {
		workspace := GinkgoT().TempDir()
		writeTestFile(filepath.Join(workspace, "apps", "hello", "compose.yaml"), "services: {}\n")
		commands := 0
		service := &Service{
			workspace: workspace,
			runCommand: func(_ context.Context, name string, args ...string) ([]byte, error) {
				commands++
				Expect(name).To(Equal("git"))
				Expect(args).To(ContainElement("status"))
				return []byte(" M apps/hello/compose.yaml\n"), nil
			},
		}

		_, _, err := service.revision(ctx, "hello", "apps/hello")

		Expect(err).To(MatchError(`app source "apps/hello" has uncommitted changes; commit it before deployment`))
		Expect(commands).To(Equal(1))
	})

	It("produces a deterministic content digest independent of creation order", func(ctx SpecContext) {
		first := GinkgoT().TempDir()
		second := GinkgoT().TempDir()
		writeTestFile(filepath.Join(first, "nested", "b.txt"), "beta")
		writeTestFile(filepath.Join(first, "a.txt"), "alpha")
		writeTestFile(filepath.Join(second, "a.txt"), "alpha")
		writeTestFile(filepath.Join(second, "nested", "b.txt"), "beta")

		firstDigest, err := candaceos.DigestAppSource(ctx, first)
		Expect(err).NotTo(HaveOccurred())
		secondDigest, err := candaceos.DigestAppSource(ctx, second)
		Expect(err).NotTo(HaveOccurred())

		Expect(firstDigest).To(Equal("sha256:409ba33a17778fce17935e35cf33b500de3e9ccb78c99d8db5bd6e75c527c610"))
		Expect(secondDigest).To(Equal(firstDigest))
	})

	It("gives unchanged app content a new identity at a new source commit", func(ctx SpecContext) {
		workspace := GinkgoT().TempDir()
		writeTestFile(filepath.Join(workspace, "apps", "hello", "compose.yaml"), "services: {}\n")
		runIntegrationGit(ctx, workspace, "init", "-q", "-b", "main")
		runIntegrationGit(ctx, workspace, "config", "user.name", "CandaceOS Test")
		runIntegrationGit(ctx, workspace, "config", "user.email", "candaceos-test@example.invalid")
		runIntegrationGit(ctx, workspace, "add", "--", "apps/hello/compose.yaml")
		runIntegrationGit(ctx, workspace, "commit", "-q", "-m", "test: add hello app")
		service := &Service{
			workspace: workspace,
			runCommand: func(commandContext context.Context, name string, args ...string) ([]byte, error) {
				return exec.CommandContext(commandContext, name, args...).Output()
			},
		}

		first, _, err := service.revision(ctx, "hello", "apps/hello")
		Expect(err).NotTo(HaveOccurred())
		writeTestFile(filepath.Join(workspace, "unrelated.txt"), "new commit, unchanged app\n")
		runIntegrationGit(ctx, workspace, "add", "--", "unrelated.txt")
		runIntegrationGit(ctx, workspace, "commit", "-q", "-m", "test: unrelated change")

		second, _, err := service.revision(ctx, "hello", "apps/hello")

		Expect(err).NotTo(HaveOccurred())
		Expect(second.Digest).To(Equal(first.Digest))
		Expect(second.Revision).NotTo(Equal(first.Revision))
		Expect(first.ID).To(Equal("hello-" + first.Revision))
		Expect(second.ID).To(Equal("hello-" + second.Revision))
		Expect(second.ID).NotTo(Equal(first.ID))
	})

	DescribeTable("enforces revision bounds before hashing an oversized source",
		func(files map[string]string, maxFiles int, maxBytes int64, message string) {
			root := GinkgoT().TempDir()
			for name, contents := range files {
				writeTestFile(filepath.Join(root, name), contents)
			}

			_, err := candaceos.DigestAppSourceWithLimits(context.Background(), root, &candaceosv1.AppSourceLimits{
				MaxFiles: int64(maxFiles),
				MaxBytes: maxBytes,
			})

			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("file count", map[string]string{"a": "1", "b": "2"}, 1, int64(10), "1-file or 10-byte revision limit"),
		Entry("byte count", map[string]string{"a": "12345"}, 1, int64(4), "1-file or 4-byte revision limit"),
	)

	It("accepts a source exactly at both revision bounds", func(ctx SpecContext) {
		root := GinkgoT().TempDir()
		writeTestFile(filepath.Join(root, "a"), "1234")

		digest, err := candaceos.DigestAppSourceWithLimits(ctx, root, &candaceosv1.AppSourceLimits{
			MaxFiles: 1,
			MaxBytes: 4,
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(digest).To(MatchRegexp(`^sha256:[0-9a-f]{64}$`))
	})
})

var _ = Describe("deployment target convergence", func() {
	It("rejects a revision that changed after approval before dispatch", func(ctx SpecContext) {
		workspace := GinkgoT().TempDir()
		writeTestFile(filepath.Join(workspace, "apps", "hello", "compose.yaml"), "services: {}\n")
		runIntegrationGit(ctx, workspace, "init", "-q", "-b", "main")
		runIntegrationGit(ctx, workspace, "config", "user.name", "CandaceOS Test")
		runIntegrationGit(ctx, workspace, "config", "user.email", "candaceos-test@example.invalid")
		runIntegrationGit(ctx, workspace, "add", "--", "apps/hello/compose.yaml")
		runIntegrationGit(ctx, workspace, "commit", "-q", "-m", "test: first approved revision")
		service := &Service{
			workspace: workspace,
			runCommand: func(commandContext context.Context, name string, args ...string) ([]byte, error) {
				return exec.CommandContext(commandContext, name, args...).Output()
			},
		}
		input := validReconcileInput()
		_, revision, composePath, err := service.resolveInput(ctx, input)
		Expect(err).NotTo(HaveOccurred())
		expected := approvedRevision(revision, composePath)
		writeTestFile(filepath.Join(workspace, "apps", "hello", "compose.yaml"), "services: {hello: {image: busybox}}\n")
		runIntegrationGit(ctx, workspace, "add", "--", "apps/hello/compose.yaml")
		runIntegrationGit(ctx, workspace, "commit", "-q", "-m", "test: change approved revision")

		_, err = service.ReconcileApproved(ctx, input, expected)

		Expect(err).To(MatchError("app revision changed after approval"))
	})

	It("stops nodes removed from the placement while reconciling every current target", func() {
		runs := planDeploymentRuns(
			[]string{"node-a", "node-b"},
			[]*candaceosv1.Node{{Id: "node-b"}, {Id: "node-c"}},
		)

		Expect(runs).To(HaveLen(3))
		Expect([]string{runs[0].NodeID, runs[1].NodeID, runs[2].NodeID}).To(Equal([]string{"node-b", "node-c", "node-a"}))
		Expect([]candaceos.DesiredState{runs[0].DesiredState, runs[1].DesiredState, runs[2].DesiredState}).To(Equal([]candaceos.DesiredState{
			candaceos.DesiredStateRunning,
			candaceos.DesiredStateRunning,
			candaceos.DesiredStateStopped,
		}))
		Expect(runs[0].ID).NotTo(BeEmpty())
		Expect(runs[1].ID).NotTo(Equal(runs[0].ID))
		Expect(runs[2].ID).NotTo(Equal(runs[1].ID))
	})

	It("stops every active node for an explicit stop", func() {
		runs := planDeploymentRuns([]string{"node-a", "node-b"}, nil)

		Expect(runs).To(HaveLen(2))
		Expect([]string{runs[0].NodeID, runs[1].NodeID}).To(Equal([]string{"node-a", "node-b"}))
		Expect(runs[0].DesiredState).To(Equal(candaceos.DesiredStateStopped))
		Expect(runs[1].DesiredState).To(Equal(candaceos.DesiredStateStopped))
	})
})
