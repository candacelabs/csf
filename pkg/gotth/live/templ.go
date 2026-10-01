package live

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
)

// The attribute vocabulary. Every attribute this library emits is in the
// data-gotth-* family, and these constants are the single place the spellings
// are written on the server: the client runtime reads exactly these, and a
// disagreement between the two is a silent no-op in the browser rather than an
// error anywhere.
// There is no attribute here for Bind.Fields, Bind.Debounce or Bind.Throttle,
// and their absence is the whole of FR-54 failure 2. Each of them used to be an
// attribute of the ELEMENT; they are now components of the binding that
// declared them, inside attrOn. See binding.
const (
	attrRegion    = "data-gotth-region"
	attrOn        = "data-gotth-on"
	attrPreserve  = "data-gotth-preserve"
	attrScriptURL = "data-gotth-url"

	// The two the dev-reload tag carries, and the only attributes here that a
	// production page never contains: (*App).DevReloadScript writes nothing at
	// all when Config.Dev is false. They are spelled out here rather than
	// beside that method because this block is where the client and the server
	// are held to one vocabulary, and a dev-only attribute that drifted would
	// fail exactly as silently as any other.
	attrDevURL   = "data-gotth-dev-url"
	attrDevBuild = "data-gotth-dev-build"
)

// clientRuntimeFile is the artifact the handler serves and Script points at.
const clientRuntimeFile = "gotth-live.min.js"

// clientInspectorFile is the dev inspector, served and pointed at only when
// Config.Dev is set. It is a second artifact rather than part of the first
// because PRD NFR-8 requires the inspector to be a separate opt-in file that
// does not count against NFR-2's 12,288-byte ceiling — client/SIZE.md carries
// both measurements, and tools/minify holds each to its own ceiling.
const clientInspectorFile = "gotth-live-inspector.min.js"

// clientRuntime is the minified client, embedded by exact filename rather than
// by a glob so that adding a file beside it cannot silently change what ships.
//
//go:embed clientjs/gotth-live.min.js
var clientRuntime []byte

// clientInspector is the minified dev inspector, embedded by exact filename
// for the same reason.
//
// Embedding is unconditional; SERVING is not. A binary built from this module
// carries these bytes the way it carries any other embedded asset, and what a
// production build does not do is hand them to a browser or name them in a
// page: both are gated on Config.Dev, and both gates are tested. If the bytes
// themselves must be absent from a production binary, that is a build-tag
// change this module does not currently make, and saying so is better than
// implying an exclusion that is not there.
//
//go:embed clientjs/gotth-live-inspector.min.js
var clientInspector []byte

// clientRuntimeETag is computed once, at init, so every response can be
// conditional without hashing per request.
var clientRuntimeETag = etagOf(clientRuntime)

// clientInspectorETag is the same, for the inspector.
var clientInspectorETag = etagOf(clientInspector)

