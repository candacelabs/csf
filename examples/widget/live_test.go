package main

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/examples/widget/clusterheartbeats"
	"github.com/candacelabs/csf/examples/widget/raftdemo"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
	"github.com/candacelabs/csf/pkg/widget"
)

// This file is the end-to-end one: a real WebSocket, a real handshake, real
// protobuf frames, and a real leader election at the other end of them.
//
// Everything else in this package's suite asserts on a reducer or on a render.
// Both are necessary and neither is sufficient: a fragment identifier that does
// not match a region, a mount that never schedules its effect, an effect the
// host cannot execute and an event name the registration does not carry are all
// mistakes those specifications hold constant and the wire does not. Until this
// existed, "the live path works" was a claim nobody had run.
const (
	// probeOrigin is what the handshake sends and what the configuration below
	// admits. It is a real allowlist rather than live.AnyOrigin because that is
	// the posture the host ships and the refusal is worth being able to reach.
	probeOrigin = "http://127.0.0.1:8080"

	// probePath is where the handler is mounted in this specification's router,
	// which is at the root: livetest dials the handler, not the host's mux.
	probePath = "/"

	// The frame origin kinds from proto/gotthlive/v1/frame.proto. They are
	// spelled here rather than imported for the reason livetest's own suite
	// gives: the .proto is the public artifact, and a value arriving that is
	// not named here fails with a number somebody can look up.
	originEffect = 2
	originMount  = 5

	// probePatience bounds a wait for one frame. It is generous against the
	// protocol's pace below — an election completes inside a few hundred
	// milliseconds — because a timeout here should mean "nothing arrived",
	// never "the machine was busy".
	probePatience = 20 * time.Second
)

// probeCluster is the pace the election runs at under a specification: fast
// enough that a whole election happens between two assertions, and still far
// enough above the heartbeat that a healthy leader is never campaigned over.
func probeCluster() *raftdemo.Cluster {
	GinkgoHelper()

	cluster, buildError := raftdemo.New(raftdemo.Config{
		Nodes:           3,
		Heartbeat:       25 * time.Millisecond,
		ElectionTimeout: 80 * time.Millisecond,
		ElectionJitter:  60 * time.Millisecond,
		Seed:            20260902,
	})
	Expect(buildError).ToNot(HaveOccurred())

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- cluster.Run(ctx) }()
	DeferCleanup(func() {
		cancel()
		Eventually(stopped, probePatience).Should(Receive(BeNil()))
	})
	return cluster
}

// probeApp is this host's own widgets and this host's own effects, wired the way
// run() wires them, minus the ticker behind the node card.
//
// Leaving the health source out is not tidying. It makes the raft region the
// only region anything can move, which is what lets a specification assert that
// an election's patch carried that region and nothing else — the independent
// live regions property, stated against the wire rather than against a reducer.
func probeApp(cluster *raftdemo.Cluster) *live.App[widget.HostState, live.AnonymousIdentity] {
	GinkgoHelper()

	options := probeOptions()
	options.Init = func(ctx context.Context, session live.Session[live.AnonymousIdentity]) ([]live.Effect[live.AnonymousIdentity], error) {
		return []live.Effect[live.AnonymousIdentity]{clusterSourceEffect(cluster)}, nil
	}
	config, configError := hostWidgets().LiveConfig(options)
	Expect(configError).ToNot(HaveOccurred())

	app, appError := live.New(config)
	Expect(appError).ToNot(HaveOccurred())
	DeferCleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		Expect(app.Close(ctx)).To(Succeed())
	})
	return app
}

// probeOptions is the security posture this host ships, without the two hooks
// that need a running cluster. It is separate from probeApp so that a spec
// asserting on the configuration rather than on the wire — which browser-
// sendable names it declares, say — builds one without starting an election.
func probeOptions() widget.MountOptions[live.AnonymousIdentity] {
	return widget.MountOptions[live.AnonymousIdentity]{
		Origins:      []string{probeOrigin},
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],
		CSRF:         live.NoCSRFCheck,
	}
}

// dialProbe opens one session against the handler, exactly as a browser does.
func dialProbe(app *live.App[widget.HostState, live.AnonymousIdentity]) *livetest.Client {
	GinkgoHelper()
	return livetest.NewClient(GinkgoTB(), app.Handler(), livetest.ClientOptions{
		Path:    probePath,
		Origin:  probeOrigin,
		Timeout: probePatience,
	})
}

