package deploy

import "errors"

var (
	// ErrInvalidNode reports a malformed node description.
	ErrInvalidNode = errors.New("deploy: invalid node")
	// ErrInvalidAppRevision reports a mutable or malformed application revision.
	ErrInvalidAppRevision = errors.New("deploy: invalid app revision")
	// ErrInvalidPlacement reports a malformed placement policy.
	ErrInvalidPlacement = errors.New("deploy: invalid placement")
	// ErrInvalidDeployment reports a malformed desired deployment.
	ErrInvalidDeployment = errors.New("deploy: invalid deployment")
	// ErrInvalidRun reports a malformed execution run.
	ErrInvalidRun = errors.New("deploy: invalid run")
	// ErrInvalidApproval reports a malformed approval request or decision.
	ErrInvalidApproval = errors.New("deploy: invalid approval")
	// ErrInvalidReceipt reports a malformed receipt.
	ErrInvalidReceipt = errors.New("deploy: invalid receipt")
	// ErrInvalidClusterSnapshot reports a malformed cluster snapshot.
	ErrInvalidClusterSnapshot = errors.New("deploy: invalid cluster snapshot")
	// ErrNotAuthoritative reports that Warden has not supplied an authoritative
	// cluster view from which mutations may be planned.
	ErrNotAuthoritative = errors.New("deploy: cluster snapshot is not authoritative")
	// ErrNoQuorum reports that a placement decision cannot safely be made.
	ErrNoQuorum = errors.New("deploy: cluster has no quorum")
	// ErrLeaderUnavailable reports that the elected leader is absent or dead.
	ErrLeaderUnavailable = errors.New("deploy: cluster leader is unavailable")
	// ErrPlacementUnsatisfied reports that too few suitable alive nodes exist.
	ErrPlacementUnsatisfied = errors.New("deploy: placement cannot be satisfied")
	// ErrReceiptAppend reports an attempt to alter receipt history instead of
	// extending it with the next event.
	ErrReceiptAppend = errors.New("deploy: receipt append rejected")
)
