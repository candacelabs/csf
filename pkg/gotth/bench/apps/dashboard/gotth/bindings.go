package main

import (
	"strconv"

	"github.com/a-h/templ"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The six controls' bindings, in one file because §2.4 measures four of them
// (DSH-1..DSH-5) and every one of them has to be the same KIND of interaction it
// is on the other stack.

// FilterBinding is DSH-1's status chip.
//
// The status travels as a STATIC field rather than as the control's value,
// because the runtime serialises a bound element's own name and value only when
// it is outside a form, and these buttons are outside one. live.Bind.Fields is
// exactly that case: a value the markup knows and the DOM does not carry.
func FilterBinding(status string) templ.Attributes {
	return live.OnWith("click", EventFilter, live.Bind{Fields: map[string]string{fieldValue: status}})
}

// PerPageBinding is DSH-4's rows-per-page chip. Buttons rather than a <select>,
// for BENCH-1's reading R-3: a click is the native pointerdown §3.2 defines
// t_input against, where a <select> would put the causal start in a change
// event the spec does not define.
func PerPageBinding(n int) templ.Attributes {
	return live.OnWith("click", EventPerPage, live.Bind{Fields: map[string]string{fieldValue: strconv.Itoa(n)}})
}

// SortBinding is DSH-3's metric_1 toggle. It carries no field: the mode cycles
// off → asc → desc → off on the server, so the browser says only that the
// button was pressed. A mode sent from the page would be a client deciding
// server state, and the value DSH-3's predicate reads would then be a value the
// client had already chosen.
func SortBinding() templ.Attributes { return live.On("click", EventSort) }

// PauseBinding is DSH-5's pause/resume. Server-authoritative, per R-2: the feed
// keeps running and this session stops following it, so the pause is a round
// trip here exactly as it is on the other stack.
func PauseBinding() templ.Attributes { return live.On("click", EventPause) }

// SearchBinding is DSH-2's text search.
//
// §2.4 requires the search to be "debounced 150 ms on both stacks with
// identical debounce implementation semantics", and BENCH-1 wrote those
// semantics down in the DSH-2 driver so this side could match rather than
// approximate: TRAILING edge, timer reset on every keystroke, one request fired
// 150 ms after the last one, no leading call and no maximum wait. That is
// exactly what live.Bind.Debounce renders and what the runtime implements, so
// the debounce is the same quantity inside the same measured interval on both
// sides.
//
// The needle rides as the input's OWN name and value — the input carries
// name="q" — rather than as a static field, because unlike the chips above it
// is a value the DOM does carry and the server must not guess.
func SearchBinding() templ.Attributes {
	return live.OnWith("input", EventSearch, live.Bind{Debounce: SearchDebounce})
}
