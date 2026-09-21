// Package keychords is the compiled source for the two modifier-aware options
// on docs/guide/events-and-forms.md: live.Bind.NoModifiers and
// live.Bind.PreventDefault.
//
// It is a separate package from events/ rather than a second view in it, and
// the reason is what these two options are for. events/ is the composer a
// reader builds first — a real <form>, where Enter-to-send comes from the
// browser and no key binding is involved at all. That is still the right first
// answer and the guide still gives it first. These options are the second
// answer, for the composer that is a <textarea> and cannot be a form: the
// benchmark equivalence spec's F-CHT-3, "Enter sends, Shift+Enter newlines".
//
// Nothing here renders markup, because what a reader has to get right is the
// Bind and not the element it is spread into. The specs beside this file assert
// the exact attribute string each function produces, which is the contract
// client/SIZE.md §7 holds the runtime to — a disagreement between the two is a
// silent no-op in a browser rather than an error anywhere.
package keychords

import (
	"time"

	"github.com/a-h/templ"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The event names, as Config.Events would have to register them. Named
// constants rather than literals for the reason docs/guide/events-and-forms.md
// gives at length: a reducer that matches a mistyped literal fails by having
// its branch never run.
const (
	EventSend  = "chat.send"
	EventDraft = "chat.draft"
	EventOpen  = "row.open"
)

// Composer is F-CHT-3: Enter sends, Shift+Enter inserts a newline.
//
// Two bindings for one key on one element, and it works because a binding
// filtered out by NoModifiers does not end the client's match loop — Shift is
// held, the send binding does not match, and no binding behind it names Enter,
// so the keypress reaches nobody and the browser inserts the line break it was
// always going to insert.
//
// PreventDefault is what stops the plain Enter doing both. Without it the send
// binding fires AND the textarea gains a newline; with it the newline is
// suppressed for the press that sent, and only for that press.
func Composer() templ.Attributes {
	return live.OnAll(
		live.OnWith("keydown", EventSend, live.Bind{
			Keys: []string{"Enter"}, NoModifiers: true, PreventDefault: true,
		}),
		live.OnWith("input", EventDraft, live.Bind{Debounce: 150 * time.Millisecond}),
	)
}

// PlainClick is NoModifiers on a binding with no Keys and no keyboard event at
// all, which is legal and is not a no-op.
//
// A MouseEvent carries the same four booleans a KeyboardEvent does, so this
// binding means a plain click: Ctrl+click and Shift+click, the two a browser
// already reads as "open this somewhere else", are left to the browser.
func PlainClick() templ.Attributes {
	return live.OnWith("click", EventOpen, live.Bind{NoModifiers: true})
}
