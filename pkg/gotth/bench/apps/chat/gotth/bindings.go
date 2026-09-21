package main

import (
	"time"

	"github.com/a-h/templ"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// DraftDebounce is how long the composer waits before telling the server what
// is in it.
//
// It is trailing-edge and it resets on every keystroke, which is the runtime's
// only debounce shape: during continuous typing NOTHING is sent until the
// typist pauses for this long, so the outbound rate is roughly one event per
// burst rather than one per character. That is fewer frames than the Next.js
// side's 1 Hz typing ping, not more, and it is the same 150 ms examples/chat
// uses.
//
// Its value is not equivalence-bearing: §2.3 specifies a debounce for the
// dashboard's search (§2.4) and none for the composer, and CHT-1's requirement
// is that the character MUST NOT round-trip, which is a statement about what
// paints and not about what is sent. What this delay does bound is how long
// CHT-1's paint predicate takes to become true on this stack, because on this
// stack the only thing in region B that can mutate when a key is pressed is the
// character counter, and the counter is server state. bench/README.md says so
// plainly rather than letting the number be read as an implementation defect.
const DraftDebounce = 150 * time.Millisecond

// ComposerBinding is the textarea's whole binding set: F-CHT-3's "Enter sends,
// Shift+Enter newlines" and the debounced draft, on one element.
//
// It renders "keydown:chat.send:Enter::::1:1;input:chat.draft::150", and every
// component of that string is load-bearing:
//
//   - Keys{"Enter"} — the filter, so no other key raises a frame.
//   - NoModifiers — component 7. Shift+Enter matches the KEY and fails the
//     modifier test, so this binding does not fire; a filtered-out binding
//     suppresses nothing and ends nothing, so the client goes on to the next
//     spec, finds no other keydown binding, and leaves the press to the
//     browser, which inserts the line break. That is the SECOND half of
//     F-CHT-3 and it is expressed by the ABSENCE of a matching binding rather
//     than by a binding that does nothing.
//   - PreventDefault — component 8. Without it Enter would send AND insert a
//     newline, which is the first half half-met.
//   - Debounce on the OTHER binding only. This is why the pair is expressible
//     at all: until 2ab18690 Debounce was read from the ELEMENT, so the send
//     binding would have inherited the draft's 150 ms timer and Enter's own
//     trailing input event would have cancelled the pending send outright.
//
// F-CHT-3 was recorded in bench/README.md as inexpressible for exactly those
// three reasons. All three are closed — 2ab18690 (per-binding options) and the
// NoModifiers/PreventDefault landing — and this is the app adopting it, so the
// §2.3 feature table is true of the MEASURED artifact and not only of the
// library. E1 requires the same product surface on both stacks and the Next.js
// side has had this since ChatLive.tsx:137; the difference is not in §2.6's
// asymmetry register and, this being a closed list only §12 may add to, closing
// it here is the only remedy this tree may take on its own.
//
// The order matters for the same-DOM-event case and not for this one — the
// client matches in the order given and the first match wins, and these are two
// different DOM events. It is written send-first anyway, so that a later keydown
// binding added below it inherits the reachable position rather than the
// shadowed one.
//
// NOTHING MEASURED CHANGES. No CHT-* interaction presses Enter: CHT-1 types
// "x", CHT-7 types a..z, and CHT-2/CHT-2b/CHT-5/CHT-8 click Send. A non-Enter
// keydown fails the key filter, falls through the input spec on type, and
// raises nothing — so the frame count per keystroke is what it was.
func ComposerBinding() templ.Attributes {
	return live.OnAll(
		live.OnWith("keydown", EventSend, live.Bind{
			Keys: []string{"Enter"}, NoModifiers: true, PreventDefault: true,
		}),
		live.OnWith("input", EventDraft, live.Bind{Debounce: DraftDebounce}),
	)
}

// SwitchBinding is CHT-4's room button.
//
// The room travels as a STATIC field rather than as the control's value,
// because the runtime serialises a bound element's own name and value only when
// it is outside a form, and these buttons are outside one. live.Bind.Fields is
// exactly that case: a value the markup knows and the DOM does not carry.
func SwitchBinding(room string) templ.Attributes {
	return live.OnWith("click", EventSwitch, live.Bind{Fields: map[string]string{fieldRoom: room}})
}
