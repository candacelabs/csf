package grpcserver_test

import (
	"context"
	"net"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/election"
	"github.com/candacelabs/csf/services/warden/grpcserver"
	"github.com/candacelabs/csf/services/warden/internal/mocks"
	"github.com/candacelabs/csf/services/warden/internal/transportidentity"
	wardenv1 "github.com/candacelabs/csf/services/warden/proto/warden/v1"
	"github.com/candacelabs/csf/services/warden/store"
	"github.com/candacelabs/csf/services/warden/testclock"
	"github.com/candacelabs/csf/services/warden/wireconv"
)

func TestHeartbeatAuthentication(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "heartbeat authentication contract suite")
}

type heartbeatRPCFixture struct {
	client  wardenv1.WardenServiceClient
	manager *election.Manager
	server  *grpcserver.Server
	store   *store.MemStore
	stop    func()
}

func newLegacyHeartbeatRPCFixture(sourceIP string, leaderAddress string) *heartbeatRPCFixture {
	controller := gomock.NewController(GinkgoT())
	discoverer := mocks.NewMockIPeerDiscoverer(controller)
	discoverer.EXPECT().Discover(gomock.Any()).Return((<-chan warden.Roster)(nil), nil)
	transport := mocks.NewMockITransport(controller)

	initialMembership := warden.Membership{
		Version:       4,
		CreatedInTerm: 4,
		Voters: []warden.Node{
			{ID: "node-a", Addr: "127.0.0.1:7717"},
			{ID: "node-b", Addr: leaderAddress},
		},
	}
	st := store.NewMemStore()
	Expect(st.Save(warden.PersistentState{
		CurrentTerm: 4,
		Membership:  &initialMembership,
	})).To(Succeed())

	mgr, err := election.NewManager(election.Config{
		Self: warden.Node{ID: "node-a", Addr: "127.0.0.1:7717"},
		Peers: []warden.Node{
			{ID: "node-a", Addr: "127.0.0.1:7717"},
			{ID: "node-b", Addr: "127.0.0.2:7717"},
			{ID: "node-c", Addr: "127.0.0.3:7717"},
		},
		HeartbeatInterval:  time.Second,
		ElectionTimeoutMin: 2 * time.Second,
		ElectionTimeoutMax: 3 * time.Second,
		Discoverer:         discoverer,
	}, transport, st, testclock.New(time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)))
	Expect(err).NotTo(HaveOccurred())
	return serveHeartbeatRPCFixture(sourceIP, mgr, st)
}

func newFixedHeartbeatRPCFixture(sourceIP string, leaderAddress string) *heartbeatRPCFixture {
	controller := gomock.NewController(GinkgoT())
	transport := mocks.NewMockITransport(controller)
	st := store.NewMemStore()
	Expect(st.Save(warden.PersistentState{CurrentTerm: 4})).To(Succeed())
	mgr, err := election.NewManager(election.Config{
		Self: warden.Node{ID: "node-a", Addr: "127.0.0.10:7717"},
		Peers: []warden.Node{
			{ID: "node-a", Addr: "127.0.0.10:7717"},
			{ID: "node-b", Addr: leaderAddress},
			{ID: "node-c", Addr: "127.0.0.3:7717"},
		},
		LeaderID:           "node-b",
		HeartbeatInterval:  time.Second,
		ElectionTimeoutMin: 2 * time.Second,
		ElectionTimeoutMax: 3 * time.Second,
	}, transport, st, testclock.New(time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)))
	Expect(err).NotTo(HaveOccurred())
	return serveHeartbeatRPCFixture(sourceIP, mgr, st)
}

func serveHeartbeatRPCFixture(sourceIP string, mgr *election.Manager, st *store.MemStore) *heartbeatRPCFixture {

	managerCtx, stopManager := context.WithCancel(context.Background())
	managerDone := make(chan error, 1)
	go func() { managerDone <- mgr.Run(managerCtx) }()

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	adapter := grpcserver.New(mgr, mgr, context.Background())
	server := grpcserver.NewGRPCServer(adapter)
	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(listener) }()

	source := net.ParseIP(sourceIP)
	Expect(source).NotTo(BeNil())
	dialer := func(ctx context.Context, address string) (net.Conn, error) {
		return (&net.Dialer{LocalAddr: &net.TCPAddr{IP: source}}).DialContext(ctx, "tcp4", address)
	}
	conn, err := grpc.NewClient(listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer),
	)
	Expect(err).NotTo(HaveOccurred())

	stop := func() {
		Expect(conn.Close()).To(Succeed())
		server.GracefulStop()
		Expect(<-serverDone).To(SatisfyAny(BeNil(), MatchError(grpc.ErrServerStopped)))
		stopManager()
		Expect(<-managerDone).To(Succeed())
	}
	return &heartbeatRPCFixture{
		client:  wardenv1.NewWardenServiceClient(conn),
		manager: mgr,
		server:  adapter,
		store:   st,
		stop:    stop,
	}
}

