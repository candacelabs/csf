package main

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/examples/widget/nodestatus"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
)

// The forgery, against the OTHER stream-delivered event this host serves.
//
// forgery_test.go proves the hole is closed for the raft card's snapshot. That
// is one event of one widget, and the fix it exercises is an absence: the name
// is missing from the list the live library was given. An absence is exactly
// the kind of fix that can be right for one name and wrong for the next — a
// generator that split the wrong widget's events, a host that appended one
// back — so "a stream-delivered name is never browser-sendable" is worth
// asserting on a second name, in a second widget, with a different payload.
//
// `widget.node-status.health` is that name. It is the whole of the node card's
// input: one flag, written by one event, delivered by one declared stream. In
// this host's probe configuration the health source is deliberately not wired,
// so the node region never moves on its own — which makes any movement in it
// attributable to the browser that forged the event.
var _ = Describe("A browser forging a second widget's stream-delivered event", func() {
	forgeHealth := func(client *livetest.Client) uint64 {
		GinkgoHelper()
		return client.Send(
			nodestatus.NodeStatusEventHealth,
			nodestatus.NodeStatusRegion,
			map[string]string{
				nodestatus.NodeStatusEventHealthFieldReachable: "true",
			})
	}

	It("is refused with the same default-deny the raft card's snapshot gets", func() {
		client := dialProbe(probeApp(probeCluster()))
		Expect(client.Snapshot().Kind).To(Equal(livetest.FrameSnapshot))

		clientRef := forgeHealth(client)

		refusal := client.Await("the server's answer to the forged health event",
			probePatience, func(frame *livetest.Frame) bool {
				if frame.Patch != nil {
					client.Ack(frame.Patch.ServerSeq)
					_, carried := frame.Patch.Fragment(nodestatus.NodeStatusRegion)
					return carried
				}
				return frame.Kind == livetest.FrameError && frame.Error.ClientRef == clientRef
			})

		Expect(refusal.Kind).To(Equal(livetest.FrameError),
			"the node card moved, so the browser wrote the health check's own truth")
		Expect(refusal.Error.Code).To(BeNumerically("==", errorCodeUnknownEvent))
		Expect(refusal.Error.Fatal).To(BeFalse())
	})

	It("leaves the node card reading exactly what the mount rendered", func() {
		client := dialProbe(probeApp(probeCluster()))

		mounted, carried := client.Snapshot().Patch.Fragment(nodestatus.NodeStatusRegion)
		Expect(carried).To(BeTrue())
		// The zero flag: nothing has told this card anything, and in this
		// configuration nothing will.
		Expect(mounted).To(ContainSubstring("unreachable"))

		forgeHealth(client)

		// Every frame this connection ever carried, the ones before the refusal
		// included: a forgery applied early would have been rendered before the
		// refusal could arrive.
		frames := append(client.Received(), client.Settle(250*time.Millisecond)...)
		for _, frame := range frames {
			if frame.Patch == nil {
				continue
			}
			html, carried := frame.Patch.Fragment(nodestatus.NodeStatusRegion)
			if !carried {
				continue
			}
			Expect(html).To(ContainSubstring("unreachable"),
				"the node card is reporting a health check nothing performed")
			Expect(strings.Count(html, "reachable")).
				To(Equal(strings.Count(html, "unreachable")),
					"every occurrence of `reachable` is inside an `unreachable`")
		}
	})

	It("keeps routing the name the declared stream delivers", func() {
		// The half a fix like this breaks silently, on this widget too: an
		// unregistered name is refused from a browser and must still reach the
		// widget when the host's own effect emits it.
		registration := nodestatus.NewNodeStatus[live.AnonymousIdentity]().Register()

		Expect(registration.Internal).To(ContainElement(nodestatus.NodeStatusEventHealth))
		Expect(registration.Events).To(BeEmpty(),
			"this widget has no control, so it has nothing a browser may send")

		delivered := live.Event{
			Name: nodestatus.NodeStatusEventHealth,
			Fields: live.NewFields(map[string]string{
				nodestatus.NodeStatusEventHealthFieldReachable: "true",
			}),
		}
		state, _ := nodestatus.NewNodeStatus[live.AnonymousIdentity]().Reduce(nodestatus.NodeStatusState{}, delivered)
		Expect(state.Reachable).To(BeTrue(),
			"the stream's own event still routes; what changed is who may send it")
	})
})
