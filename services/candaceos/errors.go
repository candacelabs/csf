package candaceos

import "errors"

var (
	// ErrInvalidNode reports a malformed node description.
	ErrInvalidNode = errors.New("candaceos: invalid node")
	// ErrInvalidAppRevision reports a mutable or malformed application revision.
	ErrInvalidAppRevision = errors.New("candaceos: invalid app revision")
	// ErrInvalidPlacement reports a malformed placement policy.
	ErrInvalidPlacement = errors.New("candaceos: invalid placement")
	// ErrInvalidDeployment reports a malformed desired deployment.
	ErrInvalidDeployment = errors.New("candaceos: invalid deployment")
	// ErrInvalidRun reports a malformed execution run.
	ErrInvalidRun = errors.New("candaceos: invalid run")
	// ErrInvalidApproval reports a malformed approval request or decision.
	ErrInvalidApproval = errors.New("candaceos: invalid approval")
	// ErrInvalidReceipt reports a malformed receipt.
	ErrInvalidReceipt = errors.New("candaceos: invalid receipt")
	// ErrInvalidClusterSnapshot reports a malformed cluster snapshot.
	ErrInvalidClusterSnapshot = errors.New("candaceos: invalid cluster snapshot")
	// ErrNotAuthoritative reports that Warden has not supplied an authoritative
	// cluster view from which mutations may be planned.
	ErrNotAuthoritative = errors.New("candaceos: cluster snapshot is not authoritative")
	// ErrNoQuorum reports that a placement decision cannot safely be made.
	ErrNoQuorum = errors.New("candaceos: cluster has no quorum")
	// ErrLeaderUnavailable reports that the elected leader is absent or dead.
	ErrLeaderUnavailable = errors.New("candaceos: cluster leader is unavailable")
	// ErrPlacementUnsatisfied reports that too few suitable alive nodes exist.
	ErrPlacementUnsatisfied = errors.New("candaceos: placement cannot be satisfied")
	// ErrReceiptAppend reports an attempt to alter receipt history instead of
	// extending it with the next event.
	ErrReceiptAppend = errors.New("candaceos: receipt append rejected")
)
