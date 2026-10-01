package grpcmux_test

// Transport contract suite for the single-port gRPC + HTTP mux, asserted over a
// REAL cmux listener on a real TCP port with a real gRPC client (h2c, insecure
// creds — the tailnet is the trust boundary). It freezes:
//
//   - unary happy paths (Vote/Heartbeat/Identify) end to end through the
//     wireconv boundary, incl. request decode;
//   - the error-code table (missing required field -> InvalidArgument; handler
//     panic -> Internal);
//   - WatchCluster semantics: initial snapshot, `since` suppression, cursor
//     dedup, drop-to-latest, delivery on leader AND membership change, and
//     survival across client disconnect/reconnect;
//   - the headline: dashboard/api/metrics still serve over HTTP/1.1 on the SAME
//     port WHILE a WatchCluster stream and gRPC unary calls run concurrently;
//   - clean stream teardown (no leaked subscription/goroutine on disconnect) —
//     the counterfactual the "drop the ctx.Done() teardown" mutation turns red.

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/grpcmux"
	"github.com/candacelabs/csf/services/warden/httpserver"
	wardenv1 "github.com/candacelabs/csf/services/warden/proto/warden/v1"
	"github.com/candacelabs/csf/services/warden/wireconv"
)

func TestGRPCMux(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "grpcmux transport contract suite")
}

// Distinct UTC instants give distinct watch cursors (updated_at is part of the
// cursor triple).
var (
	tA = time.Date(2026, 7, 21, 15, 0, 0, 0, time.UTC)
	tB = time.Date(2026, 7, 21, 15, 0, 1, 0, time.UTC)
	tC = time.Date(2026, 7, 21, 15, 0, 2, 0, time.UTC)
)

// viewAt builds a full ClusterView with a distinct UpdatedAt and the given
// leader/term at a fixed membership.
func viewAt(term uint64, leader string, at time.Time) warden.ClusterView {
	return warden.ClusterView{
		Self: "n1", Role: warden.RoleFollower, Term: warden.Term(term), LeaderID: warden.NodeID(leader),
		Source: warden.NodeID(leader), Authoritative: true, UpdatedAt: at,
		Peers:      []warden.PeerView{{Node: warden.Node{ID: "n1", Addr: "127.0.0.1:1"}, Status: warden.StatusAlive, LastSeen: at, Member: warden.MemberVoter}},
		Membership: warden.Membership{Version: 1, CreatedInTerm: 1, Voters: []warden.Node{{ID: "n1", Addr: "127.0.0.1:1"}}},
	}
}

// viewMembership bumps the membership version (a membership change) while
// keeping UpdatedAt fixed, so only the membership component of the cursor moves.
func viewMembership(version uint64, at time.Time) warden.ClusterView {
	v := viewAt(1, "n1", at)
	v.Membership.Version = version
	return v
}

func cursorOf(v warden.ClusterView) *wardenv1.ClusterViewCursor {
	return wireconv.CursorOf(wireconv.ClusterViewToProto(v))
}

func mustListen() net.Listener {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	return l
}

// httpEngine mounts a representative HTTP surface on the production gin engine
// (real 405/404 semantics). The dashboard/metrics packages test their full
// contract elsewhere; here we only prove HTTP/1.1 shares the port with gRPC.
func httpEngine() *gin.Engine {
	e := httpserver.NewEngine()
	e.GET("/api/status", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"self": "n1"}) })
	e.GET("/metrics", func(c *gin.Context) { c.String(http.StatusOK, "warden_up 1") })
	return e
}

// readUpdates continuously receives from a WatchCluster stream, forwarding each
// update onto the returned channel and closing it on stream end/error.
func readUpdates(stream wardenv1.WardenService_WatchClusterClient) chan *wardenv1.ClusterViewUpdate {
	ch := make(chan *wardenv1.ClusterViewUpdate, 64)
	go func() {
		for {
			u, err := stream.Recv()
			if err != nil {
				close(ch)
				return
			}
			ch <- u
		}
	}()
	return ch
}

