package render_test

import (
	"context"
	"fmt"
	"io"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/render"
)

type collectionEntry struct {
	ID string
	N  int
}

type collectionState struct {
	Items []collectionEntry
	Lane  string
}

func collectionFragment(rendered *[]string) render.Fragment {
	return render.Fragment{
		ID: "cards",
		Render: func(ctx context.Context, state any, writer io.Writer) error {
			*rendered = append(*rendered, "cards")
			_, err := fmt.Fprintf(writer, "<div>%v</div>", state.(collectionState))
			return err
		},
		Dirty: func(previous, next any) bool { return previous.(collectionState).Lane != next.(collectionState).Lane },
		Children: func(state any) []render.Fragment {
			var children []render.Fragment
			for _, entry := range state.(collectionState).Items {
				children = append(children, render.Fragment{
					ID: entry.ID,
					Render: func(ctx context.Context, state any, writer io.Writer) error {
						*rendered = append(*rendered, entry.ID)
						if entry.N < 0 {
							return fmt.Errorf("child render failure")
						}
						_, err := fmt.Fprintf(writer, "<article>%d</article>", entry.N)
						return err
					},
					Dirty: func(previous, next any) bool {
						before := previous.(collectionState).Items
						index := slices.IndexFunc(before, func(item collectionEntry) bool { return item.ID == entry.ID })
						return index < 0 || before[index].N != entry.N
					},
				})
			}
			return children
		},
	}
}

