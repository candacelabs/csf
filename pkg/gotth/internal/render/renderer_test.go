package render_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/render"
)

type counter struct {
	N     int
	Label string
}

func text(format string, pick func(state counter) any) render.RenderFunc {
	return func(_ context.Context, state any, w io.Writer) error {
		_, err := fmt.Fprintf(w, format, pick(state.(counter)))
		return err
	}
}

func countFragment() render.Fragment {
	return render.Fragment{
		ID:     "counter",
		Render: text("<div>%v</div>", func(c counter) any { return c.N }),
		Dirty:  func(prev, next any) bool { return prev.(counter).N != next.(counter).N },
	}
}

func labelFragment() render.Fragment {
	return render.Fragment{
		ID:     "label",
		Render: text("<span>%v</span>", func(c counter) any { return c.Label }),
		Dirty:  func(prev, next any) bool { return prev.(counter).Label != next.(counter).Label },
	}
}

func mustRegistry(frags ...render.Fragment) *render.Registry {
	GinkgoHelper()
	reg, err := render.NewRegistry(frags)
	Expect(err).NotTo(HaveOccurred())
	return reg
}

func ids(us []render.Update) []string {
	out := make([]string, len(us))
	for i, u := range us {
		out[i] = u.FragmentID
	}
	sort.Strings(out)
	return out
}

var _ = Describe("The fragment registry", func() {

	It("refuses a duplicate identifier, naming both declarations", func() {
		_, err := render.NewRegistry([]render.Fragment{countFragment(), countFragment()})

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("fragments[0] and fragments[1]"))
		Expect(err.Error()).To(ContainSubstring(`"counter"`))
	})

	It("refuses an empty fragment set", func() {
		_, err := render.NewRegistry(nil)
		Expect(err).To(HaveOccurred())
	})

	DescribeTable("refuses an identifier the schema could not carry",
		func(id string) {
			f := countFragment()
			f.ID = id
			_, err := render.NewRegistry([]render.Fragment{f})
			Expect(err).To(HaveOccurred(), "accepted the fragment ID %q", id)
		},
		Entry("empty", ""),
		Entry("a space", "my counter"),
		Entry("a slash", "counter/one"),
		Entry("an angle bracket", "<counter>"),
		Entry("too long", string(make([]byte, 65))),
	)

	It("refuses a fragment that declares no render", func() {
		f := countFragment()
		f.Render = nil
		_, err := render.NewRegistry([]render.Fragment{f})
		Expect(err).To(HaveOccurred())
	})

	It("copies the declarations, so a caller's slice cannot be edited underneath it", func() {
		frags := []render.Fragment{countFragment()}
		reg := mustRegistry(frags...)

		frags[0].ID = "mutated"

		Expect(reg.IDs()).To(Equal([]string{"counter"}))
	})
})

