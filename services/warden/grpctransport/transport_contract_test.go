package grpctransport_test

// Contract suite for the gRPC warden.ITransport client, asserted against a REAL
// grpcmux server over a real TCP port. It freezes the two client guarantees the
// election manager relies on:
//
//   - faithful round-trips of Vote/Heartbeat/Identify through the wireconv
//     boundary, and
//   - deadline propagation IDENTICAL to the retired HTTPTransport: a caller's
//     own context deadline is always honoured; the configured default timeout
//     applies ONLY when the caller passes no deadline.

import (
	"context"
	"net"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/grpcmux"
	"github.com/candacelabs/csf/services/warden/grpctransport"
	"github.com/candacelabs/csf/services/warden/httpserver"
)

func TestGRPCTransport(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "grpctransport client contract suite")
}

// stubRPC answers unary RPCs with canned responses; Vote can be held until the
// test releases it. The hold deliberately outlives request cancellation:
// returning a canned success on ctx.Done would race the client's deadline and
// test scheduler timing instead of the transport's timeout contract.
type stubRPC struct {
	voteRelease <-chan struct{}
	voteResp    warden.VoteResponse
	hbResp      warden.HeartbeatResponse
	ident       warden.IdentifyResponse
}

func (s *stubRPC) HandleVote(_ context.Context, _ warden.VoteRequest) warden.VoteResponse {
	if s.voteRelease != nil {
		<-s.voteRelease
	}
	return s.voteResp
}
func (s *stubRPC) HandleHeartbeat(ctx context.Context, req warden.HeartbeatRequest) warden.HeartbeatResponse {
	return s.hbResp
}
func (s *stubRPC) HandleIdentify(ctx context.Context) warden.IdentifyResponse { return s.ident }

// stubViews is a no-op ViewSource; WatchCluster is not exercised here.
type stubViews struct{}

func (stubViews) View() warden.ClusterView { return warden.ClusterView{Self: "n1"} }
func (stubViews) Subscribe(buf int) (<-chan warden.ClusterView, func()) {
	ch := make(chan warden.ClusterView, buf)
	return ch, func() {}
}

func startServer(rpc warden.IRPCHandler) (addr string, stop func()) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	srv := grpcmux.New(grpcmux.Config{Listener: lis, HTTP: httpserver.NewEngine(), RPC: rpc, Views: stubViews{}})
	done := make(chan error, 1)
	go func() { done <- srv.Serve() }()
	stop = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		Expect(srv.Shutdown(ctx)).To(Succeed())
		Eventually(done, 5*time.Second).Should(Receive(BeNil()))
	}
	return srv.Addr().String(), stop
}

func startBlockedVoteServer() (addr string, stop func()) {
	release := make(chan struct{})
	addr, stopServer := startServer(&stubRPC{voteRelease: release})
	return addr, func() {
		close(release)
		stopServer()
	}
}

var _ = Describe("gRPC warden.ITransport client", func() {
	Describe("round-trips", func() {
		var (
			tr   *grpctransport.Transport
			stop func()
			peer warden.Node
		)
		BeforeEach(func() {
			rpc := &stubRPC{
				voteResp: warden.VoteResponse{Term: 3, Granted: false, VoterID: "n2"},
				hbResp:   warden.HeartbeatResponse{Term: 7, OK: true, NodeID: "node-a"},
				ident:    warden.IdentifyResponse{ClusterID: "candacenet", NodeID: "node-a", Version: "v1"},
			}
			var addr string
			addr, stop = startServer(rpc)
			tr = grpctransport.New(2 * time.Second)
			peer = warden.Node{ID: "n2", Addr: addr}
		})
		AfterEach(func() {
			Expect(tr.Close()).To(Succeed())
			stop()
		})

		It("carries a VoteRequest/VoteResponse faithfully", func() {
			resp, err := tr.RequestVote(context.Background(), peer, warden.VoteRequest{Term: 3, CandidateID: "n1"})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp).To(Equal(warden.VoteResponse{Term: 3, Granted: false, VoterID: "n2"}))
		})

		It("carries a HeartbeatRequest and decodes the ack", func() {
			resp, err := tr.SendHeartbeat(context.Background(), peer, warden.HeartbeatRequest{Term: 7, LeaderID: "node-d"})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.OK).To(BeTrue())
			Expect(resp.NodeID).To(Equal(warden.NodeID("node-a")))
		})

		It("returns the identify handshake", func() {
			resp, err := tr.Identify(context.Background(), peer)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp).To(Equal(warden.IdentifyResponse{ClusterID: "candacenet", NodeID: "node-a", Version: "v1"}))
		})

		It("reuses one connection across repeated RPCs to the same peer", func() {
			for i := 0; i < 5; i++ {
				_, err := tr.Identify(context.Background(), peer)
				Expect(err).NotTo(HaveOccurred())
			}
		})
	})

	Describe("deadline propagation (identical to the retired HTTPTransport)", func() {
		It("honours a caller context deadline over the client default timeout", func() {
			addr, stop := startBlockedVoteServer()
			defer stop()
			tr := grpctransport.New(10 * time.Second) // large default
			defer tr.Close()
			peer := warden.Node{ID: "slow", Addr: addr}

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err := tr.RequestVote(ctx, peer, warden.VoteRequest{Term: 1, CandidateID: "c"})
			Expect(err).To(HaveOccurred())
			Expect(time.Since(start)).To(BeNumerically("<", 2*time.Second))
		})

		It("applies the default timeout when the caller passes no deadline", func() {
			addr, stop := startBlockedVoteServer()
			defer stop()
			tr := grpctransport.New(150 * time.Millisecond) // small default
			defer tr.Close()
			peer := warden.Node{ID: "slow", Addr: addr}

			start := time.Now()
			_, err := tr.RequestVote(context.Background(), peer, warden.VoteRequest{Term: 1, CandidateID: "c"})
			Expect(err).To(HaveOccurred())
			Expect(time.Since(start)).To(BeNumerically("<", 2*time.Second))
		})
	})

	Describe("failures", func() {
		It("returns an error when the peer is unreachable", func() {
			tr := grpctransport.New(300 * time.Millisecond)
			defer tr.Close()
			// 127.0.0.1:1 is reserved and refuses connections.
			_, err := tr.RequestVote(context.Background(), warden.Node{ID: "dead", Addr: "127.0.0.1:1"}, warden.VoteRequest{Term: 1, CandidateID: "c"})
			Expect(err).To(HaveOccurred())
		})

		It("returns an error after Close", func() {
			tr := grpctransport.New(time.Second)
			Expect(tr.Close()).To(Succeed())
			_, err := tr.Identify(context.Background(), warden.Node{ID: "x", Addr: "127.0.0.1:2"})
			Expect(err).To(HaveOccurred())
		})
	})
})