var _ = Describe("single-port gRPC + HTTP mux", func() {
	var (
		rpc      *fakeRPC
		views    *fakeViewSource
		srv      *grpcmux.Server
		serveErr chan error
		cc       *grpc.ClientConn
		client   wardenv1.WardenServiceClient
	)

	BeforeEach(func() {
		rpc = &fakeRPC{
			voteResp:  warden.VoteResponse{Term: 7, Granted: true, VoterID: "node-a"},
			hbResp:    warden.HeartbeatResponse{Term: 7, OK: true, NodeID: "node-a"},
			identResp: warden.IdentifyResponse{ClusterID: "candacenet", NodeID: "node-a", Version: "v1.2.3"},
		}
		views = newFakeViewSource(viewAt(1, "n1", tA))

		lis := mustListen()
		srv = grpcmux.New(grpcmux.Config{Listener: lis, HTTP: httpEngine(), RPC: rpc, Views: views})
		serveErr = make(chan error, 1)
		// Capture srv and the channel locally so the serve goroutine never reads
		// the Describe-scoped vars, which the next spec's BeforeEach reassigns.
		srvLocal, serveErrLocal := srv, serveErr
		go func() { serveErrLocal <- srvLocal.Serve() }()

		var err error
		cc, err = grpc.NewClient(srv.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
		Expect(err).NotTo(HaveOccurred())
		client = wardenv1.NewWardenServiceClient(cc)
	})

	AfterEach(func() {
		_ = cc.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		Expect(srv.Shutdown(ctx)).To(Succeed())
		Eventually(serveErr, 5*time.Second).Should(Receive(BeNil()))
	})

	Describe("unary RPCs", func() {
		It("Vote round-trips and the handler sees the decoded request", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			resp, err := client.Vote(ctx, &wardenv1.VoteRequest{Term: 42, CandidateId: "node-d"})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.GetTerm()).To(Equal(uint64(7)))
			Expect(resp.GetGranted()).To(BeTrue())
			Expect(resp.GetVoterId()).To(Equal("node-a"))
			Expect(rpc.sawVote()).To(Equal(&warden.VoteRequest{Term: 42, CandidateID: "node-d"}))
		})

		It("Heartbeat round-trips a request carrying a view and membership", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			hb := &wardenv1.HeartbeatRequest{Term: 9, LeaderId: "n1", View: wireconv.ClusterViewToProto(viewAt(9, "n1", tB)), Membership: wireconv.MembershipToProto(viewAt(9, "n1", tB).Membership)}
			resp, err := client.Heartbeat(ctx, hb)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.GetOk()).To(BeTrue())
			got := rpc.sawHeartbeat()
			Expect(got).NotTo(BeNil())
			Expect(got.Term).To(Equal(warden.Term(9)))
			Expect(got.LeaderID).To(Equal(warden.NodeID("n1")))
			Expect(got.View).NotTo(BeNil())
			Expect(got.View.Term).To(Equal(warden.Term(9)))
		})

		It("Identify returns the handshake response", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			resp, err := client.Identify(ctx, &wardenv1.IdentifyRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.GetClusterId()).To(Equal("candacenet"))
			Expect(resp.GetNodeId()).To(Equal("node-a"))
		})
	})

	Describe("error-code semantics", func() {
		It("rejects a vote with no candidate_id as InvalidArgument", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := client.Vote(ctx, &wardenv1.VoteRequest{Term: 1})
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("rejects a heartbeat with no leader_id as InvalidArgument", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := client.Heartbeat(ctx, &wardenv1.HeartbeatRequest{Term: 1})
			Expect(status.Code(err)).To(Equal(codes.InvalidArgument))
		})

		It("maps a handler panic to Internal (recovery interceptor)", func() {
			rpc.mu.Lock()
			rpc.panicVote = true
			rpc.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := client.Vote(ctx, &wardenv1.VoteRequest{Term: 1, CandidateId: "node-d"})
			Expect(status.Code(err)).To(Equal(codes.Internal))
		})
	})

	Describe("WatchCluster streaming", func() {
		It("delivers the initial snapshot with its cursor", func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stream, err := client.WatchCluster(ctx, &wardenv1.WatchClusterRequest{})
			Expect(err).NotTo(HaveOccurred())
			updates := readUpdates(stream)

			var u *wardenv1.ClusterViewUpdate
			Eventually(updates, 3*time.Second).Should(Receive(&u))
			Expect(u.GetView().GetLeaderId()).To(Equal("n1"))
			Expect(wireconv.CursorEqual(u.GetCursor(), cursorOf(viewAt(1, "n1", tA)))).To(BeTrue())
		})

		It("suppresses the initial snapshot when `since` matches current, then delivers changes", func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stream, err := client.WatchCluster(ctx, &wardenv1.WatchClusterRequest{Since: cursorOf(viewAt(1, "n1", tA))})
			Expect(err).NotTo(HaveOccurred())
			updates := readUpdates(stream)

			// No initial snapshot, since the resume cursor already matches.
			Consistently(updates, 400*time.Millisecond).ShouldNot(Receive())

			// A subsequent change is delivered.
			views.publish(viewAt(2, "n2", tB))
			var u *wardenv1.ClusterViewUpdate
			Eventually(updates, 3*time.Second).Should(Receive(&u))
			Expect(u.GetView().GetLeaderId()).To(Equal("n2"))
			Expect(wireconv.CursorEqual(u.GetCursor(), cursorOf(viewAt(2, "n2", tB)))).To(BeTrue())
		})

		It("dedups a repeated cursor (no update for an unchanged state)", func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stream, err := client.WatchCluster(ctx, &wardenv1.WatchClusterRequest{})
			Expect(err).NotTo(HaveOccurred())
			updates := readUpdates(stream)
			Eventually(updates, 3*time.Second).Should(Receive()) // initial

			// Re-publish the SAME view: same cursor -> suppressed.
			views.publish(viewAt(1, "n1", tA))
			Consistently(updates, 400*time.Millisecond).ShouldNot(Receive())

			// A genuine change is delivered.
			views.publish(viewAt(2, "n2", tB))
			Eventually(updates, 3*time.Second).Should(Receive())
		})

		It("drop-to-latest: a change signal delivers CURRENT state, not the signal payload", func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stream, err := client.WatchCluster(ctx, &wardenv1.WatchClusterRequest{})
			Expect(err).NotTo(HaveOccurred())
			updates := readUpdates(stream)
			Eventually(updates, 3*time.Second).Should(Receive()) // initial

			// Advance current to tC, then signal with a STALE tB payload. The
			// handler must deliver tC (latest), ignoring the payload.
			views.setView(viewAt(3, "n3", tC))
			views.signal(viewAt(2, "n2", tB))
			var u *wardenv1.ClusterViewUpdate
			Eventually(updates, 3*time.Second).Should(Receive(&u))
			Expect(wireconv.CursorEqual(u.GetCursor(), cursorOf(viewAt(3, "n3", tC)))).To(BeTrue())
		})

		It("delivers an update on a membership change (cursor's membership component moves)", func() {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stream, err := client.WatchCluster(ctx, &wardenv1.WatchClusterRequest{})
			Expect(err).NotTo(HaveOccurred())
			updates := readUpdates(stream)
			Eventually(updates, 3*time.Second).Should(Receive()) // initial (version 1)

			views.publish(viewMembership(2, tA)) // same updated_at, version 1 -> 2
			var u *wardenv1.ClusterViewUpdate
			Eventually(updates, 3*time.Second).Should(Receive(&u))
			Expect(u.GetView().GetMembership().GetVersion()).To(Equal(uint64(2)))
		})

		It("survives client disconnect and a fresh reconnect", func() {
			ctx1, cancel1 := context.WithCancel(context.Background())
			stream1, err := client.WatchCluster(ctx1, &wardenv1.WatchClusterRequest{})
			Expect(err).NotTo(HaveOccurred())
			updates1 := readUpdates(stream1)
			Eventually(updates1, 3*time.Second).Should(Receive())

			// Disconnect.
			cancel1()
			Eventually(updates1, 3*time.Second).Should(BeClosed())
			Eventually(views.activeSubs, 3*time.Second).Should(BeZero())

			// Reconnect: the server still serves and re-delivers a snapshot.
			ctx2, cancel2 := context.WithCancel(context.Background())
			defer cancel2()
			stream2, err := client.WatchCluster(ctx2, &wardenv1.WatchClusterRequest{})
			Expect(err).NotTo(HaveOccurred())
			updates2 := readUpdates(stream2)
			Eventually(updates2, 3*time.Second).Should(Receive())
		})
	})

	Describe("teardown / no subscription leak on disconnect", func() {
		It("releases the subscription when the client disconnects (goroutine-leak tripwire)", func() {
			Expect(views.activeSubs()).To(BeZero())
			ctx, cancel := context.WithCancel(context.Background())
			stream, err := client.WatchCluster(ctx, &wardenv1.WatchClusterRequest{})
			Expect(err).NotTo(HaveOccurred())
			updates := readUpdates(stream)
			Eventually(updates, 3*time.Second).Should(Receive())
			Eventually(views.activeSubs, 3*time.Second).Should(Equal(1))

			// Disconnect and stop signalling: the handler must notice the stream
			// context and tear down (defer cancel -> activeSubs back to 0). If the
			// stream-ctx teardown case were dropped, the handler would park on the
			// subscription forever and this would time out.
			cancel()
			Eventually(views.activeSubs, 3*time.Second).Should(BeZero())
		})
	})

	Describe("the headline: HTTP/1.1 and gRPC concurrent on ONE port", func() {
		It("serves dashboard/api/metrics over HTTP/1.1 while a gRPC stream and unary calls run", func() {
			// Bring up a live WatchCluster stream.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stream, err := client.WatchCluster(ctx, &wardenv1.WatchClusterRequest{})
			Expect(err).NotTo(HaveOccurred())
			updates := readUpdates(stream)
			Eventually(updates, 3*time.Second).Should(Receive())

			addr := srv.Addr().String()
			httpc := &http.Client{Timeout: 3 * time.Second}

			// HTTP/1.1 GET /api/status on the same port.
			resp, err := httpc.Get("http://" + addr + "/api/status")
			Expect(err).NotTo(HaveOccurred())
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(resp.Proto).To(Equal("HTTP/1.1"))
			Expect(string(body)).To(ContainSubstring(`"self":"n1"`))

			// HTTP/1.1 GET /metrics on the same port.
			mresp, err := httpc.Get("http://" + addr + "/metrics")
			Expect(err).NotTo(HaveOccurred())
			mbody, _ := io.ReadAll(mresp.Body)
			mresp.Body.Close()
			Expect(mresp.StatusCode).To(Equal(http.StatusOK))
			Expect(string(mbody)).To(ContainSubstring("warden_up"))

			// A concurrent gRPC unary call still works.
			uctx, ucancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer ucancel()
			vresp, err := client.Vote(uctx, &wardenv1.VoteRequest{Term: 1, CandidateId: "node-d"})
			Expect(err).NotTo(HaveOccurred())
			Expect(vresp.GetGranted()).To(BeTrue())

			// And the stream is still live: a change is delivered.
			views.publish(viewAt(5, "n2", tC))
			Eventually(updates, 3*time.Second).Should(Receive())
		})
	})
})
