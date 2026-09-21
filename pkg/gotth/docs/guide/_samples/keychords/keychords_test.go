package keychords_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/a-h/templ"
	"github.com/candacelabs/csf/pkg/gotth/docs/guide/_samples/keychords"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// These specs exist so that the attribute spellings printed on
// docs/guide/events-and-forms.md are compiled rather than transcribed.
//
// The page prints an attribute string beside every option it documents, and an
// attribute string is the one thing in this library a reader cannot check by
// reading Go: it is produced by live/templ.go, consumed by client/runtime.js,
// and a disagreement between them raises nothing at all — the binding simply
// never fires. So every spelling on that page is asserted here against the
// helper that emits it, and a page edited without the helper turns this red.
//
// The emitting side has its own suite in live/binding_test.go and the reading
// side has client/test/binding.test.mjs plus the Chromium specs in
// test/internal/conformance/keybinding_modifiers_test.go. This file duplicates
// neither: it holds the GUIDE to what those two agreed on.
var _ = Describe("the spellings docs/guide/events-and-forms.md prints", func() {
	spec := func(attrs templ.Attributes) string {
		s, ok := attrs["data-gotth-on"].(string)
		Expect(ok).To(BeTrue(), "the helper emitted no data-gotth-on at all: %#v", attrs)
		return s
	}

	It("renders F-CHT-3's composer as the page says it does", func() {
		Expect(spec(keychords.Composer())).To(Equal(
			"keydown:chat.send:Enter::::1:1;input:chat.draft::150"))
	})

	It("renders NoModifiers on a click binding as the page says it does", func() {
		Expect(spec(keychords.PlainClick())).To(Equal("click:row.open:::::1"))
	})

	// Components seven and eight, one at a time, because the page documents
	// each one's slot and a reader counting colons has to be able to trust the
	// count. Five empty components before NoModifiers, six before
	// PreventDefault.
	DescribeTable("puts each option in the component the page numbers it",
		func(b live.Bind, want string) {
			Expect(spec(live.OnWith("input", keychords.EventDraft, b))).To(Equal(want))
		},
		Entry("NoModifiers is component seven",
			live.Bind{NoModifiers: true}, "input:chat.draft:::::1"),
		Entry("PreventDefault is component eight",
			live.Bind{PreventDefault: true}, "input:chat.draft::::::1"),
		Entry("both, which is what a send binding wants",
			live.Bind{NoModifiers: true, PreventDefault: true}, "input:chat.draft:::::1:1"),
		Entry("both, behind a debounce occupying component four",
			live.Bind{Debounce: 150 * time.Millisecond, NoModifiers: true, PreventDefault: true},
			"input:chat.draft::150:::1:1"),
	)

	// The claim under the correction on the page — that a page written before
	// these two options renders byte for byte what it always did — stated here
	// so the page cannot be the only place it is written down.
	DescribeTable("adds nothing to a binding that sets neither",
		func(b live.Bind, want string) {
			Expect(spec(live.OnWith("input", keychords.EventDraft, b))).To(Equal(want))
		},
		Entry("no options at all", live.Bind{}, "input:chat.draft"),
		Entry("a key filter alone", live.Bind{Keys: []string{"Escape"}}, "input:chat.draft:Escape"),
		Entry("a debounce alone", live.Bind{Debounce: 150 * time.Millisecond}, "input:chat.draft::150"),
		Entry("static fields alone",
			live.Bind{Fields: map[string]string{"room": "alpha"}}, "input:chat.draft::::room=alpha"),
	)
})

// What the helpers REFUSE, which the page documents because a reader who meets
// one of these meets it as a panic on the first render of their view.
//
// The message matters as much as the refusal — it is the whole of what the
// reader gets — so these assert on the sentence and not merely on the fact that
// something was raised.
var _ = Describe("the arguments the binding grammar refuses", func() {
	It("refuses a separator in the DOM event", func() {
		Expect(func() { live.On("key:down", keychords.EventSend) }).
			To(PanicWith(ContainSubstring("which contains \":\"")))
	})

	It("refuses a separator in the event name", func() {
		Expect(func() { live.On("keydown", "chat;send") }).
			To(PanicWith(ContainSubstring("which contains \";\"")))
	})

	It("refuses an empty event name", func() {
		Expect(func() { live.On("keydown", "") }).
			To(PanicWith(ContainSubstring("silences every binding behind it")))
	})

	It("refuses a separator in a key", func() {
		Expect(func() {
			live.OnWith("keydown", keychords.EventSend, live.Bind{Keys: []string{";"}})
		}).To(PanicWith(ContainSubstring("Bind.Keys")))
	})

	// The other side of the refusal, and the reason the page can still say
	// "nothing else is reserved": every other printable key value, including
	// the two a reader most expects to be special, goes through unchanged.
	It("carries a comma and a space through as ordinary keys", func() {
		Expect(live.OnWith("keydown", keychords.EventSend,
			live.Bind{Keys: []string{",", " "}})["data-gotth-on"]).To(Equal(
			"keydown:chat.send:,;keydown:chat.send: "))
	})
})