func sendHeartbeat(client wardenv1.WardenServiceClient, leaderID warden.NodeID) warden.HeartbeatResponse {
	proposed := warden.Membership{
		Version:       99,
		CreatedInTerm: 9,
		Voters:        []warden.Node{{ID: "node-a", Addr: "127.0.0.1:7717"}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := client.Heartbeat(ctx, wireconv.HeartbeatRequestToProto(warden.HeartbeatRequest{
		Term:       9,
		LeaderID:   leaderID,
		Membership: &proposed,
	}))
	Expect(err).NotTo(HaveOccurred())
	return wireconv.HeartbeatResponseFromProto(response)
}

func expectPersistedState(st *store.MemStore, term warden.Term, version uint64, voters ...warden.NodeID) {
	persisted, ok, err := st.Load()
	Expect(err).NotTo(HaveOccurred())
	Expect(ok).To(BeTrue())
	Expect(persisted.CurrentTerm).To(Equal(term))
	Expect(persisted.Membership).NotTo(BeNil())
	Expect(persisted.Membership.Version).To(Equal(version))
	actual := make([]warden.NodeID, 0, len(persisted.Membership.Voters))
	for _, voter := range persisted.Membership.Voters {
		actual = append(actual, voter.ID)
	}
	Expect(actual).To(Equal(voters))
}

var _ = Describe("heartbeat sender authentication at the real gRPC boundary", func() {
	DescribeTable("rejects untrusted senders before persisting their term or membership",
		func(sourceIP string, leaderID warden.NodeID) {
			fixture := newLegacyHeartbeatRPCFixture(sourceIP, "127.0.0.2:7717")
			DeferCleanup(fixture.stop)

			response := sendHeartbeat(fixture.client, leaderID)

			Expect(response.OK).To(BeFalse())
			Expect(response.Term).To(Equal(warden.Term(4)))
			expectPersistedState(fixture.store, 4, 4, "node-a", "node-b")
		},
		Entry("a forged current-voter identity from the wrong address", "127.0.0.9", warden.NodeID("node-b")),
		Entry("an observer or unknown identity", "127.0.0.4", warden.NodeID("observer")),
		Entry("a removed former voter", "127.0.0.3", warden.NodeID("node-c")),
	)

	DescribeTable("authenticates the current discovery membership before persisting an update", func(sourceIP string, leaderAddress string, accepted bool) {
		fixture := newLegacyHeartbeatRPCFixture(sourceIP, leaderAddress)
		DeferCleanup(fixture.stop)

		response := sendHeartbeat(fixture.client, "node-b")

		Expect(response.OK).To(Equal(accepted))
		if accepted {
			Expect(response.Term).To(Equal(warden.Term(9)))
			expectPersistedState(fixture.store, 9, 99, "node-a")
		} else {
			Expect(response.Term).To(Equal(warden.Term(4)))
			expectPersistedState(fixture.store, 4, 4, "node-a", "node-b")
		}
	},
		Entry("literal IP", "127.0.0.2", "127.0.0.2:7717", true),
		Entry("persisted hostname overrides seed address", "127.0.0.1", "localhost:7717", true),
		Entry("unrelated IP cannot claim hostname identity", "127.0.0.9", "localhost:7717", false),
		Entry("seed IP cannot override current hostname", "127.0.0.2", "localhost:7717", false),
	)

	DescribeTable("accepts the fixed leader through its configured host mapping without adopting its roster",
		func(sourceIP string, leaderAddress string) {
			fixture := newFixedHeartbeatRPCFixture(sourceIP, leaderAddress)
			DeferCleanup(fixture.stop)

			response := sendHeartbeat(fixture.client, "node-b")

			Expect(response.OK).To(BeTrue())
			Expect(response.Term).To(Equal(warden.Term(9)))
			view := fixture.manager.View()
			Expect(view.Membership.Version).To(BeZero())
			Expect(view.Membership.Voters).To(ConsistOf(
				warden.Node{ID: "node-a", Addr: "127.0.0.10:7717"},
				warden.Node{ID: "node-b", Addr: leaderAddress},
				warden.Node{ID: "node-c", Addr: "127.0.0.3:7717"},
			))
			persisted, ok, err := fixture.store.Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(ok).To(BeTrue())
			Expect(persisted.CurrentTerm).To(Equal(warden.Term(9)))
			Expect(persisted.Membership).To(BeNil())
		},
		Entry("literal IP", "127.0.0.2", "127.0.0.2:7717"),
		Entry("hostname resolved during trusted setup", "127.0.0.1", "localhost:7717"),
	)

	It("rejects the fixed leader identity from a different actual IP before mutation", func() {
		fixture := newFixedHeartbeatRPCFixture("127.0.0.9", "127.0.0.2:7717")
		DeferCleanup(fixture.stop)

		response := sendHeartbeat(fixture.client, "node-b")

		Expect(response.OK).To(BeFalse())
		Expect(response.Term).To(Equal(warden.Term(4)))
		Expect(fixture.manager.View().Term).To(Equal(warden.Term(4)))
	})

	It("rejects a matching configured non-leader source before its high term mutates state", func() {
		fixture := newFixedHeartbeatRPCFixture("127.0.0.3", "127.0.0.2:7717")
		DeferCleanup(fixture.stop)

		response := sendHeartbeat(fixture.client, "node-c")

		Expect(response.OK).To(BeFalse())
		Expect(response.Term).To(Equal(warden.Term(4)))
		Expect(fixture.manager.View().Term).To(Equal(warden.Term(4)))
	})

	It("clears a caller-injected address when no network peer is present", func() {
		fixture := newFixedHeartbeatRPCFixture("127.0.0.2", "127.0.0.2:7717")
		DeferCleanup(fixture.stop)
		ctx := transportidentity.WithPeerAddress(context.Background(), "127.0.0.2:12345")

		response, err := fixture.server.Heartbeat(ctx, wireconv.HeartbeatRequestToProto(warden.HeartbeatRequest{
			Term: 9, LeaderID: "node-b",
		}))

		Expect(err).NotTo(HaveOccurred())
		Expect(response.GetOk()).To(BeFalse())
		Expect(response.GetTerm()).To(Equal(uint64(4)))
	})
})