// awaitRaft takes frames until one carries the raft region's markup satisfying
// the predicate, acknowledging each patch on the way.
//
// The acknowledgement is what makes this a browser rather than a stalled one.
// livetest never acknowledges on its own — that is what its backpressure
// specifications are built on — and a probe that inherited the silence would
// fill the outbound window in a couple of seconds at this heartbeat and then
// fail with "no frame arrived", which is a true statement about the wrong thing.
//
// CS-9 verdict: this helper stays on livetest's own Await rather than moving
// to candace/pkg/patience, and the distinction is the rule's rather than a
// dispensation from it. There is no timing loop here to delete: Client.Await
// blocks on the frame channel, it is the typed await belonging to the library
// that owns the frame stream, and it fails with every frame it did see —
// which a poller that took one frame per tick could not report. CS-9 asks for
// the loop to live with whatever owns the datum; for frames, that is livetest.
// What is left in this function is the domain — acknowledge, then match the
// raft region's markup — which is exactly what a wrapper should be.
func awaitRaft(client *livetest.Client, what string, match func(html string) bool) *livetest.Frame {
	GinkgoHelper()
	return client.Await(what, probePatience, func(frame *livetest.Frame) bool {
		if frame.Patch == nil {
			return false
		}
		client.Ack(frame.Patch.ServerSeq)
		html, carried := frame.Patch.Fragment(clusterheartbeats.ClusterHeartbeatsRegion)
		return carried && match(html)
	})
}