var _ = Describe("A session's renderer", func() {
	var (
		ctx   context.Context
		reg   *render.Registry
		v     *render.Renderer
		state counter
	)

	BeforeEach(func() {
		ctx = context.Background()
		reg = mustRegistry(countFragment(), labelFragment())
		v = reg.NewRenderer()
		state = counter{N: 0, Label: "hits"}
	})

	It("renders everything on its first pass, having nothing to suppress against", func() {
		res := v.Render(ctx, state)

		Expect(ids(res.Updates)).To(Equal([]string{"counter", "label"}))
		Expect(res.Suppressed).To(BeEmpty())
		Expect(res.Failed).To(BeEmpty())
		Expect(v.Pending()).To(BeFalse())
	})

	// FR-19: same state, byte-identical HTML, across runs. The known hazard is
	// ranging a map in a template; this is the assertion that catches it.
	It("produces byte-identical markup for the same state, every time", func() {
		first := v.Render(ctx, state)

		for i := 0; i < 32; i++ {
			w := reg.NewRenderer()
			again := w.Render(ctx, state)
			Expect(again.Updates).To(Equal(first.Updates),
				"render %d produced different bytes for identical state", i)
		}
	})

	It("suppresses a re-render whose bytes did not move, and says so", func() {
		v.Commit(v.Render(ctx, state))
		v.MarkAll()

		res := v.Render(ctx, state)

		Expect(res.Updates).To(BeEmpty())
		Expect(res.Suppressed).To(ConsistOf("counter", "label"))
	})

	It("renders only what a transition marked", func() {
		v.Render(ctx, state)

		next := state
		next.N++
		Expect(v.Mark(state, next)).To(BeEmpty())

		res := v.Render(ctx, next)

		Expect(ids(res.Updates)).To(Equal([]string{"counter"}))
		Expect(res.Updates[0].HTML).To(Equal("<div>1</div>"))
		Expect(res.Updates[0].Op).To(Equal(render.OpMorph))
	})

	It("marks every fragment when a fragment declares no change function", func() {
		f := countFragment()
		f.Dirty = nil
		v = mustRegistry(f, labelFragment()).NewRenderer()
		v.Commit(v.Render(ctx, state))

		Expect(v.Mark(state, state)).To(BeEmpty())

		Expect(v.Pending()).To(BeTrue())
		Expect(v.Render(ctx, state).Suppressed).To(ConsistOf("counter"))
	})

	It("re-sends an unchanged fragment in a snapshot, which has nothing to morph against", func() {
		v.Render(ctx, state)

		res := v.RenderAll(ctx, state)

		Expect(ids(res.Updates)).To(Equal([]string{"counter", "label"}))
		Expect(res.Suppressed).To(BeEmpty())
	})

	Describe("when application code panics", func() {
		var boom *render.Registry

		BeforeEach(func() {
			bad := render.Fragment{
				ID:     "bad",
				Render: func(ctx context.Context, state any, w io.Writer) error { panic("render exploded") },
			}
			boom = mustRegistry(bad, countFragment())
			v = boom.NewRenderer()
		})

		It("leaves that fragment stale and patches the others", func() {
			res := v.Render(ctx, state)

			Expect(ids(res.Updates)).To(Equal([]string{"counter"}))
			Expect(res.Failed).To(HaveLen(1))
			Expect(res.Failed[0].FragmentID).To(Equal("bad"))
			Expect(res.Failed[0].Site).To(Equal("render"))
			Expect(res.Failed[0].Stack).NotTo(BeEmpty())
		})

		It("does not retry it on the next pass, which would only panic again", func() {
			v.Render(ctx, state)

			Expect(v.Pending()).To(BeFalse())
			Expect(v.Render(ctx, state).Failed).To(BeEmpty())
		})

		It("treats a change declaration that panics as a change", func() {
			bad := countFragment()
			bad.Dirty = func(prev, next any) bool { panic("dirty exploded") }
			v = mustRegistry(bad).NewRenderer()
			v.Render(ctx, state)

			failed := v.Mark(state, state)

			Expect(failed).To(HaveLen(1))
			Expect(failed[0].Site).To(Equal("dirty"))
			Expect(v.Pending()).To(BeTrue())
		})

		It("reports a render that returns an error the same way it reports a panic", func() {
			bad := render.Fragment{
				ID: "bad",
				Render: func(ctx context.Context, state any, w io.Writer) error {
					return fmt.Errorf("template failed")
				},
			}
			v = mustRegistry(bad).NewRenderer()

			res := v.Render(ctx, state)

			Expect(res.Updates).To(BeEmpty())
			Expect(res.Failed).To(HaveLen(1))
			Expect(res.Failed[0].Value).To(MatchError("template failed"))
		})
	})

	// U-6. v.buf is per-session storage reused for every fragment of every
	// pass, and it used to be what application render code received. The
	// []byte aliasing hazard from the refinement research is real here in the
	// render direction: a fragment could type-assert the writer to
	// *bytes.Buffer and keep .Bytes(), which is a live view of storage the next
	// fragment's Reset overwrites, or retain the io.Writer and write into some
	// other fragment's markup later. The library's own use is correct —
	// buf.String() copies — so the hole was only ever the one it handed out.
	Describe("the writer a fragment renders through", func() {
		It("does not expose the renderer's buffer to a type assertion", func() {
			var asserted bool
			v = mustRegistry(render.Fragment{
				ID: "counter",
				Render: func(_ context.Context, _ any, w io.Writer) error {
					_, asserted = w.(*bytes.Buffer)
					_, err := io.WriteString(w, "<div>ok</div>")
					return err
				},
			}).NewRenderer()

			res := v.Render(ctx, state)

			Expect(asserted).To(BeFalse(),
				"a fragment reached the buffer every other fragment overwrites")
			Expect(res.Updates).To(HaveLen(1))
			Expect(res.Updates[0].HTML).To(Equal("<div>ok</div>"))
		})

		It("refuses a write from a writer the fragment retained", func() {
			var escaped io.Writer
			v = mustRegistry(render.Fragment{
				ID: "counter",
				Render: func(_ context.Context, _ any, w io.Writer) error {
					escaped = w
					_, err := io.WriteString(w, "<div>ok</div>")
					return err
				},
			}).NewRenderer()

			Expect(v.Render(ctx, state).Updates).To(HaveLen(1))

			n, err := escaped.Write([]byte("<script>late</script>"))

			Expect(n).To(BeZero())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("after Render returned"))
		})

		// What this does NOT claim, stated so the next reader does not assume
		// it: within one pass every fragment renders through the same writer,
		// so a handle retained by fragment A and used by fragment B during B's
		// own render is indistinguishable from B's own and lands in B's markup,
		// which is where B was writing anyway. Narrowing that further would
		// cost a wrapper allocation per fragment per pass, and the two hazards
		// the review named — the type assertion and the write after the pass —
		// are both closed above. A goroutine writing concurrently with a render
		// is a violation of the single-goroutine render contract rather than of
		// this one.
		It("accepts the writer normally for the fragment it was handed to", func() {
			v = mustRegistry(countFragment(), labelFragment()).NewRenderer()

			res := v.Render(ctx, state)

			Expect(res.Failed).To(BeEmpty())
			Expect(ids(res.Updates)).To(Equal([]string{"counter", "label"}))
		})
	})

	It("keeps per-session state per session", func() {
		other := reg.NewRenderer()
		v.Commit(v.Render(ctx, state))

		res := other.Render(ctx, state)

		Expect(res.Suppressed).To(BeEmpty(),
			"a second session suppressed a render on the strength of the first session's hashes")
	})

	It("marks a fragment by name, and reports an undeclared one", func() {
		v.Render(ctx, state)

		Expect(v.MarkID("counter")).To(BeTrue())
		Expect(v.MarkID("nope")).To(BeFalse())
		Expect(v.Pending()).To(BeTrue())
	})
})

var _ = Describe("The dirty set", func() {
	// The set is a word-per-64-fragments bitset because it is per-session
	// state. The behaviour that matters is that it survives a fragment count
	// that is not a multiple of the word size.
	It("holds every fragment across a word boundary", func() {
		var frags []render.Fragment
		for i := 0; i < 130; i++ {
			f := countFragment()
			f.ID = fmt.Sprintf("f%d", i)
			f.Dirty = nil
			frags = append(frags, f)
		}
		v := mustRegistry(frags...).NewRenderer()

		first := v.Render(context.Background(), counter{})
		Expect(first.Updates).To(HaveLen(130))
		Expect(v.Pending()).To(BeFalse())
		v.Commit(first)

		v.MarkAll()
		Expect(v.Render(context.Background(), counter{}).Suppressed).To(HaveLen(130))
	})
})
