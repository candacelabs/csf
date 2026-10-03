package deploy_test

import (
	"errors"
	"testing"
	"time"

	deployv1 "github.com/candacelabs/csf/proto/candace/deploy/v1"
	"github.com/candacelabs/csf/services/deploy"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestDeploy(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Deploy Domain Suite")
}

const (
	gitRevision  = "0123456789abcdef0123456789abcdef01234567"
	sourceDigest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func exactDeployment(nodeID string) deploy.Deployment {
	return deploy.Deployment{
		ID:            "deployment-1",
		AppRevisionID: "revision-1",
		DesiredState:  deploy.DesiredStateRunning,
		Placement: deploy.Placement{
			ExactNode: &deploy.ExactNodePlacement{NodeID: nodeID},
		},
	}
}

func clusterNodes() []*deployv1.Node {
	return []*deployv1.Node{
		{Id: "node-c", Alive: true, Labels: map[string]string{"region": "west", "gpu": "nvidia"}},
		{Id: "node-dead", Alive: false, Labels: map[string]string{"region": "west", "gpu": "nvidia"}},
		{Id: "node-a", Alive: true, Labels: map[string]string{"region": "west", "gpu": "nvidia", "disk": "ssd"}},
		{Id: "node-b", Alive: true, Labels: map[string]string{"region": "east", "gpu": "nvidia"}},
	}
}

var _ = Describe("durable user-facing primitives", func() {
	Describe("Node contract", func() {
		It("accepts a stable node identity and labels", func() {
			snapshot := deploy.ClusterSnapshot{Nodes: []*deployv1.Node{{
				Id: "node-a", Alive: true, Labels: map[string]string{"gpu": "nvidia"},
			}}}
			Expect(snapshot.Validate()).To(Succeed())
		})

		DescribeTable("returns a recognizable, field-specific validation error",
			func(labels map[string]string) {
				snapshot := deploy.ClusterSnapshot{Nodes: []*deployv1.Node{{
					Id: "node-a", Labels: labels,
				}}}
				err := snapshot.Validate()
				Expect(errors.Is(err, deploy.ErrInvalidClusterSnapshot)).To(BeTrue())
				Expect(errors.Is(err, deploy.ErrInvalidNode)).To(BeTrue())
				Expect(err).To(MatchError(And(ContainSubstring("labels"), ContainSubstring("refinement violated"))))
			},
			Entry("for a malformed key", map[string]string{"bad key": "value"}),
			Entry("for an untrimmed value", map[string]string{"good-key": " value"}),
		)
	})

	Describe("AppRevision", func() {
		validRevision := func() deploy.AppRevision {
			return deploy.AppRevision{
				ID:          "revision-1",
				AppID:       "notes",
				Source:      "https://example.test/candace/notes.git",
				Revision:    gitRevision,
				Digest:      sourceDigest,
				ComposePath: "deploy/compose.yaml",
			}
		}

		It("accepts immutable source coordinates", func() {
			Expect(validRevision().Validate()).To(Succeed())
		})

		DescribeTable("rejects mutable or unsafe coordinates",
			func(mutate func(revision *deploy.AppRevision), message string) {
				revision := validRevision()
				mutate(&revision)
				err := revision.Validate()
				Expect(errors.Is(err, deploy.ErrInvalidAppRevision)).To(BeTrue())
				Expect(err).To(MatchError(ContainSubstring(message)))
			},
			Entry("a branch", func(revision *deploy.AppRevision) { revision.Revision = "main" }, "full lowercase"),
			Entry("a short object ID", func(revision *deploy.AppRevision) { revision.Revision = "0123456" }, "full lowercase"),
			Entry("a noncanonical digest", func(revision *deploy.AppRevision) { revision.Digest = "0123" }, "canonical sha256"),
			Entry("an absolute Compose path", func(revision *deploy.AppRevision) { revision.ComposePath = "/compose.yaml" }, "repository-relative"),
			Entry("a parent traversal", func(revision *deploy.AppRevision) { revision.ComposePath = "../compose.yaml" }, "repository-relative"),
			Entry("a non-Compose extension", func(revision *deploy.AppRevision) { revision.ComposePath = "README.md" }, ".yaml or .yml"),
		)
	})

	Describe("Deployment", func() {
		It("accepts each complete placement variant", func() {
			placements := []deploy.Placement{
				{ExactNode: &deploy.ExactNodePlacement{NodeID: "node-a"}},
				{Leader: &deploy.LeaderPlacement{}},
				{MatchLabels: &deploy.MatchLabelsPlacement{Labels: map[string]string{"gpu": "nvidia"}, Replicas: 2}},
			}
			for _, placement := range placements {
				deployment := exactDeployment("node-a")
				deployment.Placement = placement
				Expect(deployment.Validate()).To(Succeed())
			}
		})

		It("rejects ambiguous placement", func() {
			deployment := exactDeployment("node-a")
			deployment.Placement.Leader = &deploy.LeaderPlacement{}
			err := deployment.Validate()
			Expect(errors.Is(err, deploy.ErrInvalidDeployment)).To(BeTrue())
			Expect(errors.Is(err, deploy.ErrInvalidPlacement)).To(BeTrue())
			Expect(err).To(MatchError(ContainSubstring("exactly one")))
		})

		It("rejects zero label replicas", func() {
			deployment := exactDeployment("node-a")
			deployment.Placement = deploy.Placement{
				MatchLabels: &deploy.MatchLabelsPlacement{Labels: map[string]string{"gpu": "nvidia"}},
			}
			err := deployment.Validate()
			Expect(errors.Is(err, deploy.ErrInvalidPlacement)).To(BeTrue())
			Expect(err).To(MatchError(ContainSubstring("replicas must be at least 1")))
		})

		DescribeTable("pins stateful workloads to exact nodes",
			func(placement deploy.Placement) {
				deployment := exactDeployment("node-a")
				deployment.Stateful = true
				deployment.Placement = placement
				err := deployment.Validate()
				Expect(errors.Is(err, deploy.ErrInvalidDeployment)).To(BeTrue())
				Expect(err).To(MatchError(ContainSubstring("stateful workloads require exact_node")))
			},
			Entry("instead of following the leader", deploy.Placement{Leader: &deploy.LeaderPlacement{}}),
			Entry("instead of following labels", deploy.Placement{MatchLabels: &deploy.MatchLabelsPlacement{Labels: map[string]string{"disk": "ssd"}, Replicas: 1}}),
		)

		It("accepts exact-node stateful placement", func() {
			deployment := exactDeployment("node-a")
			deployment.Stateful = true
			Expect(deployment.Validate()).To(Succeed())
		})
	})

	Describe("Run", func() {
		var requestedAt, startedAt, finishedAt time.Time

		BeforeEach(func() {
			requestedAt = time.Unix(100, 0).UTC()
			startedAt = requestedAt.Add(time.Second)
			finishedAt = startedAt.Add(time.Second)
		})

		validRun := func(status deploy.RunStatus) deploy.Run {
			run := deploy.Run{
				ID:            "run-1",
				DeploymentID:  "deployment-1",
				AppRevisionID: "revision-1",
				NodeID:        "node-a",
				Status:        status,
				RequestedAt:   requestedAt,
			}
			switch status {
			case deploy.RunStatusRunning:
				run.StartedAt = &startedAt
			case deploy.RunStatusSucceeded, deploy.RunStatusFailed:
				run.StartedAt = &startedAt
				run.FinishedAt = &finishedAt
			case deploy.RunStatusCanceled:
				run.FinishedAt = &finishedAt
			}
			return run
		}

		It("accepts complete lifecycle states", func() {
			statuses := []deploy.RunStatus{
				deploy.RunStatusQueued,
				deploy.RunStatusAwaitingApproval,
				deploy.RunStatusRunning,
				deploy.RunStatusSucceeded,
				deploy.RunStatusFailed,
				deploy.RunStatusCanceled,
			}
			for _, status := range statuses {
				Expect(validRun(status).Validate()).To(Succeed())
			}
		})

		It("rejects impossible lifecycle timestamps", func() {
			run := validRun(deploy.RunStatusSucceeded)
			before := requestedAt.Add(-time.Second)
			run.FinishedAt = &before
			err := run.Validate()
			Expect(errors.Is(err, deploy.ErrInvalidRun)).To(BeTrue())
			Expect(err).To(MatchError(ContainSubstring("finished_at cannot precede requested_at")))
		})
	})

	Describe("Approval", func() {
		It("accepts pending and attributed terminal decisions", func() {
			requestedAt := time.Unix(100, 0).UTC()
			pending := deploy.Approval{
				ID: "approval-1", RunID: "run-1", Action: "deploy notes", Decision: deploy.ApprovalPending, RequestedAt: requestedAt,
			}
			Expect(pending.Validate()).To(Succeed())

			decidedAt := requestedAt.Add(time.Second)
			approved := pending
			approved.Decision = deploy.ApprovalApproved
			approved.DecidedAt = &decidedAt
			approved.DecidedBy = "operator"
			Expect(approved.Validate()).To(Succeed())
		})

		It("rejects an unattributed terminal decision", func() {
			requestedAt := time.Unix(100, 0).UTC()
			decidedAt := requestedAt.Add(time.Second)
			approval := deploy.Approval{
				ID: "approval-1", RunID: "run-1", Action: "deploy notes", Decision: deploy.ApprovalDenied,
				RequestedAt: requestedAt, DecidedAt: &decidedAt,
			}
			err := approval.Validate()
			Expect(errors.Is(err, deploy.ErrInvalidApproval)).To(BeTrue())
			Expect(err).To(MatchError(ContainSubstring("decided_by is required")))
		})
	})
})

var _ = Describe("deterministic placement", func() {
	It("sorts alive exact-label matches by stable node ID and returns full replicas", func() {
		deployment := exactDeployment("node-a")
		deployment.Placement = deploy.Placement{
			MatchLabels: &deploy.MatchLabelsPlacement{
				Labels:   map[string]string{"region": "west", "gpu": "nvidia"},
				Replicas: 2,
			},
		}
		nodes := clusterNodes()
		snapshot := deploy.ClusterSnapshot{Nodes: nodes, LeaderNodeID: "node-c", Authoritative: true, HasQuorum: true}

		selected, err := deploy.ResolvePlacement(deployment, snapshot)
		Expect(err).NotTo(HaveOccurred())
		Expect(selected).To(HaveLen(2))
		Expect([]string{selected[0].GetId(), selected[1].GetId()}).To(Equal([]string{"node-a", "node-c"}))
		Expect(nodes[0].GetId()).To(Equal("node-c"), "resolution must not reorder caller-owned snapshot data")

		selected[0].Labels["gpu"] = "mutated"
		Expect(nodes[2].Labels["gpu"]).To(Equal("nvidia"), "resolution must not alias caller-owned labels")
	})

	It("selects an alive exact node", func() {
		selected, err := deploy.ResolvePlacement(exactDeployment("node-b"), deploy.ClusterSnapshot{
			Nodes: clusterNodes(), LeaderNodeID: "node-c", Authoritative: true, HasQuorum: true,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(selected).To(HaveLen(1))
		Expect(selected[0].GetId()).To(Equal("node-b"))
	})

	It("selects only an alive elected leader", func() {
		deployment := exactDeployment("node-a")
		deployment.Placement = deploy.Placement{Leader: &deploy.LeaderPlacement{}}
		selected, err := deploy.ResolvePlacement(deployment, deploy.ClusterSnapshot{
			Nodes: clusterNodes(), LeaderNodeID: "node-c", Authoritative: true, HasQuorum: true,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(selected).To(HaveLen(1))
		Expect(selected[0].GetId()).To(Equal("node-c"))
	})

	It("fails closed without quorum", func() {
		_, err := deploy.ResolvePlacement(exactDeployment("node-a"), deploy.ClusterSnapshot{
			Nodes: clusterNodes(), LeaderNodeID: "node-c", Authoritative: true,
		})
		Expect(errors.Is(err, deploy.ErrNoQuorum)).To(BeTrue())
	})

	It("fails closed when Warden has no authoritative view", func() {
		_, err := deploy.ResolvePlacement(exactDeployment("node-a"), deploy.ClusterSnapshot{
			Nodes: clusterNodes(), LeaderNodeID: "node-c", HasQuorum: true,
		})
		Expect(errors.Is(err, deploy.ErrNotAuthoritative)).To(BeTrue())
	})

	It("fails closed when no alive leader gates placement", func() {
		_, err := deploy.ResolvePlacement(exactDeployment("node-a"), deploy.ClusterSnapshot{
			Nodes: clusterNodes(), LeaderNodeID: "node-dead", Authoritative: true, HasQuorum: true,
		})
		Expect(errors.Is(err, deploy.ErrLeaderUnavailable)).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("missing or not alive")))
	})

	It("does not require quorum to resolve stopped desired state to no targets", func() {
		deployment := exactDeployment("node-a")
		deployment.DesiredState = deploy.DesiredStateStopped
		selected, err := deploy.ResolvePlacement(deployment, deploy.ClusterSnapshot{Nodes: clusterNodes()})
		Expect(err).NotTo(HaveOccurred())
		Expect(selected).To(BeEmpty())
	})

	DescribeTable("fails instead of returning partial or unsafe placement",
		func(deployment deploy.Deployment, snapshot deploy.ClusterSnapshot, message string) {
			selected, err := deploy.ResolvePlacement(deployment, snapshot)
			Expect(selected).To(BeNil())
			Expect(errors.Is(err, deploy.ErrPlacementUnsatisfied)).To(BeTrue())
			Expect(err).To(MatchError(ContainSubstring(message)))
		},
		Entry("when an exact node is dead",
			exactDeployment("node-dead"),
			deploy.ClusterSnapshot{Nodes: clusterNodes(), LeaderNodeID: "node-c", Authoritative: true, HasQuorum: true},
			"missing or not alive",
		),
		Entry("when label replicas cannot be satisfied",
			func() deploy.Deployment {
				deployment := exactDeployment("node-a")
				deployment.Placement = deploy.Placement{
					MatchLabels: &deploy.MatchLabelsPlacement{Labels: map[string]string{"region": "west"}, Replicas: 3},
				}
				return deployment
			}(),
			deploy.ClusterSnapshot{Nodes: clusterNodes(), LeaderNodeID: "node-c", Authoritative: true, HasQuorum: true},
			"needs 3 replicas but only 2 alive nodes match",
		),
	)

	It("rejects duplicate node identities before resolving", func() {
		nodes := clusterNodes()
		nodes = append(nodes, &deployv1.Node{Id: "node-a", Alive: true})
		_, err := deploy.ResolvePlacement(exactDeployment("node-a"), deploy.ClusterSnapshot{
			Nodes: nodes, LeaderNodeID: "node-c", Authoritative: true, HasQuorum: true,
		})
		Expect(errors.Is(err, deploy.ErrInvalidClusterSnapshot)).To(BeTrue())
		Expect(err).To(MatchError(ContainSubstring("duplicate node id")))
	})
})

var _ = Describe("append-only receipts", func() {
	var log *deploy.ReceiptLog
	var at time.Time

	BeforeEach(func() {
		var err error
		log, err = deploy.NewReceiptLog("run-1")
		Expect(err).NotTo(HaveOccurred())
		at = time.Unix(100, 0).UTC()
	})

	receipt := func(id string, sequence uint64, kind deploy.ReceiptKind, offset time.Duration) deploy.Receipt {
		return deploy.Receipt{
			ID: id, RunID: "run-1", Sequence: sequence, Kind: kind, At: at.Add(offset), Summary: string(kind),
		}
	}

	It("appends a chronological sequence and returns independent snapshots", func() {
		Expect(log.Append(receipt("receipt-1", 1, deploy.ReceiptRunQueued, 0))).To(Succeed())
		Expect(log.Append(receipt("receipt-2", 2, deploy.ReceiptRunStarted, time.Second))).To(Succeed())
		Expect(log.Len()).To(Equal(2))

		entries := log.Entries()
		entries[0].Summary = "rewritten"
		Expect(log.Entries()[0].Summary).To(Equal(string(deploy.ReceiptRunQueued)))
	})

	DescribeTable("rejects history rewrites",
		func(second deploy.Receipt, message string) {
			Expect(log.Append(receipt("receipt-1", 1, deploy.ReceiptRunQueued, 0))).To(Succeed())
			err := log.Append(second)
			Expect(errors.Is(err, deploy.ErrReceiptAppend)).To(BeTrue())
			Expect(err).To(MatchError(ContainSubstring(message)))
			Expect(log.Len()).To(Equal(1))
		},
		Entry("by replacing a sequence", receipt("receipt-2", 1, deploy.ReceiptRunStarted, time.Second), "sequence must be 2"),
		Entry("with a duplicate ID", receipt("receipt-1", 2, deploy.ReceiptRunStarted, time.Second), "duplicate receipt id"),
		Entry("across run histories", func() deploy.Receipt {
			other := receipt("receipt-2", 2, deploy.ReceiptRunStarted, time.Second)
			other.RunID = "run-2"
			return other
		}(), "does not match log"),
		Entry("by reversing time", receipt("receipt-2", 2, deploy.ReceiptRunStarted, -time.Second), "cannot precede"),
	)

	It("validates restored durable history", func() {
		restored, err := deploy.RestoreReceiptLog("run-1", []deploy.Receipt{
			receipt("receipt-1", 1, deploy.ReceiptRunQueued, 0),
			receipt("receipt-2", 2, deploy.ReceiptRunStarted, time.Second),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(restored.Len()).To(Equal(2))
	})
})