func etagOf(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// Region marks an element as the root of the named live fragment.
//
// Morph never touches anything outside a region, which is what makes an
// HTMX-driven or third-party-owned region on the same page safe by
// construction rather than by care.
func Region(id string) templ.Attributes {
	return templ.Attributes{attrRegion: id}
}

// On binds a DOM event to a server event.
//
// On a form, submission sends the form's fields; on a named control, the
// control's name and value are sent. Several bindings on one element combine —
// the client matches them in order and the first match wins — and OnAll is how
// they are spelled, because two spreads of the same attribute are one attribute
// in the browser and the second one is dropped.
//
// # It panics on an argument the binding grammar cannot carry
//
// A ":" or a ";" in either argument, and an empty eventName. ":" separates the
// components of one binding and ";" separates the bindings on an element, so
// either character renders as an extra component and shifts every component
// behind it — turning a declared Bind.Debounce into a Throttle. An empty
// eventName is worse: it renders a spec that matches, ends the client's match
// loop, and silences every binding behind it on the same DOM event.
//
// It is a panic rather than an error because this function returns
// templ.Attributes — a map[string]any with no error channel — and
// templ.RenderAttributes has no default case, so an error value put in the map
// would be dropped in silence and the element would render with no binding at
// all. A panic is what this library already does for a nil page handler or a
// mount path of "/": each of these is a literal in the caller's source, so it
// fails on the first render of the view rather than on a visitor's request.
func On(domEvent, eventName string) templ.Attributes {
	return templ.Attributes{attrOn: binding(domEvent, eventName, Bind{})}
}

// Bind carries the extra options OnWith accepts.
//
// Every option here is scoped to the ONE binding it is given to, and travels
// with that binding rather than being written on the element. That is a
// property to rely on: composing this binding with another through OnAll
// changes neither one's behaviour, and neither can read the other's interval,
// its rate or its fields.
//
// It was not always so, and the correction is FR-54 failure 2. Fields, Debounce
// and Throttle were attributes of the element until 2026-08-05, so every
// binding on an element shared one of each and one timer. What that cost was
// measured rather than argued: on the guide's own composer — an Escape binding
// composed with a 150 ms input binding — the Escape inherited the interval, and
// a keystroke inside that window did not delay the pending clear, it destroyed
// it. No error, no console warning, nothing on the wire. The reverse held too:
// an Escape inside the window destroyed a pending draft, so the server never
// learned what was typed while the browser went on showing it.
// docs/qa/fr-54-debounce-repro.md is the reproduction, in Chromium, against the
// real runtime.
type Bind struct {
	// Fields are static values sent with every occurrence of this binding's
	// event, in addition to whatever the element itself contributes.
	Fields map[string]string
	// Debounce delays sending until THIS binding's DOM event has been quiet
	// for this long. Zero means no debounce.
	Debounce time.Duration
	// Throttle sends at most one of THIS binding's events per interval. Zero
	// means no throttle.
	Throttle time.Duration
	// Keys restricts a keyboard binding to these keys, and raises no event for
	// any other. Empty is every key, which is what a keydown binding without
	// this option has always meant.
	//
	// Each entry is compared, exactly and case-sensitively, against the
	// browser's own KeyboardEvent.key value: "Escape" and not "Esc",
	// "ArrowUp" and not "Up", " " and not "Space", "A" and not "a" for a
	// shifted letter. Nothing is normalised, because "a" and "A" are different
	// keys and the name set belongs to the UI Events specification rather than
	// to this library — an unrecognised name is therefore not an error here,
	// it is a filter that matches nothing, and it shows up as a binding that
	// never fires on the first keypress.
	//
	// The key is compared and the modifier state is not — unless the binding
	// also sets NoModifiers, which is the only thing that reads a modifier and
	// is off by default for the reason stated there. A printable key already
	// carries its modifiers (Shift and "=" arrive as "+"), a modifier pressed
	// alone arrives as "Shift", "Control", "Alt" or "Meta" and matches only a
	// filter that names it, and a Ctrl or Meta chord belongs to the browser:
	// this library takes no key away from the browser except where a binding
	// asked for it by name, which is PreventDefault below and is off by
	// default too.
	//
	// A key filter on an event that carries no key — a click, an input — never
	// matches, so the binding never fires. A filter filters.
	//
	// ":" and ";" separate the bindings the client parses, so a key that is
	// one of those two characters cannot be expressed here, and OnWith PANICS
	// on one rather than rendering it. Nothing else is reserved: "," and " "
	// and every other printable key value are carried through unchanged,
	// because a list of keys is emitted as one binding per key rather than as
	// a separated list.
	//
	// That list of two is complete and did not grow when Fields, Debounce and
	// Throttle moved into the binding beside this one. They are further ":"
	// components of a grammar that had already spent ":", so the set of
	// characters a key may not be is exactly what it was: ":" and ";".
	//
	// What the panic replaces is worth saying, because it is what a page
	// written before 2026-08-05 got. Such a key used to render, and rendering
	// it did something neither this comment nor the author asked for: the
	// stray separator became an extra component, so the key filter widened to
	// EVERY key and every option declared beside it landed one slot later than
	// the client reads it — a 150 ms Debounce arrived as a 150 ms Throttle. A
	// ";" was worse: it split the binding in two and left the remainder as a
	// spec of junk. Both were silent.
	//
	// It is a panic and not an error because OnWith returns templ.Attributes
	// and has no error channel — see On.
	Keys []string

	// NoModifiers restricts this binding to presses with NO modifier key
	// held — no Shift, no Control, no Alt, no Meta.
	//
	// False, the zero value, is what every key binding has meant since
	// 591c275a: the key is compared and the modifier state is not. That
	// default is not a legacy, it is required: a printable key already
	// carries its modifiers, and "+" IS Shift and "=" pressed together on
	// most layouts, so a binding that named "+" and silently demanded no
	// modifier would match nothing (F-CTR-6).
	//
	// What it reads is exactly four booleans — the event's shiftKey, ctrlKey,
	// altKey and metaKey — and any one of them held is a press this binding
	// does not match. Two consequences of that are worth naming here, because
	// neither is visible from the option's name:
	//
	//   - AltGr sets BOTH ctrlKey and altKey. So a printable key that needs
	//     AltGr on the member's layout — "@" on many European layouts, "\"
	//     and "|" on others — will NOT match a binding that names it and sets
	//     this option, while the member is typing exactly the character the
	//     binding asked for. Name such a key without this option.
	//   - CapsLock and NumLock are NOT read. They are lock states rather than
	//     held modifiers, they set none of the four booleans, and a binding
	//     filtered here fires with either of them on.
	//
	// It applies whether or not Keys is set, and it is not restricted to
	// keyboard events. An unfiltered keydown binding with this option raises
	// its event for every UNMODIFIED key, which is a filter and not a no-op;
	// and because a mouse event carries the same four booleans, a click
	// binding with this option means a plain click rather than a Ctrl+click
	// or a Shift+click — the two a browser already treats as "open this
	// somewhere else". An event that carries none of the four, such as input
	// or submit, has all four absent, so a binding on one is unaffected.
	//
	// A binding this filters out suppresses nothing and ends nothing: the
	// client goes on to the next binding on the element for the same DOM
	// event. That is what lets Enter and Shift+Enter reach two different
	// bindings, or one binding and none at all, from one composer (F-CHT-3).
	NoModifiers bool

	// PreventDefault calls preventDefault() on the browser event when THIS
	// binding matches, and only then.
	//
	// The library calls it for a recognised form submit and an anchor click
	// already; this is the same act for a binding the author declared
	// explicitly, per binding, defaulting off. It is not a filter: a binding
	// that does not match does not suppress anything, which is what leaves
	// Shift+Enter to the browser.
	//
	// It is NOT called while an IME composition is active, and that ordering
	// is load-bearing rather than incidental. Enter during a composition
	// COMMITS the candidate, so a binding that suppressed it would take the
	// commit key away from every composer that uses one — the population
	// FR-26's composition guard exists for. The client tests that guard
	// first: mid-composition it neither sends the event nor suppresses the
	// default, and the binding fires on the Enter after the commit instead.
	PreventDefault bool
}

// OnWith is On with static extra fields, a debounce, a throttle, a key filter,
// a no-modifier restriction, or a preventDefault.
//
// It emits exactly one attribute — data-gotth-on — whatever the Bind holds. See
// Bind for why the options are inside the binding rather than beside it.
//
// It panics on everything On panics on, and additionally on a Bind.Keys entry
// containing ":" or ";" — the two characters Bind.Keys has said since 591c275a
// a key cannot be. See On for why it is a panic.
func OnWith(domEvent, eventName string, b Bind) templ.Attributes {
	return templ.Attributes{attrOn: binding(domEvent, eventName, b)}
}

// OnAll combines several bindings on one element.
//
// It exists because the client has always supported several bindings per
// element and nothing could emit them: templ renders each spread separately,
// two spreads of On produce the same attribute twice, and an HTML parser keeps
// the first and discards the second. So the second binding vanished silently,
// which is how a composer bound for input could not also be bound for a key,
// and how the two bindings a keyboard-driven counter needs on one focused
// element could not be written at all.
//
// The bindings are matched by the client in the order given and the first
// match wins, so a filtered binding must come before an unfiltered one for the
// same DOM event or it can never be reached.
//
// Every other option in a Bind — Fields, Debounce, Throttle — belongs to the
// binding that declared it and to no other, so composing bindings here changes
// none of them. A binding rendered by OnAll is byte-identical to the same
// binding rendered alone.
//
// # The merge rule that used to be here, and why it is gone
//
// Fields, Debounce and Throttle used to be attributes of the ELEMENT, so this
// function had to reconcile them, and the rule was that where two bindings
// disagreed the FIRST was kept. That rule was not arbitrary — it is what an
// HTML parser already did with the duplicate attribute this function replaces,
// and it existed so that moving a page from two spreads to one OnAll could not
// silently change which debounce was in force for the binding that survived.
//
// Per-binding scoping keeps that property and extends it: composition now
// changes nothing about ANY binding, not merely about the first. So there is no
// longer a disagreement to resolve, and the rule is vacuous rather than
// changed. What remains here is a defensive carry-through — On and OnWith emit
// exactly one attribute each, and anything else spread in is copied with the
// first occurrence winning.
//
// The property is worth stating in the direction a reader will need it: two
// bindings that ask for different intervals now get different intervals. Before
// 2026-08-05 the second one silently got the first one's, which on the guide's
// own composer meant an Escape binding inherited a 150 ms debounce and the next
// keystroke destroyed the pending clear outright. Bind carries the measurement.
func OnAll(bindings ...templ.Attributes) templ.Attributes {
	out := templ.Attributes{}
	var on []string
	for _, b := range bindings {
		for name, value := range b {
			if name == attrOn {
				continue
			}
			if _, seen := out[name]; !seen {
				out[name] = value
			}
		}
		// Read separately from the range above: a map iterates in a random
		// order, and the order of the bindings is what decides which one a
		// key filter reaches.
		if s, ok := b[attrOn].(string); ok && s != "" {
			on = append(on, s)
		}
	}
	if len(on) > 0 {
		out[attrOn] = strings.Join(on, ";")
	}
	return out
}

// binding renders one binding, or one per key when the binding is filtered.
//
// The grammar is
//
//	<domEvent>:<eventName>[:<key>[:<debounceMs>[:<throttleMs>[:<fields>
//	    [:<noModifiers>[:<preventDefault>]]]]]]
//
// with trailing empty components trimmed, and several bindings joined by ";".
// client/SIZE.md §7 is the copy of this the client runtime is held to.
//
// A key list is several bindings rather than a separated list inside one,
// which is the whole reason no separator has to be reserved for it: every
// printable character is a legal KeyboardEvent.key value, including the comma
// that would otherwise be the obvious choice. Each of those bindings carries
// the whole Bind's options, so "+" and "-" cannot differ by which the author
// wrote first.
//
// The three components after the key were added for FR-54 failure 2, and they
// reserve nothing new. ":" was already spent — Bind.Keys has said since
// 591c275a that a key cannot be ":" or ";" — so extending the same separator
// costs a reader no character they previously had. The alternative shapes each
// did: a "," or "|" list inside one component would have taken a printable key
// value away from Bind.Keys, and a second attribute of the element is the
// defect being fixed.
//
// Fields go last and are safe there because encodeFields is net/url's query
// encoding, which escapes ":" and ";" in both keys and values. That is asserted
// rather than assumed — see live/binding_test.go — because it is the only
// component whose content is a caller's data.
//
// Components seven and eight — NoModifiers and PreventDefault — were added for
// FR-54 failure 1 and reserve nothing new either, for the same reason: they are
// two more ":" fields of a split that was already happening. Each renders "1"
// when set and nothing when not, so trimEmpty drops both for every Bind that
// does not set them and EVERY binding this library rendered before they existed
// is byte-identical after. That is why they go on the end rather than beside
// the key they most often accompany.
//
// A boolean is spelled "1" rather than "true" because the client tests the
// component for truthiness and an empty string is the only falsey value the
// grammar can produce — so "1" costs one byte, needs no parse, and cannot be
// spelled two ways by two writers.
//
// A sub-millisecond Debounce or Throttle renders as "0", which the client reads
// as none. That is unchanged behaviour, stated here because it now shows up as
// a component rather than as an attribute.
//
// # What it refuses, and why it panics rather than returning
//
// Four inputs the grammar cannot carry are refused before anything is written:
// a ":" or ";" in domEvent, in eventName or in a Bind.Keys entry, and an empty
// eventName. See refuseUnbindable, which is where the argument is.
func binding(domEvent, eventName string, b Bind) string {
	// First, and before any component is even rendered: refuse rather than
	// repair, on the argument normalizeMount states at length one screen down.
	// Nothing below this line can produce a spec the client mis-parses.
	refuseUnbindable(domEvent, eventName, b.Keys)

	// Components seven and eight, spelled inline rather than through a helper
	// so this landing adds no identifier of any kind — the count in
	// docs/api-surface.md §0 is what FR-54 failure 1 was accepted at.
	noMods, prevent := "", ""
	if b.NoModifiers {
		noMods = "1"
	}
	if b.PreventDefault {
		prevent = "1"
	}

	opts := trimEmpty([]string{
		millis(b.Debounce),
		millis(b.Throttle),
		encodeFields(b.Fields),
		noMods,
		prevent,
	})

	// One binding per key; no key at all is one binding with an empty key
	// component, which the client reads as "every key" exactly as a missing
	// one does.
	keys := b.Keys
	if len(keys) == 0 {
		keys = []string{""}
	}

	var out strings.Builder
	for i, key := range keys {
		if i > 0 {
			out.WriteByte(';')
		}
		out.WriteString(domEvent)
		out.WriteByte(':')
		out.WriteString(eventName)
		if key == "" && len(opts) == 0 {
			continue
		}
		out.WriteByte(':')
		out.WriteString(key)
		for _, o := range opts {
			out.WriteByte(':')
			out.WriteString(o)
		}
	}
	return out.String()
}

// refuseUnbindable panics on an input this grammar cannot carry.
//
// # What it refuses
//
// A ":" or a ";" anywhere in domEvent, in eventName or in a Bind.Keys entry,
// and an empty eventName. Those four and nothing else — an unrecognised DOM
// event, an unrecognised key name and an empty Bind.Keys entry are all legal
// and documented, and a static field may hold either separator because
// encodeFields escapes both in keys and values alike.
//
// # Why
//
// ":" separates the components of one binding and ";" separates the bindings on
// an element, so a component carrying one renders as more than one component
// and every component after it lands one slot later than the client reads it.
// Before per-binding options that cost a key filter; since 2026-08-05 it also
// moves Debounce into Throttle's slot, which means a page that declared a 150 ms
// debounce got a 150 ms throttle instead — a different behaviour, silently, from
// an input Bind.Keys' own documentation calls inexpressible.
//
// The empty event name is the sharpest of the four and the only one that
// predates that landing. dispatch() breaks out of its match loop on the first
// spec whose DOM event and key filter match and tests the name only afterwards,
// so a binding with an empty name MATCHES, ends the loop, and silences every
// binding behind it on the same DOM event.
//
// # Why a panic and not an error
//
// Because there is nowhere to return one to, and a value that cannot be
// returned would be worse than useless here. On and OnWith return
// templ.Attributes, which is a map[string]any with no error channel, and
// templ.RenderAttributes (v0.3.1020) switches on each value's dynamic type with
// NO default case: a value of any other type — an error included — is skipped
// in silence and the attribute simply does not appear. So an error smuggled
// into the map would produce an element with no binding on it at all, which is
// exactly the silent no-op the attribute vocabulary at the top of this file
// exists to prevent, and it would be produced BY the mechanism meant to report
// it.
//
// A panic carrying a full sentence is what this library already does for a
// programmer error with nowhere to return one: (*App).PageHandler on a nil page
// and (*App).Mux on a nil handler or a mount of "/". The precedent fits for the
// reason (*App).Mux gives for borrowing it from http.ServeMux.Handle — every
// one of these four is a literal in the caller's source, so it is a startup
// mistake rather than a condition a running server can be in, and it fires on
// the first render of that view rather than on some visitor's request.
//
// docs/error-audit.md §3.3.1 rules that §2.1's census does not cover panics —
// "a panic carrying a sentence" is graded there as §6's fourth weakness rather
// than as a site — so these four sites move neither the FR-58 census nor
// internal/arch/errors_test.go's count, and internal/arch's walk agrees: it
// counts errors.New, fmt.Errorf, protocol.reject and *Error literals, and
// fmt.Sprintf is none of them.
func refuseUnbindable(domEvent, eventName string, keys []string) {
	if i := strings.IndexAny(domEvent, ":;"); i >= 0 {
		panic(fmt.Sprintf(
			"gotth-live: live.On or live.OnWith was given the DOM event %q, which contains %q: "+
				`":" separates the components of one binding and ";" separates the bindings on `+
				"an element, so this name renders as more than one component and the event name, "+
				"the key filter and every option behind it land one slot later than the client "+
				"reads them — the binding names a DOM event nothing raises and fires never. "+
				"Neither character can be escaped in this grammar. Pass the browser's own event "+
				`type, such as "click" or "keydown"`,
			domEvent, domEvent[i:i+1]))
	}
	if eventName == "" {
		panic(fmt.Sprintf(
			"gotth-live: live.On or live.OnWith was given an empty event name for the DOM event "+
				"%q: the client matches a binding on its DOM event and its key filter and reads "+
				"the name only afterwards, so an empty name renders a spec that MATCHES, ends the "+
				"match loop, and silences every binding behind it on the same DOM event — "+
				"including ones that would otherwise have fired. Pass the server event's own "+
				`name, the string the handler switches on, such as "counter.inc"`,
			domEvent))
	}
	if i := strings.IndexAny(eventName, ":;"); i >= 0 {
		panic(fmt.Sprintf(
			"gotth-live: live.On or live.OnWith was given the event name %q, which contains %q: "+
				`":" separates the components of one binding and ";" separates the bindings on `+
				"an element, so this name renders as more than one component — its tail is read "+
				"as a key filter, which an event carrying no key never matches, and every option "+
				"behind it lands one slot later, so a Bind.Debounce arrives at the client as a "+
				"throttle. Neither character can be escaped in this grammar. Name the server "+
				`event without one, such as "chat.send"`,
			eventName, eventName[i:i+1]))
	}
	for _, k := range keys {
		if i := strings.IndexAny(k, ":;"); i >= 0 {
			panic(fmt.Sprintf(
				"gotth-live: live.OnWith was given %q in Bind.Keys, which contains %q: "+
					`":" separates the components of one binding and ";" separates the bindings `+
					"on an element, so a key that is one of those two characters cannot be "+
					"expressed here — rendered, it would widen this filter to EVERY key and move "+
					"every option declared beside it one slot later, so a Bind.Debounce arrives "+
					"at the client as a throttle. Bind.Keys carries every other printable "+
					"KeyboardEvent.key value through unchanged and these two are the whole of "+
					"what it cannot carry; there is no escape for them. Bind a different key",
				k, k[i:i+1]))
		}
	}
}

// millis renders a duration the way the client parses it, or "" for an option
// that was not set.
func millis(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	return strconv.FormatInt(d.Milliseconds(), 10)
}

// trimEmpty drops trailing empty components, so a binding that sets only a
// debounce does not render the two placeholders behind it.
func trimEmpty(components []string) []string {
	for len(components) > 0 && components[len(components)-1] == "" {
		components = components[:len(components)-1]
	}
	return components
}

// encodeFields renders the static fields as a URL-encoded query string, sorted
// by key, so the same Bind always produces byte-identical markup and a render
// stays deterministic.
func encodeFields(fields map[string]string) string {
	values := make(url.Values, len(fields))
	for k, v := range fields {
		values.Set(k, v)
	}
	return values.Encode()
}

// Preserve marks an element and its subtree as never morphed.
//
// It is the sanctioned way to host HTMX- or third-party-JS-owned DOM inside a
// live region. The rule is innermost-declaration-wins: an hx-* element inside a
// live fragment without Preserve is server-owned, morph will overwrite it, and
// any swap into it will be reverted by the next patch.
func Preserve() templ.Attributes {
	return templ.Attributes{attrPreserve: true}
}

// Script renders the script tag for the embedded client runtime, for an
// application whose handler is mounted at mountPath. There is no CDN and no
// build step: the runtime is compiled into the binary and served by the same
// handler that serves the connection.
//
// mountPath is the prefix the handler is reachable at as the *browser* sees
// it — "/live", "/app/live" — and it is a parameter because it is knowledge
// only the caller has. App.Handler is an http.Handler; the router strips the
// prefix before the handler sees a request, and this renders on a different
// request entirely, so no check inside this library can observe a mismatch. A
// default was worse than no default: mounted at "/app/", the tag pointed at
// "/live/gotth-live.min.js", the page loaded, the script 404'd, and nothing
// was live with no server-side error anywhere — the same silent-no-op failure
// the attribute vocabulary above exists to prevent, eighty lines below the
// comment saying so.
//
// It is a path-only, same-origin reference — that prefix and nothing else —
// emitted unchanged apart from trimming at most one trailing "/", so "/live"
// and "/live/" render identically.
//
// Anything else makes Render return an error and emit no tag: a mountPath that
// is empty or does not begin with "/", or that contains "//" anywhere, "\",
// "?", "#", or a byte below 0x20 or equal to 0x7F. Each is a string a browser
// reads as something other than a path. "//" and "\" begin an authority, so
// "/"+prefix+"/live" with an empty prefix names a host called "live" and sends
// both the runtime fetch and the session's WebSocket there; "?" and "#" end
// the path, so the runtime filename appended to it is never fetched, and "#"
// makes the WebSocket constructor throw outright; and browsers strip control
// bytes from a URL before parsing it, so the path requested is not the path
// written. The error lands on the page request, where a handler already has an
// error path. A 500 is a better answer than a blank page.
//
// Percent-encoding, ".." segments and spaces are accepted: they are the
// caller's business and a browser resolves them to a same-origin path.
//
// # One place it refuses to render at all
//
// Rendered inside the head content of [App.Document], this returns an error and
// emits no tag. That component already renders this tag, and it renders it
// below [App.InspectorScript]'s, which is the order the inspector needs; a
// second tag from the head content would land ABOVE the inspector's, and since
// both are deferred and deferred scripts run in document order, the runtime
// would open its socket before the inspector wrapped WebSocket. The inspector
// would then show nothing, silently, which is the failure that component exists
// to make unwritable. So the mistake is a 500 on the page request instead —
// [App.PageHandler] renders into a buffer, so nothing half-written reaches the
// browser.
//
// Nowhere else is affected. A hand-written shell calls this under a context
// [App.Document] never touched, and a document given [NoRuntime] renders no
// runtime tag of its own and therefore refuses none of yours.
func Script(mountPath string) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		// Before the mount path, because a caller who has made both mistakes
		// should be told about the structural one: the mount path is fixable
		// and this call still should not be here.
		if runtimeTagRefused(ctx) {
			return errRuntimeTagInDocumentHead
		}
		mount, err := normalizeMount(mountPath)
		if err != nil {
			return err
		}
		// Both attributes come from that one normalisation. Deriving src by
		// trimming a second time, as this once did, is how "/live//" rendered
		// src="/live/…" beside data-gotth-url="/live/" — two attributes
		// naming different mounts. The root is the only mount that already
		// ends in the separator, and joining it again would spell "//": the
		// authority normalizeMount refuses in every other position.
		src := mount + "/" + clientRuntimeFile
		if mount == "/" {
			src = mount + clientRuntimeFile
		}
		// templ.EscapeString, not %q. %q is Go quoting: it renders " as \",
		// which closes the attribute and leaves the backslash in the value, and
		// it passes & through, so "/reports&sect;ion" reaches the browser as
		// "/reports§ion" — a path the caller never wrote. This is the module's
		// one hand-rolled markup writer; escaping here means it is correct on
		// its own terms rather than because normalizeMount happens to forbid
		// the characters that would break it.
		_, err = io.WriteString(w,
			`<script src="`+templ.EscapeString(src)+`" `+
				attrScriptURL+`="`+templ.EscapeString(mount)+`" defer></script>`)
		return err
	})
}

