package widget_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/a-h/templ"
	"github.com/candacelabs/csf/pkg/widget/internal/mocks"
	"go.uber.org/mock/gomock"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/examples/widget/clusterheartbeats"
	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
	"github.com/candacelabs/csf/pkg/widget"
)

type keyedCardState = clusterheartbeats.ClusterHeartbeatsState
type keyedCards = widget.KeyedCollection[keyedCardState, live.AnonymousIdentity, *clusterheartbeats.ClusterHeartbeats[live.AnonymousIdentity]]

const keyedTestBudget = 10 * time.Second

func testKeyedCards() *keyedCards {
	GinkgoHelper()
	cards, err := widget.NewKeyedCollection[keyedCardState, live.AnonymousIdentity](
		"board.cards", clusterheartbeats.NewClusterHeartbeatsAt[live.AnonymousIdentity])
	Expect(err).NotTo(HaveOccurred())
	return cards
}

func testCardState(cards *keyedCards, keys ...string) widget.KeyedState[keyedCardState] {
	GinkgoHelper()
	items := make([]widget.KeyedItem[keyedCardState], len(keys))
	for index, key := range keys {
		items[index] = widget.KeyedItem[keyedCardState]{Key: key, State: keyedCardState{Term: int64(index + 1)}}
	}
	state, err := cards.State(items)
	Expect(err).NotTo(HaveOccurred())
	return state
}

// compactKeyedCard is a host rendering adapter around the actual generated
// widget. Its small markup lets the bulk test isolate frame-count behavior.
type compactKeyedCard struct {
	*clusterheartbeats.ClusterHeartbeats[live.AnonymousIdentity]
}

func (card *compactKeyedCard) Render(state keyedCardState) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		_, err := fmt.Fprintf(writer, `<article data-gotth-region="%s">%t</article>`, card.Register().Region, state.Paused)
		return err
	})
}