var _ = Describe("Dynamic child regions", func() {
	var (
		rendered []string
		view     *render.Renderer
		initial  collectionState
	)
	BeforeEach(func() {
		rendered = nil
		view = mustRegistry(collectionFragment(&rendered)).NewRenderer()
		initial = collectionState{Items: []collectionEntry{{"cards:a", 1}, {"cards:b", 2}}}
	})

	It("publishes membership only after transport success and targets one dirty child", func() {
		first := view.RenderAll(context.Background(), initial)
		Expect(ids(first.Updates)).To(Equal([]string{"cards"}))
		Expect(view.KnownID("cards:a")).To(BeFalse())
		view.Commit(first)
		Expect(view.KnownID("cards:a")).To(BeTrue())
		rendered = nil
		next := collectionState{Items: []collectionEntry{{"cards:a", 3}, {"cards:b", 2}}}
		Expect(view.Mark(initial, next)).To(BeEmpty())
		patch := view.Render(context.Background(), next)
		Expect(ids(patch.Updates)).To(Equal([]string{"cards:a"}))
		Expect(rendered).To(Equal([]string{"cards:a"}))
		view.Commit(patch)
		view.Mark(next, next)
		Expect(view.Render(context.Background(), next).Updates).To(BeEmpty())
	})

	It("adds removes and reorders through a single parent update and forgets removed identities", func() {
		view.Commit(view.RenderAll(context.Background(), initial))
		states := []collectionState{
			{Items: []collectionEntry{{"cards:a", 1}, {"cards:b", 2}, {"cards:c", 3}}},
			{Items: []collectionEntry{{"cards:c", 3}, {"cards:a", 1}, {"cards:b", 2}}},
			{Items: []collectionEntry{{"cards:c", 3}, {"cards:b", 2}}},
			{},
		}
		previous := initial
		for _, next := range states {
			view.Mark(previous, next)
			patch := view.Render(context.Background(), next)
			Expect(ids(patch.Updates)).To(Equal([]string{"cards"}))
			view.Commit(patch)
			previous = next
		}
		Expect(view.KnownID("cards:a")).To(BeFalse())
		Expect(view.KnownID("cards:b")).To(BeFalse())
		Expect(view.KnownID("cards")).To(BeTrue())
	})

	It("coalesces membership churn into the latest complete parent", func() {
		view.Commit(view.RenderAll(context.Background(), initial))
		added := collectionState{Items: []collectionEntry{{"cards:temporary", 3}}}
		removed := collectionState{}
		latest := collectionState{Items: []collectionEntry{{"cards:final", 4}}}
		view.Mark(initial, added)
		view.Mark(added, removed)
		view.Mark(removed, latest)
		patch := view.Render(context.Background(), latest)
		Expect(ids(patch.Updates)).To(Equal([]string{"cards"}))
		Expect(patch.Updates[0].HTML).To(ContainSubstring("cards:final"))
		Expect(patch.Updates[0].HTML).NotTo(ContainSubstring("cards:temporary"))
		view.Commit(patch)
		Expect(view.KnownID("cards:final")).To(BeTrue())
		Expect(view.KnownID("cards:a")).To(BeFalse())
		Expect(view.KnownID("cards:temporary")).To(BeFalse())
	})

	It("retries failed child or structural sends without publishing their hashes or members", func() {
		view.Commit(view.RenderAll(context.Background(), initial))
		next := collectionState{Items: []collectionEntry{{"cards:a", 3}, {"cards:b", 2}}}
		view.Mark(initial, next)
		patch := view.Render(context.Background(), next)
		view.Discard(patch)
		retry := view.Render(context.Background(), next)
		Expect(ids(retry.Updates)).To(Equal([]string{"cards:a"}))
		view.Commit(retry)
		next = collectionState{Items: []collectionEntry{{"cards:new", 4}}}
		view.Mark(initial, next)
		patch = view.Render(context.Background(), next)
		view.Discard(patch)
		Expect(view.KnownID("cards:new")).To(BeFalse())
		Expect(view.KnownID("cards:a")).To(BeTrue())
		view.Commit(view.Render(context.Background(), next))
		Expect(view.KnownID("cards:new")).To(BeTrue())
		Expect(view.KnownID("cards:a")).To(BeFalse())
	})

	It("retries a healthy targeted child without rerunning a failing sibling", func() {
		view.Commit(view.RenderAll(context.Background(), initial))
		next := collectionState{Items: []collectionEntry{{"cards:a", 3}, {"cards:b", -1}}}
		view.Mark(initial, next)
		patch := view.Render(context.Background(), next)
		Expect(ids(patch.Updates)).To(Equal([]string{"cards:a"}))
		Expect(patch.Failed).To(HaveLen(1))
		view.Discard(patch)
		rendered = nil
		retry := view.Render(context.Background(), next)
		Expect(ids(retry.Updates)).To(Equal([]string{"cards:a"}))
		Expect(retry.Failed).To(BeEmpty())
		Expect(rendered).To(Equal([]string{"cards:a"}))
	})

	It("resyncs the whole parent after targeted updates and honors extra structural dirtiness", func() {
		view.Commit(view.RenderAll(context.Background(), initial))
		next := collectionState{Items: []collectionEntry{{"cards:a", 3}, {"cards:b", 2}}}
		view.Mark(initial, next)
		view.Commit(view.Render(context.Background(), next))
		resync := view.RenderAll(context.Background(), next)
		Expect(ids(resync.Updates)).To(Equal([]string{"cards"}))
		Expect(resync.Updates[0].HTML).To(ContainSubstring("cards:a 3"))
		view.Commit(resync)
		moved := next
		moved.Lane = "done"
		view.Mark(next, moved)
		Expect(ids(view.Render(context.Background(), moved).Updates)).To(Equal([]string{"cards"}))
	})

	It("does not suppress a parent whose old hash predates a committed child update", func() {
		view.Commit(view.RenderAll(context.Background(), initial))
		next := collectionState{Items: []collectionEntry{{"cards:a", 3}, {"cards:b", 2}}}
		view.Mark(initial, next)
		view.Commit(view.Render(context.Background(), next))
		Expect(view.MarkID("cards")).To(BeTrue())
		reverted := view.Render(context.Background(), initial)
		Expect(ids(reverted.Updates)).To(Equal([]string{"cards"}))
		Expect(reverted.Suppressed).To(BeEmpty())
		view.Commit(reverted)

		Expect(view.MarkID("cards")).To(BeTrue())
		unchanged := view.Render(context.Background(), initial)
		Expect(unchanged.Updates).To(BeEmpty())
		Expect(unchanged.Suppressed).To(Equal([]string{"cards"}))
		Expect(view.Pending()).To(BeFalse())
	})

	It("preserves child order and observes suppressed children without committing changes", func() {
		initial.Items = []collectionEntry{{"cards:b", 2}, {"cards:a", 1}}
		view.Commit(view.RenderAll(context.Background(), initial))
		var suppressedIDs []string
		view.Observe(func(ctx context.Context, id string) (context.Context, func(suppressed, failed bool)) {
			return ctx, func(suppressed, failed bool) {
				Expect(failed).To(BeFalse())
				if suppressed {
					suppressedIDs = append(suppressedIDs, id)
				}
			}
		})
		view.MarkID("cards:a")
		view.MarkID("cards:b")
		unchanged := view.Render(context.Background(), initial)
		Expect(unchanged.Updates).To(BeEmpty())
		Expect(unchanged.Suppressed).To(Equal([]string{"cards:b", "cards:a"}))
		Expect(suppressedIDs).To(Equal(unchanged.Suppressed))
		view.Commit(unchanged)
		Expect(view.Pending()).To(BeFalse())

		next := collectionState{Items: []collectionEntry{{"cards:b", 3}, {"cards:a", 4}}}
		view.Mark(initial, next)
		patch := view.Render(context.Background(), next)
		Expect(patch.Updates).To(Equal([]render.Update{
			{FragmentID: "cards:b", Op: render.OpMorph, HTML: "<article>3</article>"},
			{FragmentID: "cards:a", Op: render.OpMorph, HTML: "<article>4</article>"},
		}))
	})

	It("discards staged child hashes and membership when the parent render fails", func() {
		parent := collectionFragment(&rendered)
		renderParent := parent.Render
		failParent := false
		parent.Render = func(ctx context.Context, state any, writer io.Writer) error {
			if failParent {
				return fmt.Errorf("parent render failure")
			}
			return renderParent(ctx, state, writer)
		}
		view = mustRegistry(parent).NewRenderer()
		view.Commit(view.RenderAll(context.Background(), initial))
		next := collectionState{Items: []collectionEntry{{"cards:a", 3}, {"cards:b", 2}, {"cards:new", 4}}}
		failParent = true
		view.Mark(initial, next)
		failed := view.Render(context.Background(), next)
		Expect(failed.Updates).To(BeEmpty())
		Expect(failed.Failed).To(HaveLen(1))
		Expect(failed.Failed[0].FragmentID).To(Equal("cards"))
		view.Commit(failed)
		Expect(view.KnownID("cards:new")).To(BeFalse())
		Expect(view.KnownID("cards:a")).To(BeTrue())
		Expect(view.Pending()).To(BeFalse())

		failParent = false
		next.Items = next.Items[:2]
		view.MarkID("cards:a")
		Expect(ids(view.Render(context.Background(), next).Updates)).To(Equal([]string{"cards:a"}))
	})

	DescribeTable("identifies invalid child declarations before rendering",
		func(child render.Fragment, message string) {
			parent := collectionFragment(&rendered)
			parent.Children = func(state any) []render.Fragment { return []render.Fragment{child} }
			view = mustRegistry(parent).NewRenderer()
			result := view.RenderAll(context.Background(), initial)
			Expect(result.Failed).To(HaveLen(1))
			Expect(result.Failed[0].Site).To(Equal("children"))
			Expect(result.Failed[0].Value).To(MatchError(ContainSubstring(message)))
			Expect(result.Updates).To(BeEmpty())
			Expect(rendered).To(BeEmpty())
		},
		Entry("missing render", render.Fragment{ID: "cards:a"}, "must declare Render"),
		Entry("nested collection", render.Fragment{
			ID: "cards:a", Render: countFragment().Render,
			Children: func(state any) []render.Fragment { return nil },
		}, "cannot declare Children"),
	)

	It("rejects duplicate children and escaped namespaces without corrupting committed membership", func() {
		view.Commit(view.RenderAll(context.Background(), initial))
		for _, items := range [][]collectionEntry{
			{{"cards:a", 1}, {"cards:a", 2}}, {{"other:a", 1}}, {{"cards:", 1}}, {{"cards:bad/key", 1}},
		} {
			invalid := collectionState{Items: items}
			Expect(view.Mark(initial, invalid)).To(HaveLen(1))
			result := view.RenderAll(context.Background(), invalid)
			Expect(result.Failed).To(HaveLen(1))
			Expect(result.Updates).To(BeEmpty())
			view.Commit(result)
			Expect(view.KnownID("cards:a")).To(BeTrue())
		}
	})

	It("rejects overlapping collection and static namespaces at startup", func() {
		child := countFragment()
		child.ID = "cards:a"
		_, err := render.NewRegistry([]render.Fragment{collectionFragment(&rendered), child})
		Expect(err).To(MatchError(ContainSubstring("overlaps the child namespace")))
	})
})