// InspectorScript renders the script tag for the dev session inspector, for an
// application whose handler is mounted at mountPath.
//
// The inspector is a floating panel showing the causal chain of the session
// this page is running: every event the browser sent, the event id, transition
// and state version the server minted for it, and the patches each produced,
// joined by the causal identifiers the frames already carry (FR-39 through
// FR-42). It also flags `hx-*` attributes inside an unpreserved live fragment,
// which morph will overwrite (RFC-0001 §10.3). docs/guide/inspector.md is the
// user-facing page.
//
// # It renders nothing unless Config.Dev is set
//
// With Dev false — the zero value, and what production must run (see that
// field) — this writes zero bytes and returns nil, and the route serving the
// inspector's JavaScript answers 404. Those are two independent gates on one
// switch, and they are what PRD NFR-8's "MUST NOT load in production builds"
// means here: a production page has no tag naming the file, and a production
// binary would not serve the file to a browser that asked for it anyway.
//
// A component that renders nothing is normally this library's least favourite
// shape — Script's own documentation argues at length against a silent no-op.
// The difference is that here the silence IS the requirement, it is keyed to a
// field the application set deliberately, and it fails in the safe direction:
// the mistake this could produce is a developer wondering where their panel
// went, not a page that is quietly not live.
//
// # Order matters, and it is not checkable from here
//
// This tag MUST come BEFORE live.Script's. The inspector reads the session's
// frames off the WebSocket, which means it must wrap the constructor before
// the runtime opens a socket; both tags are deferred, and deferred scripts run
// in document order. Getting it wrong does not break the page — the inspector
// detects that the runtime booted first and says so in its own panel — but it
// shows nothing until the next reconnect.
//
// mountPath is validated exactly as Script validates it, by the same function,
// and the same mount produces the same prefix in both tags. It is a parameter
// for the reason it is a parameter there: the prefix as the browser sees it is
// knowledge only the caller has, and a default would point at a file that
// 404s.
func (a *App[S, I]) InspectorScript(mountPath string) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		// Validated even when nothing is written, so that a bad mount path is
		// an error in dev and in production alike. A component whose argument
		// is only checked in one mode is a component that starts failing when
		// somebody flips the mode.
		mount, err := normalizeMount(mountPath)
		if err != nil {
			return err
		}
		if !a.cfg.Dev {
			return nil
		}
		src := mount + "/" + clientInspectorFile
		if mount == "/" {
			src = mount + clientInspectorFile
		}
		_, err = io.WriteString(w, `<script src="`+templ.EscapeString(src)+`" defer></script>`)
		return err
	})
}