var _ = Describe("Keyed generated widget collections", func() {
	It("renders two instances and routes reduction/snapshot to the exact live member", func() {
		cards := testKeyedCards()
		state := testCardState(cards, "a", "b")
		markup := render(cards.Render(state))
		for _, key := range []string{"a", "b"} {
			Expect(strings.Count(markup, `data-gotth-region="board.cards:`+key+`"`)).To(Equal(1))
			Expect(strings.Count(markup, `id="board.cards:`+key+`-title"`)).To(Equal(1))
		}
		next, effects := cards.Reduce(state, live.Event{
			Name: clusterheartbeats.ClusterHeartbeatsEventToggleMotion, FragmentID: "board.cards:a",
		})
		Expect(effects).To(BeEmpty())
		a, _ := next.Get("a")
		b, _ := next.Get("b")
		before, _ := state.Get("a")
		Expect(a.Paused).To(BeTrue())
		Expect(b.Paused).To(BeFalse())
		Expect(before.Paused).To(BeFalse())
		snapshot, exists := cards.Snapshot(next, "a")
		Expect(exists).To(BeTrue())
		Expect(snapshot.Widget).To(Equal(clusterheartbeats.ClusterHeartbeatsName))
		Expect(snapshot.Fields).To(ContainElement(widget.SnapshotField{Name: "paused", Value: "true"}))
		for _, event := range []live.Event{
			{Name: "wrong.event", FragmentID: "board.cards:a"},
			{Name: clusterheartbeats.ClusterHeartbeatsEventToggleMotion, FragmentID: "other:a"},
			{Name: clusterheartbeats.ClusterHeartbeatsEventToggleMotion, FragmentID: "board.cards:missing"},
		} {
			unchanged, effects := cards.Reduce(next, event)
			Expect(unchanged).To(Equal(next))
			Expect(effects).To(BeEmpty())
		}
		removed := next.Remove("a")
		unchanged, _ := cards.Reduce(removed, live.Event{Name: clusterheartbeats.ClusterHeartbeatsEventToggleMotion, FragmentID: "board.cards:a"})
		Expect(unchanged).To(Equal(removed))
	})

	It("rejects factories that change a member's name, event set or assigned region", func() {
		for _, mutate := range []func(registration *widget.Registration){
			func(registration *widget.Registration) { registration.Name = "Other" },
			func(registration *widget.Registration) { registration.Events = []string{"other.event"} },
			func(registration *widget.Registration) { registration.Region = "other.region" },
		} {
			controller := gomock.NewController(GinkgoT())
			cards, err := widget.NewKeyedCollection[keyedCardState, live.AnonymousIdentity]("board.cards", func(region string) *mocks.MockIWidget[keyedCardState, live.AnonymousIdentity] {
				registration := clusterheartbeats.NewClusterHeartbeatsAt[live.AnonymousIdentity](region).Register()
				if region == "board.cards:b" {
					mutate(&registration)
				}
				return stub[keyedCardState](controller, registration)
			})
			Expect(err).NotTo(HaveOccurred())
			state, err := cards.State([]widget.KeyedItem[keyedCardState]{{Key: "b"}})
			Expect(err).NotTo(HaveOccurred())
			Expect(func() { cards.Children(state) }).To(PanicWith(MatchError(ContainSubstring("changed its definition registration"))))
		}
	})

	It("broadcasts reserved runtime degradation and recovery notices to current members", func() {
		cards := testKeyedCards()
		state := testCardState(cards, "a", "b")
		degraded, _ := cards.Reduce(state, live.Event{Name: live.SlowClientEvent})
		for _, item := range degraded.Items() {
			Expect(item.State.Degraded).To(BeTrue())
		}
		recovered, _ := cards.Reduce(degraded, live.Event{Name: live.ClientRecoveredEvent})
		for _, item := range recovered.Items() {
			Expect(item.State.Degraded).To(BeFalse())
		}
		Expect(cards.Events()).NotTo(ContainElement(live.SlowClientEvent))
	})

	It("keeps ordered state immutable and rejects collisions, invalid keys and incomplete reorders", func() {
		cards := testKeyedCards()
		state := testCardState(cards, "a", "b")
		items := state.Items()
		items[0].Key = "tampered"
		Expect(state.Items()[0].Key).To(Equal("a"))
		added, err := state.Upsert("c", keyedCardState{Term: 3})
		Expect(err).NotTo(HaveOccurred())
		reordered, err := added.Reorder([]string{"c", "a", "b"})
		Expect(err).NotTo(HaveOccurred())
		Expect(reordered.Items()[0].Key).To(Equal("c"))
		Expect(state.Items()).To(HaveLen(2))
		Expect(reordered.Remove("a").Items()).To(HaveLen(2))
		for _, keys := range [][]string{{"a"}, {"a", "a"}, {"a", "missing"}} {
			_, err := state.Reorder(keys)
			Expect(err).To(HaveOccurred())
		}
		for _, keys := range [][]string{{"a", "a"}, {"a/b"}, {""}, {strings.Repeat("a", 64)}} {
			items := make([]widget.KeyedItem[keyedCardState], len(keys))
			for index, key := range keys {
				items[index].Key = key
			}
			_, err := cards.State(items)
			Expect(err).To(HaveOccurred())
		}
		Expect(cards.Events()).To(Equal([]string{clusterheartbeats.ClusterHeartbeatsEventToggleMotion}))
	})

	It("serves targeted changes and structural snapshots over one socket, rejects stale IDs, reconnects and cancels subscriptions", func() {
		cards := testKeyedCards()
		initial := testCardState(cards, "a", "b")
		var loaded atomic.Pointer[widget.KeyedState[keyedCardState]]
		loaded.Store(&initial)
		changes := make(chan struct{}, 1)
		started := make(chan struct{}, 2)
		stopped := make(chan struct{}, 2)
		teardown := make(chan struct{}, 2)
		config := live.Config[widget.KeyedState[keyedCardState], live.AnonymousIdentity]{
			Init: func(ctx context.Context, session live.Session[live.AnonymousIdentity]) (widget.KeyedState[keyedCardState], []live.Effect[live.AnonymousIdentity], error) {
				effect := live.Effect[live.AnonymousIdentity]{Source: "board.subscription", Run: func(ctx context.Context, session live.Session[live.AnonymousIdentity], emit live.Emitter) error {
					started <- struct{}{}
					defer func() { stopped <- struct{}{} }()
					for {
						select {
						case <-ctx.Done():
							return nil
						case <-changes:
							payload, err := json.Marshal(loaded.Load().Items())
							if err != nil {
								return err
							}
							if err := emit(live.Event{Name: "board.reload", Fields: live.NewFields(map[string]string{"snapshot": string(payload)})}); err != nil {
								return err
							}
						}
					}
				}}
				return *loaded.Load(), []live.Effect[live.AnonymousIdentity]{effect}, nil
			},
			Reduce: func(state widget.KeyedState[keyedCardState], event live.Event) (widget.KeyedState[keyedCardState], []live.Effect[live.AnonymousIdentity]) {
				if event.Name == "board.reload" {
					var items []widget.KeyedItem[keyedCardState]
					if err := json.Unmarshal([]byte(event.Fields.Get("snapshot")), &items); err != nil {
						return state, nil
					}
					next, err := cards.State(items)
					if err != nil {
						return state, nil
					}
					return next, nil
				}
				return cards.Reduce(state, event)
			},
			Fragments: []live.Fragment[widget.KeyedState[keyedCardState]]{cards.Fragment()}, Events: cards.Events(),
			Origins: []string{"http://localhost"}, Authenticate: live.Anonymous,
			Authorize: live.AllowAll[live.AnonymousIdentity], CSRF: live.NoCSRFCheck,
			Teardown: func(ctx context.Context, session live.Session[live.AnonymousIdentity], state widget.KeyedState[keyedCardState]) {
				teardown <- struct{}{}
			},
		}
		app, err := live.New(config)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Close(context.Background())).To(Succeed()) })
		options := livetest.ClientOptions{Path: "/live", Origin: "http://localhost"}
		client := livetest.NewClient(GinkgoTB(), app.Handler(), options)
		Eventually(started, keyedTestBudget).Should(Receive())
		Expect(client.Snapshot().Patch.FragmentIDs()).To(Equal([]string{"board.cards"}))
		client.Ack(client.Snapshot().Patch.ServerSeq)

		client.Send(clusterheartbeats.ClusterHeartbeatsEventToggleMotion, "board.cards:a", nil)
		patch := client.Await("only card a to change", keyedTestBudget, func(frame *livetest.Frame) bool { return frame.Kind == livetest.FramePatch })
		Expect(patch.Patch.FragmentIDs()).To(Equal([]string{"board.cards:a"}))
		client.Ack(patch.Patch.ServerSeq)
		client.Send(clusterheartbeats.ClusterHeartbeatsEventToggleMotion, "board.cards:b", nil)
		patch = client.Await("only card b to change", keyedTestBudget, func(frame *livetest.Frame) bool { return frame.Kind == livetest.FramePatch })
		Expect(patch.Patch.FragmentIDs()).To(Equal([]string{"board.cards:b"}))
		client.Ack(patch.Patch.ServerSeq)
		client.Send(clusterheartbeats.ClusterHeartbeatsEventSnapshot, "board.cards:a", nil)
		refused := client.Await("internal event refusal", keyedTestBudget, func(frame *livetest.Frame) bool { return frame.Kind == livetest.FrameError })
		Expect(refused.Error.Code).To(Equal(int32(4)))

		for _, keys := range [][]string{{"a", "b", "c"}, {"c", "b", "a"}, {"c", "b"}} {
			next := testCardState(cards, keys...)
			loaded.Store(&next)
			changes <- struct{}{}
			patch = client.Await("one structural parent patch", keyedTestBudget, func(frame *livetest.Frame) bool { return frame.Kind == livetest.FramePatch })
			Expect(patch.Patch.FragmentIDs()).To(Equal([]string{"board.cards"}))
			markup := patch.Patch.Updates[0].HTML
			positions := make([]int, len(keys))
			for index, key := range keys {
				positions[index] = strings.Index(markup, `data-gotth-region="board.cards:`+key+`"`)
			}
			Expect(slices.IsSorted(positions)).To(BeTrue())
			client.Ack(patch.Patch.ServerSeq)
		}
		client.Send(clusterheartbeats.ClusterHeartbeatsEventToggleMotion, "board.cards:a", nil)
		refused = client.Await("removed region refusal", keyedTestBudget, func(frame *livetest.Frame) bool { return frame.Kind == livetest.FrameError })
		Expect(refused.Error.Code).To(Equal(int32(5)))
		// Leave one delivered patch unacknowledged so the resync describes a
		// real gap; already acknowledged cursors correctly receive an Ack.
		client.Send(clusterheartbeats.ClusterHeartbeatsEventToggleMotion, "board.cards:b", nil)
		client.Await("an unacknowledged card patch", keyedTestBudget, func(frame *livetest.Frame) bool { return frame.Kind == livetest.FramePatch })
		client.Resync(1, 1)
		snapshot := client.Await("complete collection resync", keyedTestBudget, func(frame *livetest.Frame) bool { return frame.Kind == livetest.FrameSnapshot })
		Expect(snapshot.Patch.FragmentIDs()).To(Equal([]string{"board.cards"}))
		Expect(snapshot.Patch.Updates[0].HTML).NotTo(ContainSubstring(`data-gotth-region="board.cards:a"`))
		Expect(client.Close()).To(Succeed())
		Eventually(stopped, keyedTestBudget).Should(Receive())
		Eventually(teardown, keyedTestBudget).Should(Receive())
		reconnected := livetest.NewClient(GinkgoTB(), app.Handler(), options)
		Eventually(started, keyedTestBudget).Should(Receive())
		Expect(reconnected.Snapshot().Patch.Updates[0].HTML).To(ContainSubstring(`data-gotth-region="board.cards:c"`))
		Expect(reconnected.Snapshot().Patch.Updates[0].HTML).NotTo(ContainSubstring(`data-gotth-region="board.cards:a"`))
		Expect(reconnected.Close()).To(Succeed())
		Eventually(stopped, keyedTestBudget).Should(Receive())
		Eventually(teardown, keyedTestBudget).Should(Receive())
	})

	It("collapses more than the protocol update bound to a complete parent patch", func() {
		cards, err := widget.NewKeyedCollection[keyedCardState, live.AnonymousIdentity]("board.cards", func(region string) *compactKeyedCard {
			return &compactKeyedCard{ClusterHeartbeats: clusterheartbeats.NewClusterHeartbeatsAt[live.AnonymousIdentity](region)}
		})
		Expect(err).NotTo(HaveOccurred())
		keys := make([]string, 65)
		for index := range keys {
			keys[index] = fmt.Sprintf("card%d", index)
		}
		items := make([]widget.KeyedItem[keyedCardState], len(keys))
		for index, key := range keys {
			items[index].Key = key
		}
		initial, err := cards.State(items)
		Expect(err).NotTo(HaveOccurred())
		config := live.Config[widget.KeyedState[keyedCardState], live.AnonymousIdentity]{
			Init: func(ctx context.Context, session live.Session[live.AnonymousIdentity]) (widget.KeyedState[keyedCardState], []live.Effect[live.AnonymousIdentity], error) {
				return initial, nil, nil
			},
			Reduce: func(state widget.KeyedState[keyedCardState], event live.Event) (widget.KeyedState[keyedCardState], []live.Effect[live.AnonymousIdentity]) {
				items := state.Items()
				for index := range items {
					items[index].State.Paused = true
				}
				next, err := cards.State(items)
				Expect(err).NotTo(HaveOccurred())
				return next, nil
			},
			Fragments: []live.Fragment[widget.KeyedState[keyedCardState]]{cards.Fragment()}, Events: []string{"board.pause"},
			Origins: []string{"http://localhost"}, Authenticate: live.Anonymous, Authorize: live.AllowAll[live.AnonymousIdentity], CSRF: live.NoCSRFCheck,
		}
		app, err := live.New(config)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(app.Close(context.Background())).To(Succeed()) })
		client := livetest.NewClient(GinkgoTB(), app.Handler(), livetest.ClientOptions{Path: "/live", Origin: "http://localhost"})
		client.Ack(client.Snapshot().Patch.ServerSeq)
		client.Send("board.pause", "board.cards", nil)
		patch := client.Await("bounded bulk parent patch", keyedTestBudget, func(frame *livetest.Frame) bool { return frame.Kind == livetest.FramePatch })
		Expect(patch.Patch.FragmentIDs()).To(Equal([]string{"board.cards"}))
	})
})
