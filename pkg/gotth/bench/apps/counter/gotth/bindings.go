package main

import (
	"github.com/a-h/templ"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// KeyBindings is F-CTR-6: "Keyboard: `+`/`-` on the focused counter apply
// `+1`/`−1`".
//
// It lives in a .go file rather than in view.templ because templ's generated
// code imports templ itself, so a .templ file that also imports it declares the
// name twice and the generated package does not compile. A helper returning
// templ.Attributes therefore belongs beside the view rather than inside it.
//
// It is ONE helper spreading two bindings, not two spreads, and that is not
// tidiness. templ renders each attribute spread separately, two spreads of
// live.On both emit data-gotth-on, and an HTML parser keeps the first — so the
// second binding would vanish with no error anywhere. live.OnAll is the only
// way to put two bindings on one element, and F-CTR-6 needs two.
//
// live.Bind.Keys compares exactly and case-sensitively against the browser's
// own KeyboardEvent.key, so "+" is the shifted "=" as the browser reports it,
// which is exactly what the harness's CTR-5 dispatches
// (bench/harness/input.mjs sends key "+" with its text). A filtered binding
// must also come BEFORE an unfiltered one for the same DOM event or it can
// never be reached — there is no unfiltered keydown binding here, so the order
// of the two below is free, and it is written most-specific-first anyway so it
// stays correct if one is ever added.
//
// The two properties this cannot express, recorded here because the chat app
// needs them and cannot have them: modifier state is not compared, and a key
// binding never calls preventDefault. Neither matters for a bare "+" or "-".
// Both are why F-CHT-3's "Enter sends, Shift+Enter newlines" is not
// expressible — see bench/README.md.
//
// Corrected 2026-08-05: the library expresses both now, per binding, as
// live.Bind.NoModifiers and live.Bind.PreventDefault, and the chat app has
// adopted them. The paragraph above is kept because it is the record of what
// this row could see from where it sat.
//
// NEITHER OPTION BELONGS HERE, and the reason is the same sentence that made
// them safe to add: "+" IS Shift and "=" pressed together on most layouts, so a
// binding that named "+" and demanded no modifier held would match nothing at
// all, and taking "+" away from the browser would be this library deciding what
// a key means. Both default off; the two bindings below render exactly what
// they rendered before either field existed, which is what keeps CTR-5's
// measured markup unchanged across the landing.
func KeyBindings() templ.Attributes {
	return live.OnAll(
		live.OnWith("keydown", EventIncrement, live.Bind{Keys: []string{"+"}}),
		live.OnWith("keydown", EventDecrement, live.Bind{Keys: []string{"-"}}),
	)
}