// normalizeMount validates a mount path and strips one trailing slash.
//
// Only one is stripped, and only at the end. A path is a routing decision the
// caller made, and silently rewriting more of it than the one character that
// is genuinely ambiguous — mux.Handle wants "/live/", a src attribute wants
// "/live" — would make this function a second, quieter router.
//
// That same argument is why the checks below refuse rather than repair. The
// instinct on reading them is to normalise: collapse "//" to "/", drop the
// query. Doing so would rewrite a routing decision silently, which is exactly
// what the paragraph above declines to do — and the caller who wrote "//live"
// meant something, they just did not mean the host "live". Refusing is the
// point.
//
// The rule is positive — a mount path is a path and nothing else — rather than
// a list of bad prefixes, because the parser that decides is the browser's and
// this project does not own it: browsers follow the WHATWG URL Standard, not
// RFC 3986, and net/url calls both "///host" and "/\host" same-origin when a
// real browser does not. Each clause names the browser behaviour it prevents.
//
// Deliberately not rejected, and not to be added without browser evidence:
// percent-encoding ("/%2f%2f…" is not a bypass, it stays same-origin), ".."
// segments (the browser normalises them and the result is the caller's), and
// spaces.
func normalizeMount(mountPath string) (string, error) {
	return normalizeMountFor("live.Script", mountPath)
}

