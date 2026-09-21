package main

import (
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/examples/widget/clusterheartbeats"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
)

// This file is the P2 adversarial audit's one unrepaired exploit, kept as a
// specification rather than as a paragraph.
//
// What the audit did: it opened a real WebSocket against this demo and posted
// the cluster snapshot event itself — the event a declared stream delivers from
// the raft engine — with a term, a voter count and an alive count of its own
// choosing. The card rendered them. The demo's state is per-session and carries
// nothing privileged, so the forgery lied only to the browser that sent it; the
// severity was never this page, it was that the SDK made every host solve the
// same problem and the first one to forget would ship the hole.
//
// The audit wrote: "The probe that produced the line above was deleted with the
// rest of this session's throwaway specs; it belongs with the fix, as the
// characterization test that shows the hole closing." This is that probe.
const (
	// The forged values, exactly as the audit sent them. They are absurd on
	// purpose: no election this demo can run produces a term of 31337 or a
	// membership of 99, so the card rendering either of them could only have
	// got it from the browser.
	forgedTerm        = "31337"
	forgedVoters      = "99"
	forgedAliveVoters = "0"

	// errorCodeUnknownEvent is ErrorCode.UNKNOWN_EVENT from
	// proto/gotthlive/v1/frame.proto, spelled here for the reason livetest's
	// own suite gives: the .proto is the public artifact, and a code arriving
	// that is not named here fails with a number somebody can look up.
	errorCodeUnknownEvent = 4

	// forgerySettle is how long the card is watched for the forged values after
	// the refusal. It only has to outlast the round trip the forgery would have
	// taken; the refusal has already arrived by the time it starts.
	forgerySettle = 250 * time.Millisecond
)

