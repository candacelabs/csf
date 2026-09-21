package session_test

import (
	"context"
	"fmt"
	"io"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/internal/render"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
)

const collectionWaitBudget = 10 * time.Second

var _ = Describe("Serialized dynamic membership", func() {
	It("queues a newly visible child's event while the parent's send is still completing", func() {
		rendered := make(chan struct{}, 1)
		parent := counterFragment()
		parent.Dirty = func(previous, next any) bool { return false }
		parent.Render = func(ctx context.Context, state any, writer io.Writer) error {
			value := state.(counterState).N
			if value == 1 {
				rendered <- struct{}{}
			}
			_, err := fmt.Fprintf(writer, "<div>parent %d</div>", value)
			return err
		}
		parent.Children = func(state any) []render.Fragment {
			if state.(counterState).N == 0 {
				return nil
			}
			return []render.Fragment{{
				ID: "counter:one",
				Render: func(ctx context.Context, state any, writer io.Writer) error {
					_, err := fmt.Fprintf(writer, "<div>child %d</div>", state.(counterState).N)
					return err
				},
			}}
		}
		app := newTestApp(parent)
		h := newHarness(app, session.DefaultLimits())
		h.start()
		DeferCleanup(h.stop)
		release := make(chan struct{})
		DeferCleanup(func() {
			select {
			case <-release:
			default:
				close(release)
			}
		})
		h.sink.mu.Lock()
		h.sink.block = release
		h.sink.mu.Unlock()
		h.sendEvent("counter.increment")
		Eventually(rendered, collectionWaitBudget).Should(Receive())
		queued := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			h.send(&pb.Frame{
				ProtocolVersion: protocol.Version, SessionId: sessionIDBytes(),
				Payload: &pb.Frame_Event{Event: &pb.Event{
					ClientRef: h.ref(), Name: "counter.increment", FragmentId: "counter:one", SeenServerSeq: 2,
				}},
			})
			close(queued)
		}()
		Eventually(queued, collectionWaitBudget).Should(BeClosed(), "namespace admission must not read stale renderer membership")
		close(release)
		Eventually(h.sink.patches, collectionWaitBudget).Should(HaveLen(2))
		Expect(h.sink.errors()).To(BeEmpty())
		patches := h.sink.patches()
		Expect(patches[0].GetUpdates()[0].GetFragmentId()).To(Equal("counter"))
		Expect(patches[1].GetUpdates()[0].GetFragmentId()).To(Equal("counter:one"))
		Expect(patches[1].GetUpdates()[0].GetHtml()).To(ContainSubstring("child 2"))

		h.send(&pb.Frame{
			ProtocolVersion: protocol.Version, SessionId: sessionIDBytes(),
			Payload: &pb.Frame_Event{Event: &pb.Event{
				ClientRef: h.ref(), Name: "counter.increment", FragmentId: "counter:missing", SeenServerSeq: 3,
			}},
		})
		Eventually(h.sink.errors, collectionWaitBudget).Should(HaveLen(1))
		Expect(h.sink.errors()[0].GetCode()).To(Equal(pb.ErrorCode_UNKNOWN_FRAGMENT))
		Expect(app.reducedNames()).To(HaveLen(2), "an unknown dynamic member reached reduction")
	})
})
