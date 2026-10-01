package election

import (
	. "github.com/onsi/ginkgo/v2"

	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/internal/mocks"
)

// iHarnessT is the minimal subset of *testing.T that the deterministic election
// simulator (harness_test.go) and the standalone Manager builders (newSolo,
// newUnstartedManager, ledgerLeader, newClusterWithStores) need. Both
// *testing.T and Ginkgo's GinkgoT() satisfy it, so the hand-written simulators
// drive Ginkgo specs unchanged: the simulators stay simulators (never gomock),
// only their surrounding test functions became specs.
type iHarnessT interface {
	Helper()
	Fatalf(format string, args ...any)
	Cleanup(cleanup func())
}

// stubTransport is the gomock swap for the old hand-rolled nopTransport: a
// permissive MockITransport where every peer is unreachable and any number of
// calls (including none) is acceptable. It stands in for the trivial stub
// transport used by the standalone (non-harness) Manager builders, where
// transport behavior is irrelevant — the harness's own cluster transport is a
// simulator and is never swapped. The controller auto-finishes via GinkgoT()'s
// Cleanup; the AnyTimes expectations never fail Finish.
func stubTransport() *mocks.MockITransport {
	ctrl := gomock.NewController(GinkgoT())
	tr := mocks.NewMockITransport(ctrl)
	tr.EXPECT().RequestVote(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(warden.VoteResponse{}, errUnreachable).AnyTimes()
	tr.EXPECT().SendHeartbeat(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(warden.HeartbeatResponse{}, errUnreachable).AnyTimes()
	tr.EXPECT().Identify(gomock.Any(), gomock.Any()).
		Return(warden.IdentifyResponse{}, errUnreachable).AnyTimes()
	return tr
}