var _ = Describe("A browser forging a stream-delivered event", func() {
	// forge posts the audit's snapshot over the socket and returns the client's
	// handle for it, so a spec can tell the server's answer to this event from
	// its answer to anything else on the connection.
	forge := func(client *livetest.Client) uint64 {
		GinkgoHelper()
		return client.Send(
			clusterheartbeats.ClusterHeartbeatsEventSnapshot,
			clusterheartbeats.ClusterHeartbeatsRegion,
			map[string]string{
				clusterheartbeats.ClusterHeartbeatsEventSnapshotFieldSequence:      "999999",
				clusterheartbeats.ClusterHeartbeatsEventSnapshotFieldConnected:     "true",
				clusterheartbeats.ClusterHeartbeatsEventSnapshotFieldAuthoritative: "true",
				clusterheartbeats.ClusterHeartbeatsEventSnapshotFieldLeaderKnown:   "false",
				clusterheartbeats.ClusterHeartbeatsEventSnapshotFieldHasQuorum:     "false",
				clusterheartbeats.ClusterHeartbeatsEventSnapshotFieldTerm:          forgedTerm,
				clusterheartbeats.ClusterHeartbeatsEventSnapshotFieldVoters:        forgedVoters,
				clusterheartbeats.ClusterHeartbeatsEventSnapshotFieldAliveVoters:   forgedAliveVoters,
			})
	}

	// answer waits for whichever comes first: the server refusing the forged
	// event, or the card rendering what the forgery asked for. Racing the two
	// is what makes this a probe rather than an assertion about timing — a
	// forgery that lands arrives as a patch, and a spec that waited only for
	// the refusal would report "nothing arrived", which is a true statement
	// about the wrong thing.
	answer := func(client *livetest.Client, clientRef uint64) *livetest.Frame {
		GinkgoHelper()
		return client.Await("the server's answer to the forged event", probePatience,
			func(frame *livetest.Frame) bool {
				if frame.Patch != nil {
					client.Ack(frame.Patch.ServerSeq)
					html, carried := frame.Patch.Fragment(clusterheartbeats.ClusterHeartbeatsRegion)
					return carried && strings.Contains(html, "term "+forgedTerm)
				}
				return frame.Kind == livetest.FrameError && frame.Error.ClientRef == clientRef
			})
	}

	It("is refused before any reducer runs, because the widget does not register the name", func() {
		client := dialProbe(probeApp(probeCluster()))
		awaitRaft(client, "a live cluster",
			func(html string) bool { return strings.Contains(html, "Live activity") })

		refusal := answer(client, forge(client))

		Expect(refusal.Kind).To(Equal(livetest.FrameError),
			"the card rendered a term no election reached, so the forged snapshot was applied")
		Expect(refusal.Error.Code).To(BeNumerically("==", errorCodeUnknownEvent),
			"the name is not registered, which is the default-deny the SDK's comment always claimed")
		Expect(refusal.Error.Fatal).To(BeFalse(),
			"a refused event leaves the session running; forging one is not a way to close somebody's tab")
	})

	It("never reaches the card, so the forged cluster is not what the browser is shown", func() {
		client := dialProbe(probeApp(probeCluster()))
		awaitRaft(client, "a live cluster",
			func(html string) bool { return strings.Contains(html, "Live activity") })

		answer(client, forge(client))

		// The two lines the audit read back off the forged card, asserted over
		// every frame this connection ever carried — the ones before the
		// refusal included, because a forgery applied early would have been
		// rendered before the refusal could arrive.
		frames := append(client.Received(), client.Settle(forgerySettle)...)
		for _, frame := range frames {
			if frame.Patch == nil {
				continue
			}
			html, carried := frame.Patch.Fragment(clusterheartbeats.ClusterHeartbeatsRegion)
			if !carried {
				continue
			}
			Expect(html).ToNot(ContainSubstring("term "+forgedTerm),
				"the card is showing a term no election reached")
			Expect(html).ToNot(ContainSubstring(forgedAliveVoters+" / "+forgedVoters+" voters alive"),
				"the card is showing a membership no cluster has")
		}
	})

	It("is unregistered by the widget itself, not subtracted by this host", func() {
		// The registration is where the fix lives. Before it, the generator put
		// every declared event in Events, the homepage subtracted the
		// stream-delivered ones by hand, and this demo did not — so the hole was
		// in whichever host forgot, which is exactly the wrong place for it to
		// depend on.
		registration := clusterheartbeats.NewClusterHeartbeats[live.AnonymousIdentity]().Register()

		Expect(registration.Internal).To(ContainElement(
			clusterheartbeats.ClusterHeartbeatsEventSnapshot))
		Expect(registration.Events).ToNot(ContainElement(
			clusterheartbeats.ClusterHeartbeatsEventSnapshot))
		Expect(registration.Events).To(ContainElement(
			clusterheartbeats.ClusterHeartbeatsEventToggleMotion),
			"the pause button is a browser interaction and stays sendable")

		config, configError := hostWidgets().LiveConfig(probeOptions())
		Expect(configError).ToNot(HaveOccurred())
		Expect(config.Events).ToNot(ContainElement(
			clusterheartbeats.ClusterHeartbeatsEventSnapshot),
			"registration is the only thing that makes a name sendable, so the enforcement is the absence")
	})

	It("still reaches the card when the declared stream is what delivers it", func() {
		// The other half, and the one a fix like this breaks silently: an
		// unregistered name must still route. A stream-delivered snapshot is
		// emitted server-side by the host's own effect, and the card moves.
		client := dialProbe(probeApp(probeCluster()))

		frame := awaitRaft(client, "the card reporting a real elected leader",
			func(html string) bool { return strings.Contains(html, "elected leader") })

		html, _ := frame.Patch.Fragment(clusterheartbeats.ClusterHeartbeatsRegion)
		Expect(html).To(MatchRegexp(`term [1-9][0-9]*`))
		Expect(strconv.Quote(html)).ToNot(ContainSubstring(forgedTerm))
	})
})