var _ = Describe("The live patch path, over a real WebSocket", func() {
	It("mounts with the page's own markup, before the election has said anything", func() {
		snapshot := dialProbe(probeApp(probeCluster())).Snapshot()

		Expect(snapshot.Kind).To(Equal(livetest.FrameSnapshot))
		Expect(snapshot.Patch.Origin.Kind).To(BeNumerically("==", originMount))
		Expect(snapshot.Patch.FragmentIDs()).To(ContainElement(clusterheartbeats.ClusterHeartbeatsRegion),
			"a mount renders every region, which is what makes the first paint the "+
				"bytes the first snapshot would produce")

		html, carried := snapshot.Patch.Fragment(clusterheartbeats.ClusterHeartbeatsRegion)
		Expect(carried).To(BeTrue())
		Expect(html).To(ContainSubstring("leader unavailable"),
			"the zero state is a card that has heard nothing, not a card pretending to")
		Expect(html).To(ContainSubstring("stream reconnecting"))
	})

	It("delivers the leader a real election elected, as a patch caused by the stream", func() {
		client := dialProbe(probeApp(probeCluster()))

		frame := awaitRaft(client, "the card reporting an elected leader",
			func(html string) bool { return strings.Contains(html, "elected leader") })

		html, _ := frame.Patch.Fragment(clusterheartbeats.ClusterHeartbeatsRegion)

		// The leader itself: the centre of the scene is bound to what the
		// cluster is doing rather than to which machine it is, so an elected
		// leader is what "there is one" looks like on the wire.
		Expect(html).To(ContainSubstring("elected leader"))
		Expect(html).To(ContainSubstring("term + fleet view"))

		// A term the election actually reached. Which term depends on whether
		// the first round split, so it is asserted as "a real one" rather than
		// as a number the scheduler gets to choose.
		Expect(html).To(MatchRegexp(`term [1-9][0-9]*`),
			"the stat line carries the term the protocol is in, not a placeholder")
		Expect(html).ToNot(ContainSubstring("term —"))

		// Caused by the stream, not by the mount and not by anything the
		// browser sent. This is the assertion that makes the rest of the file
		// about the live path rather than about a render.
		Expect(frame.Kind).To(Equal(livetest.FramePatch))
		Expect(frame.Patch.Origin.Kind).To(BeNumerically("==", originEffect))
		Expect(frame.Patch.StateVersion).To(BeNumerically(">", 0))

		// One event moved one widget, so one region was re-rendered. The node
		// card has no source in this configuration and must not appear.
		Expect(frame.Patch.FragmentIDs()).To(
			ConsistOf(clusterheartbeats.ClusterHeartbeatsRegion))
	})

	It("carries the quorum the leader can actually see", func() {
		client := dialProbe(probeApp(probeCluster()))

		// Both halves are required. A leaderless cluster reports every node
		// alive too — nothing has failed, there is simply nobody elected — so
		// the count alone would be satisfied by the first view of all, before
		// any election had happened at all.
		frame := awaitRaft(client, "the card reporting an elected leader with a full quorum",
			func(html string) bool {
				return strings.Contains(html, "elected leader") &&
					strings.Contains(html, "3 / 3 voters alive")
			})

		html, _ := frame.Patch.Fragment(clusterheartbeats.ClusterHeartbeatsRegion)
		Expect(html).To(ContainSubstring("Live activity"))
		Expect(html).To(ContainSubstring(`data-motion="true"`),
			"the motion gate opens on a live cluster, which is what makes the pulses "+
				"a statement about the protocol")
	})

	It("reports the leader going away, and the leader that replaces it", func() {
		cluster := probeCluster()
		client := dialProbe(probeApp(cluster))

		awaitRaft(client, "a first leader",
			func(html string) bool { return strings.Contains(html, "3 / 3 voters alive") })

		// The card never names the leader — the centre of the scene is bound to
		// what the cluster is doing rather than to which machine it is — so
		// which node to crash is a question only the engine can answer.
		tracking, releaseTracking := context.WithCancel(context.Background())
		DeferCleanup(releaseTracking)
		views, subscribeError := cluster.Subscribe(tracking)
		Expect(subscribeError).ToNot(HaveOccurred())

		var leader string
		Eventually(func() string {
			select {
			case view := <-views:
				if view.LeaderKnown {
					leader = view.Leader
				}
			case <-time.After(time.Second):
			}
			return leader
		}, probePatience).ShouldNot(BeEmpty())
		Expect(cluster.Crash(context.Background(), leader)).To(Succeed())

		// The card stops moving while the survivors campaign. The gate is the
		// widget's own decision from the state the stream wrote, and nothing in
		// this host or in the engine knows the gate exists.
		//
		// The status line says "leader election in progress". Both `whenNot
		// leaderKnown` and `whenNot authoritative` hold of this state — a fleet
		// view is authoritative only when a leader minted it, so no leader means
		// no authority — and an ordered decision returns the first match. The
		// document used to test authoritative first, which meant this line read
		// "non-authoritative view" and the election clause below it could never
		// be reached from any state this host can produce. The order is the
		// meaning, so the narrower guard goes first.
		campaigning := awaitRaft(client, "the card reporting the leaderless cluster",
			func(html string) bool {
				return strings.Contains(html, "leader pending")
			})
		outage, _ := campaigning.Patch.Fragment(clusterheartbeats.ClusterHeartbeatsRegion)
		Expect(outage).To(ContainSubstring(`data-motion="false"`))
		Expect(outage).To(ContainSubstring("leader election in progress"))
		Expect(outage).ToNot(ContainSubstring("non-authoritative view"),
			"the election is the news; non-authority is its consequence, and only one of them renders")
		Expect(outage).To(ContainSubstring("while a leader election is in progress"),
			"the scene's own text alternative says what the picture is showing")

		settled := awaitRaft(client, "the card reporting the replacement leader",
			func(html string) bool {
				return strings.Contains(html, "elected leader") &&
					strings.Contains(html, "2 / 3 voters alive")
			})
		replacement, _ := settled.Patch.Fragment(clusterheartbeats.ClusterHeartbeatsRegion)
		Expect(replacement).To(ContainSubstring("Live activity"),
			"two of three is still a majority, so the survivors are live")
	})

	It("answers a browser's own event on the same connection", func() {
		client := dialProbe(probeApp(probeCluster()))
		awaitRaft(client, "a live cluster",
			func(html string) bool { return strings.Contains(html, "Live activity") })

		// The region is not optional decoration: the frame schema requires a
		// non-empty fragment_id, and it is also what the registry routes on
		// before it looks at the wire name.
		client.Send(clusterheartbeats.ClusterHeartbeatsEventToggleMotion,
			clusterheartbeats.ClusterHeartbeatsRegion, nil)

		paused := awaitRaft(client, "the card reporting itself paused",
			func(html string) bool { return strings.Contains(html, "Resume live pulses") })
		html, _ := paused.Patch.Fragment(clusterheartbeats.ClusterHeartbeatsRegion)
		Expect(html).To(ContainSubstring(`aria-pressed="true"`))
		Expect(html).To(ContainSubstring(`data-motion="false"`),
			"the button closes the gate; it does not stop the protocol")
		Expect(html).To(ContainSubstring("Live activity"),
			"and the cluster is still live while the viewer is not watching it")
	})
})