// normalizeMountFor is normalizeMount with the caller's own name in the error.
//
// One rule, several callers: Script and the two dev-only script components
// render a mount path, and (*App).Mux ROUTES with one. They are entitled to the
// same predicate and they are not entitled to the same sentence — an error
// blaming live.Script for a string the caller handed to Mux sends a reader to
// the wrong line. The subject is a parameter for that reason and for no other;
// every clause below is unchanged.
func normalizeMountFor(who, mountPath string) (string, error) {
	if mountPath == "" {
		return "", fmt.Errorf(
			"gotth-live: %s needs the path the handler is mounted at, as the browser sees it, "+
				`such as "/live": the client runtime is served by that handler, so an empty mount `+
				"cannot address it", who)
	}
	if !strings.HasPrefix(mountPath, "/") {
		return "", fmt.Errorf(
			"gotth-live: %s was given the mount path %q, which is not absolute: "+
				`the browser resolves this against the page's own URL, so it must begin with "/"`,
			who, mountPath)
	}
	// Checked against the string as given, before the trailing slash is
	// trimmed: "//" would otherwise trim to "/" and pass.
	if strings.Contains(mountPath, "//") {
		return "", fmt.Errorf(
			"gotth-live: %s was given the mount path %q, which contains \"//\": "+
				`a leading "//" begins an authority, so "//live" names the host "live" and sends `+
				"both the runtime fetch and this session's WebSocket there, and an inner or "+
				"trailing one is an empty path segment no router registered",
			who, mountPath)
	}
	if strings.Contains(mountPath, `\`) {
		return "", fmt.Errorf(
			"gotth-live: %s was given the mount path %q, which contains a backslash: "+
				`for http and https the browser's URL parser treats "\" as "/", so "/\host" `+
				`begins an authority exactly as "//host" does, and one backslash and two behave `+
				"identically",
			who, mountPath)
	}
	if i := strings.IndexAny(mountPath, "?#"); i >= 0 {
		return "", fmt.Errorf(
			"gotth-live: %s was given the mount path %q, which contains %q: "+
				"a query or fragment ends the path, so the runtime filename appended to this "+
				`mount lands inside one and is never fetched — and "#" additionally makes the `+
				"browser's WebSocket constructor throw",
			who, mountPath, mountPath[i:i+1])
	}
	for i := 0; i < len(mountPath); i++ {
		if b := mountPath[i]; b < 0x20 || b == 0x7f {
			return "", fmt.Errorf(
				"gotth-live: %s was given the mount path %q, which contains the "+
					"control byte %#02x: browsers remove tab, CR and LF from a URL before "+
					"parsing it, so the path the browser requests is not the path written here",
				who, mountPath, b)
		}
	}
	if mountPath == "/" {
		return "/", nil
	}
	return strings.TrimSuffix(mountPath, "/"), nil
}

// serveClientRuntime serves the embedded runtime.
//
// The artifact is immutable for the life of a build, so it is served with a
// strong ETag and a long max-age: a client that reconnects a thousand times
// fetches it once.
func serveClientRuntime(w http.ResponseWriter, r *http.Request) {
	serveAsset(w, r, clientRuntime, clientRuntimeETag)
}

// serveClientInspector serves the embedded dev inspector.
//
// It is reached only from the Dev arm of routes(), so a production build never
// calls it. The response is otherwise identical to the runtime's, including
// the immutable caching: a developer reloading a page fifty times should not
// re-fetch 15 KB fifty times, and the artifact changes only when the binary
// does.
func serveClientInspector(w http.ResponseWriter, r *http.Request) {
	serveAsset(w, r, clientInspector, clientInspectorETag)
}

// serveAsset serves one immutable embedded file.
//
// One function over both artifacts, because the alternative is two copies of a
// conditional-request implementation that then differ: the runtime's honours
// If-None-Match and HEAD, and the second copy would be the one that quietly
// did not.
func serveAsset(w http.ResponseWriter, r *http.Request, body []byte, etag string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		return
	}
	_, _ = w.Write(body)
}
