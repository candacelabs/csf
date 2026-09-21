package live_test

import (
	"time"

	"github.com/a-h/templ"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The emission half of FR-54 failure 2.
//
// docs/gates/phase-4.md §5.6 row 2, driven by QA-1 in Chromium
// (docs/qa/fr-54-debounce-repro.md, verdict REPRODUCES): Fields, Debounce and
// Throttle used to be attributes of the ELEMENT, so composing two bindings
// changed what one of them did. The guide's own composer put an Escape binding
// beside a 150 ms input binding and the Escape inherited the interval — and a
// keystroke inside the window did not delay the clear, it destroyed it.
//
// These specs are about where the value LANDS, which is the half the server
// owns. The half that reads it is client/test/binding.test.mjs, and the pair
// only means something together: the attribute spellings are a contract with
// the client runtime, and a disagreement between the two is a silent no-op in
// the browser rather than an error anywhere.
var _ = Describe("Per-binding options in the binding grammar (FR-54 failure 2)", func() {
	// The rule, in one sentence: an option a binding declares is a component
	// of that binding, so no other binding on the element can read it.
	It("emits no element-level option attribute at all", func() {
		attrs := live.OnWith("input", "search.query", live.Bind{
			Fields:   map[string]string{"scope": "all", "page": "1"},
			Debounce: 250 * time.Millisecond,
			Throttle: time.Second,
		})

		Expect(attrs).To(HaveLen(1),
			"every option a binding declares travels inside data-gotth-on")
		Expect(attrs).NotTo(HaveKey("data-gotth-fields"))
		Expect(attrs).NotTo(HaveKey("data-gotth-debounce"))
		Expect(attrs).NotTo(HaveKey("data-gotth-throttle"))
	})

	// The grammar is domEvent:eventName:key:debounce:throttle:fields, with
	// trailing empty components trimmed. The key keeps the position it has had
	// since 591c275a; the three that follow are new and reserve nothing new,
	// because ":" was already spent on this grammar and Bind.Keys already says
	// a key cannot be one.
	It("spells the options as components of the binding, in grammar order", func() {
		Expect(live.OnWith("input", "search.query", live.Bind{
			Fields:   map[string]string{"scope": "all", "page": "1"},
			Debounce: 250 * time.Millisecond,
			Throttle: time.Second,
		})["data-gotth-on"]).To(Equal("input:search.query::250:1000:page=1&scope=all"),
			"static fields must encode in a stable order, or the render is not deterministic")
	})

	DescribeTable("trims the components no binding set",
		func(b live.Bind, want string) {
			Expect(live.OnWith("input", "chat.draft", b)["data-gotth-on"]).To(Equal(want))
		},
		Entry("nothing at all is the shape On has always emitted",
			live.Bind{}, "input:chat.draft"),
		Entry("a debounce alone leaves the key slot empty",
			live.Bind{Debounce: 150 * time.Millisecond}, "input:chat.draft::150"),
		Entry("a throttle alone leaves the key and debounce slots empty",
			live.Bind{Throttle: time.Second}, "input:chat.draft:::1000"),
		Entry("fields alone leave the three before them empty",
			live.Bind{Fields: map[string]string{"room": "alpha"}}, "input:chat.draft::::room=alpha"),
		Entry("a key alone is exactly what it was before this landed",
			live.Bind{Keys: []string{"Escape"}}, "input:chat.draft:Escape"),
	)

	// Static fields are URL-encoded by net/url, which escapes ":" and ";" —
	// the two characters this grammar spends — so a field name or value can
	// hold either and the binding still parses. Asserted rather than assumed,
	// because the whole component sits inside a ":"-separated spec.
	It("percent-encodes a field whose value would otherwise split the binding", func() {
		Expect(live.OnWith("click", "row.open", live.Bind{
			Fields: map[string]string{"at": "12:30", "and": "a;b"},
		})["data-gotth-on"]).To(Equal("click:row.open::::and=a%3Bb&at=12%3A30"))
	})

	// A key list is several bindings, so each of them carries the whole Bind's
	// options. Anything less would make "+ debounced" and "= debounced" differ
	// by which one the author wrote first.
	It("gives every binding a key list expands to the same options", func() {
		Expect(live.OnWith("keydown", "counter.step", live.Bind{
			Keys:     []string{"+", "-"},
			Debounce: 40 * time.Millisecond,
		})["data-gotth-on"]).To(Equal("keydown:counter.step:+:40;keydown:counter.step:-:40"))
	})

	// THE CASE §5.6 IS ABOUT. The guide's composer, verbatim. Before this
	// landed the pair rendered data-gotth-on plus one data-gotth-debounce="150"
	// on the element, and the Escape binding read it.
	It("leaves a filtered binding unencumbered by a sibling's debounce", func() {
		attrs := live.OnAll(
			live.OnWith("keydown", "chat.clear", live.Bind{Keys: []string{"Escape"}}),
			live.OnWith("input", "chat.draft", live.Bind{Debounce: 150 * time.Millisecond}),
		)

		Expect(attrs).To(Equal(templ.Attributes{
			"data-gotth-on": "keydown:chat.clear:Escape;input:chat.draft::150",
		}))
	})

	// The disagreement case OnAll's godoc used to describe. There is no longer
	// a disagreement to resolve: two bindings that ask for different intervals
	// get different intervals, and neither is silently given the other's.
	It("keeps each binding's own value where two of them disagree", func() {
		attrs := live.OnAll(
			live.OnWith("input", "chat.draft", live.Bind{
				Debounce: 150 * time.Millisecond,
				Fields:   map[string]string{"room": "alpha"},
			}),
			live.OnWith("keydown", "chat.clear", live.Bind{
				Keys:     []string{"Escape"},
				Debounce: 900 * time.Millisecond,
				Throttle: time.Second,
			}),
		)

		Expect(attrs).To(Equal(templ.Attributes{
			"data-gotth-on": "input:chat.draft::150::room=alpha;" +
				"keydown:chat.clear:Escape:900:1000",
		}))
	})

	// Composition is now non-destructive for EVERY binding rather than for the
	// first one. That is the property OnAll's first-wins rule was protecting,
	// and it is why the rule is now vacuous rather than merely changed.
	It("renders a composed binding byte-identically to that binding alone", func() {
		alone := live.OnWith("input", "chat.draft", live.Bind{Debounce: 150 * time.Millisecond})
		composed := live.OnAll(
			alone,
			live.OnWith("keydown", "chat.clear", live.Bind{Keys: []string{"Escape"}, Throttle: time.Second}),
		)

		Expect(composed["data-gotth-on"]).To(HavePrefix(alone["data-gotth-on"].(string) + ";"))
	})

	// The fixture docs/reviews/fr-54.md §7.2 runs on the client side, pinned
	// here on the emitting side. The two halves of this pair only mean
	// something together: client/test/binding.test.mjs writes this exact string
	// as a literal, and if the server stops emitting it that suite goes on
	// passing against markup nothing renders.
	//
	// It is also the shape §3 of that review moved Bind.Fields for: two keys,
	// ONE event name, different payloads.
	It("gives two bindings that share an event name their own key, fields and interval", func() {
		attrs := live.OnAll(
			live.OnWith("keydown", "c.step", live.Bind{
				Keys:     []string{"+"},
				Fields:   map[string]string{"dir": "up"},
				Debounce: 100 * time.Millisecond,
			}),
			live.OnWith("keydown", "c.step", live.Bind{
				Keys:     []string{"-"},
				Fields:   map[string]string{"dir": "down"},
				Debounce: 100 * time.Millisecond,
			}),
		)

		Expect(attrs["data-gotth-on"]).To(Equal(
			"keydown:c.step:+:100::dir=up;keydown:c.step:-:100::dir=down"))
	})
})

// FR54-3 — what the grammar cannot carry is REFUSED, not repaired.
//
// docs/reviews/fr-54.md §6 attacked the positional grammar with thirty hostile
// inputs and found one corner that this landing made worse. Before per-binding
// options, a key that was ":" widened the filter to every key and the debounce
// beside it still worked, because the debounce was an attribute of the element.
// Now the stray separator renders as an extra component, so every option
// declared after it lands one slot LATER than the client reads it and a
// declared Debounce arrives as a Throttle. A ";" is worse still: it splits one
// binding into two, the second of which is junk.
//
// The same shift reaches in through domEvent and eventName, which nothing
// validated either, and the empty event name is the sharpest of the four:
// dispatch() breaks out of its match loop on the first spec whose DOM event and
// key filter match and tests the name only afterwards, so a binding with an
// empty name matches, ends the loop, and silences every binding behind it on
// the same DOM event.
//
// # Why a panic, and what it costs
//
// On and OnWith return templ.Attributes, which is a map[string]any, and they
// have no error channel at all. templ.RenderAttributes (v0.3.1020) switches on
// each value's dynamic type with NO default case — verified by reading it — so
// a value of any other type, an error included, is skipped in silence and the
// attribute simply does not appear. An error value here would therefore produce
// exactly the silent no-op this vocabulary exists to prevent.
//
// So it panics, carrying a full sentence, which is what this library already
// does for a programmer error with nowhere to return one: live/page.go:85,
// :210 and :218. docs/error-audit.md §3.3.1 rules that §2.1's census does not
// cover panics, so these four sites do not move the FR-58 census (121) or
// internal/arch's live count (40).
var _ = Describe("Inputs the binding grammar cannot carry (FR54-3)", func() {
	DescribeTable("refuses them, loudly, before it writes anything",
		func(render func(), offending, because string) {
			Expect(render).To(PanicWith(SatisfyAll(
				ContainSubstring("gotth-live:"),
				ContainSubstring(offending),
				ContainSubstring(because),
			)), "the refusal must name the offending input and say why it cannot be carried")
		},

		// §6's table, all three rows. Row 1 is the unchanged-since-591c275a
		// behaviour that was merely wrong; rows 2 and 3 are what this landing
		// made worse and are the reason the condition exists.
		Entry(`a ":" key alone — it widened the filter to every key`,
			func() { live.OnWith("keydown", "e.one", live.Bind{Keys: []string{":"}}) },
			`":"`, "Bind.Keys"),
		Entry(`a ":" key with a debounce — the debounce became a THROTTLE`,
			func() {
				live.OnWith("keydown", "e.one", live.Bind{
					Keys: []string{":"}, Debounce: 150 * time.Millisecond,
				})
			},
			`":"`, "Bind.Keys"),
		Entry(`a ";" key with a debounce — it split the binding in two and the second half was junk`,
			func() {
				live.OnWith("keydown", "e.one", live.Bind{
					Keys: []string{";"}, Debounce: 150 * time.Millisecond,
				})
			},
			`";"`, "Bind.Keys"),

		// §6's second block: nothing validated domEvent or eventName either.
		Entry(`a ":" in the event name — the tail was read as a key filter, so a click never matched`,
			func() { live.On("click", "a:b") },
			`"a:b"`, "event name"),
		Entry(`a ":" in the event name with a debounce — and 150 landed in the throttle slot`,
			func() {
				live.OnWith("click", "a:b", live.Bind{Debounce: 150 * time.Millisecond})
			},
			`"a:b"`, "event name"),
		Entry(`a ";" in the event name — two specs, the second junk`,
			func() { live.On("click", "a;b") },
			`"a;b"`, "event name"),
		Entry("an EMPTY event name — it matched, broke the loop, and shadowed every binding behind it",
			func() { live.On("click", "") },
			`"click"`, "empty event name"),
		Entry(`a ":" in the DOM event — every component after it shifts`,
			func() { live.On("cl:ick", "e.one") },
			`"cl:ick"`, "DOM event"),
		Entry(`a ";" in the DOM event`,
			func() { live.On("cl;ick", "e.one") },
			`"cl;ick"`, "DOM event"),
	)

	// The other half of a refusal is what it does NOT refuse. §6 checked that
	// every other printable key still round-trips into the key slot untouched,
	// and the set of characters a key may not be is exactly ":" and ";" — so a
	// refusal that grew that set would be a worse regression than the one it
	// fixes.
	DescribeTable("refuses nothing else",
		func(render func() templ.Attributes, want string) {
			var attrs templ.Attributes
			Expect(func() { attrs = render() }).NotTo(Panic())
			Expect(attrs["data-gotth-on"]).To(Equal(want))
		},
		Entry(`","`, func() templ.Attributes {
			return live.OnWith("keydown", "e.one", live.Bind{Keys: []string{","}})
		}, "keydown:e.one:,"),
		Entry(`"+"`, func() templ.Attributes {
			return live.OnWith("keydown", "e.one", live.Bind{Keys: []string{"+"}})
		}, "keydown:e.one:+"),
		Entry(`" "`, func() templ.Attributes {
			return live.OnWith("keydown", "e.one", live.Bind{Keys: []string{" "}})
		}, "keydown:e.one: "),
		Entry(`"|" "=" "&" "%" "\"" "<", one binding each`, func() templ.Attributes {
			return live.OnWith("keydown", "e.one", live.Bind{Keys: []string{"|", "=", "&", "%", `"`, "<"}})
		}, "keydown:e.one:|;keydown:e.one:=;keydown:e.one:&;"+
			"keydown:e.one:%;keydown:e.one:\";keydown:e.one:<"),
		// The documented "every key", and NOT an empty component to be refused:
		// an empty Keys entry renders as no key component at all, which is what
		// a binding with no Keys renders and what §6 verified.
		Entry("an empty Keys entry is the documented every-key filter", func() templ.Attributes {
			return live.OnWith("keydown", "e.one", live.Bind{Keys: []string{""}})
		}, "keydown:e.one"),
		// A field may hold either separator, because encodeFields escapes both
		// in keys and in values alike. That is the component whose content is
		// the caller's data, and it is the one that needs no refusal.
		Entry("a field carrying both separators", func() templ.Attributes {
			return live.OnWith("click", "row.open", live.Bind{Fields: map[string]string{"at": "12:30;x"}})
		}, "click:row.open::::at=12%3A30%3Bx"),
		// An empty DOM event is deliberately NOT refused: it renders one
		// component exactly as any other name does, it shadows nothing (the
		// client compares s[0] against e.type and no event type is ""), and it
		// is the same class as Bind.Keys' documented unrecognised key name — a
		// filter that matches nothing. Refusing it would widen FR54-3 past what
		// the grammar cannot carry.
		Entry("an empty DOM event still renders one well-formed component", func() templ.Attributes {
			return live.On("", "e.one")
		}, ":e.one"),
	)
})

// FR54-6 — components 7 and 8, the Part B landing.
//
// docs/reviews/fr-54.md §12 accepts two exported struct fields on Bind and
// REFUSES the full modifier set (§13, with a re-open trigger). The requirement
// is the benchmark equivalence spec's F-CHT-3 — "Enter sends, Shift+Enter
// inserts a newline" — which a key filter alone cannot express, because the
// filter chooses which keys raise events and does not take a key away from the
// page. That limitation has its own spec in
// test/internal/conformance/keybinding_test.go and it stays green: nothing
// about a binding that sets neither of these fields changes.
//
// These specs are the emission half. The reading half is
// client/test/binding.test.mjs plus the browser specs in
// test/internal/conformance/keybinding_modifiers_test.go, and the three only
// mean something together — the attribute spelling is a contract between the
// server and a runtime that is versioned with it, and a disagreement is a
// silent no-op in the browser rather than an error anywhere.
var _ = Describe("NoModifiers and PreventDefault as components of the binding (FR-54 failure 1)", func() {
	// F-CHT-3, verbatim from the review's §12.1. This literal is the whole
	// requirement: two bindings, one element, and the send binding carrying
	// both new options while the draft binding beside it carries neither.
	It("emits F-CHT-3's composer exactly as the ruling specifies it", func() {
		attrs := live.OnAll(
			live.OnWith("keydown", "chat.send", live.Bind{
				Keys: []string{"Enter"}, NoModifiers: true, PreventDefault: true,
			}),
			live.OnWith("input", "chat.draft", live.Bind{Debounce: 150 * time.Millisecond}),
		)

		Expect(attrs).To(Equal(templ.Attributes{
			"data-gotth-on": "keydown:chat.send:Enter::::1:1;input:chat.draft::150",
		}))
	})

	DescribeTable("renders each as \"1\" when set, and as nothing at all when not",
		func(b live.Bind, want string) {
			Expect(live.OnWith("input", "chat.draft", b)["data-gotth-on"]).To(Equal(want))
		},
		Entry("NoModifiers alone leaves the five slots before it empty",
			live.Bind{NoModifiers: true}, "input:chat.draft:::::1"),
		Entry("PreventDefault alone leaves the six before it empty",
			live.Bind{PreventDefault: true}, "input:chat.draft::::::1"),
		Entry("both, which is the shape F-CHT-3's send binding wants",
			live.Bind{NoModifiers: true, PreventDefault: true}, "input:chat.draft:::::1:1"),
		Entry("both, behind a debounce that occupies component four",
			live.Bind{Debounce: 150 * time.Millisecond, NoModifiers: true, PreventDefault: true},
			"input:chat.draft::150:::1:1"),
	)

	// C-1, asserted rather than inferred from the other specs being green.
	//
	// The claim the whole landing rests on is that every binding the tree
	// renders TODAY is byte-identical after it, which is what makes the
	// runtime and the markup upgradeable independently — the mixed-version
	// window FR54-1 is about. Trailing-empty trimming is the mechanism, and
	// the case that would break first is the one where the previously-LAST
	// component is set: two unset components behind it must render as nothing
	// rather than as two colons.
	DescribeTable("adds not one byte to a binding that sets neither",
		func(b live.Bind, want string) {
			Expect(live.OnWith("input", "chat.draft", b)["data-gotth-on"]).To(Equal(want))
		},
		Entry("nothing at all", live.Bind{}, "input:chat.draft"),
		Entry("a key alone", live.Bind{Keys: []string{"Escape"}}, "input:chat.draft:Escape"),
		Entry("a debounce alone", live.Bind{Debounce: 150 * time.Millisecond}, "input:chat.draft::150"),
		Entry("a throttle alone", live.Bind{Throttle: time.Second}, "input:chat.draft:::1000"),
		Entry("fields alone — the component that USED to be last",
			live.Bind{Fields: map[string]string{"room": "alpha"}}, "input:chat.draft::::room=alpha"),
		Entry("all of them at once",
			live.Bind{
				Keys:     []string{"Escape"},
				Debounce: 150 * time.Millisecond,
				Throttle: time.Second,
				Fields:   map[string]string{"room": "alpha"},
			},
			"input:chat.draft:Escape:150:1000:room=alpha"),
	)

	// A key list is several bindings and every one of them carries the whole
	// Bind, which is the property that keeps "+" and "-" from differing by
	// which the author wrote first. It has to hold for these two as much as
	// for the interval.
	It("gives every binding a key list expands to both options", func() {
		Expect(live.OnWith("keydown", "c.step", live.Bind{
			Keys: []string{"+", "-"}, NoModifiers: true, PreventDefault: true,
		})["data-gotth-on"]).To(Equal(
			"keydown:c.step:+::::1:1;keydown:c.step:-::::1:1"))
	})

	// FR54-3's refusal and these two components do not interact, and the
	// direction that could have gone wrong is worth pinning rather than
	// asserting in a comment.
	//
	// Before this landing Fields was the LAST component, so a separator that
	// escaped encodeFields could only have produced junk at the end of the
	// spec. Now two components sit BEHIND it, so the same escape is what keeps
	// a caller's data from shifting a boolean into existence — a field value
	// holding ":" would otherwise render a component the client reads as
	// NoModifiers, and the binding would silently stop firing for a shifted
	// key. net/url escapes both separators in keys and values alike; that was
	// already asserted for the value's own sake, and this asserts it for the
	// components it now protects.
	It("keeps a field's separators out of the two components behind it", func() {
		Expect(live.OnWith("click", "row.open", live.Bind{
			Fields:         map[string]string{"at": "12:30;x"},
			NoModifiers:    true,
			PreventDefault: true,
		})["data-gotth-on"]).To(Equal("click:row.open::::at=12%3A30%3Bx:1:1"))
	})

	// The other direction: neither component can carry a separator, because
	// neither is ever anything but "1" or absent. So the refusal set is
	// exactly what FR54-3 left it — a bool has nothing to refuse — and a
	// refusable key is still refused when it arrives beside them.
	It("refuses an unbindable key beside them exactly as it does without them", func() {
		Expect(func() {
			live.OnWith("keydown", "e.one", live.Bind{
				Keys: []string{":"}, NoModifiers: true, PreventDefault: true,
			})
		}).To(PanicWith(SatisfyAll(
			ContainSubstring("gotth-live:"),
			ContainSubstring(`":"`),
			ContainSubstring("Bind.Keys"),
		)))
	})

	// Composition changes nothing about either option, which is the property
	// OnAll carries for every other field of Bind.
	It("renders a composed binding byte-identically to that binding alone", func() {
		alone := live.OnWith("keydown", "chat.send", live.Bind{
			Keys: []string{"Enter"}, NoModifiers: true, PreventDefault: true,
		})
		composed := live.OnAll(alone, live.OnWith("input", "chat.draft", live.Bind{}))

		Expect(composed["data-gotth-on"]).To(HavePrefix(alone["data-gotth-on"].(string) + ";"))
	})
})
