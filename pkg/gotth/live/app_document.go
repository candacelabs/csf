package live

import (
	"context"
	"errors"
	"io"

	"github.com/a-h/templ"
)

// NoRuntime is the mountPath value that tells [App.Document] this page is
// deliberately not live.
//
// It exists because "omit the runtime" cannot be spelled as an absence. A
// missing mount path, an empty string or a nil field would all mean "the author
// forgot", and the resulting page loads perfectly and does nothing — the exact
// silent failure [Script] refuses a default in order to prevent. So the
// omission is a value somebody wrote down, in the shape this library already
// uses for every other opt-out: [AnyOrigin], [Anonymous], [AllowAll] and
// [NoCSRFCheck] are all named, greppable symbols for the same reason, and
// auditing every page in a deployment that carries no runtime is one search for
// this identifier.
//
// It is not a path and no router will ever register it: it does not begin with
// "/", so every other function here that takes a mount path refuses it.
//
// A document with no runtime also carries no inspector and no dev-reload tag.
// That is not an oversight and it is not a limit of the implementation: all
// three address the mount, and NoRuntime is the statement that this page does
// not talk to the live handler at all. An application that wants the dev-reload
// tag on a page that is otherwise static can pass
// (*App).DevReloadScript(mountPath) as head content — its position on the page
// is documented not to matter, which is what makes that safe.
const NoRuntime = "no-runtime"

// Document renders the whole HTML document around this application's page
// content: the doctype, the <html> element, a <head> carrying the character
// encoding, the title and the client runtime, and a <body> holding the children
// this component is given.
//
//	templ Page(s State) {
//		@app.Document(MountPath, "gotth-live quickstart", templ.Attributes{"lang": "en"}) {
//			@Count(s)
//		}
//	}
//
// # Why this is a method, and what that buys
//
// [Script] is a package-level function, and everything about this component
// says it should be one too — until the dev tags. [App.InspectorScript] MUST be
// rendered above [Script]'s tag (both are deferred, deferred scripts run in
// document order, and the inspector has to wrap the WebSocket constructor
// before the runtime opens a socket), and both dev tags are methods because
// what they emit depends on [Config.Dev], which is state the application
// declared once. A package-level shell could emit [Script] and nothing else,
// and would then leave the application placing the inspector *relative to a tag
// it can no longer see* — an ordering it can only get wrong, against a marker
// this component has taken away. That is a worse page than the hand-written
// shell it replaces.
//
// So this is a method, it emits all three tags itself, and the application
// never places any of them. **The ordering invariant is not preserved here, it
// is made inexpressible**: no argument to this component, in any order, can
// produce an inspector tag below a runtime tag. That takes two mechanisms
// rather than one, because the first has a hole in it and the hole is the
// failure itself:
//
//   - Head content renders ABOVE the three tags, so an application that renders
//     its own [App.InspectorScript] there still lands above [Script]. That half
//     falls out of the ordering and needs nothing.
//   - Head content that renders a RUNTIME tag of its own would land above the
//     inspector, and that is the ordering failure and not merely a duplicate:
//     both tags are deferred, deferred scripts run in document order, and the
//     runtime would open its socket before the inspector wrapped WebSocket. So
//     it is refused. While this component renders head content it marks the
//     context; [Script] reads the mark and returns an error; and the page
//     becomes [App.PageHandler]'s 500 with a named reason instead of a page
//     whose inspector silently sees nothing. A whole [App.Document] nested in
//     the head is refused by the same mark, because its own [Script] call
//     renders under it.
//
// The mark is set around the head content and nowhere else, and only when this
// document is emitting the runtime itself. Two things therefore stay
// expressible, both deliberately:
//
//   - [Script] among this component's CHILDREN renders. It lands below the
//     inspector, so the ordering holds; what remains is a duplicate runtime tag,
//     which is two sockets on one page — a real defect, with a different shape,
//     and not the one this mark is for.
//   - A document given [NoRuntime] emits none of the three and sets no mark, so
//     placing [Script] by hand on a page that has declared itself not live
//     works, and there is no inspector for it to be ordered against.
//
// Reaching the App from the page function is the caller's problem and it has a
// zero-cost answer: [App.PageHandler] takes a func(state S) templ.Component and gives
// it no receiver, so the application holds its App wherever it already holds
// its state type — docs/quickstart.md makes it a package-level var, the
// examples pass it into the templ component as a parameter. Both are one line
// that was already there.
//
// # What it owns, and what stays the application's
//
// It owns exactly the parts of a document that are the same in every live
// application and that nothing above it can get right: the doctype, the
// character encoding declaration (first in the head, where a byte-counting
// parser needs it), and the placement of the runtime, inspector and dev-reload
// tags.
//
// Everything else is the application's, and is a parameter here rather than a
// default:
//
//   - title is required. A document's title is content, this library has no
//     defensible guess at it, and an empty <title> is an accessibility failure
//     that renders as a blank tab rather than as an error. So an empty title is
//     an error and no bytes are written.
//   - htmlAttrs are the attributes of the <html> element, and lang is among
//     them. A live-connection library does not choose a document's language:
//     there is no default here, a nil map is a document that says nothing, and
//     nothing is added to what the caller passes. They are rendered by the same
//     function templ's own attribute spread uses, in sorted key order, so one
//     call always produces one byte sequence.
//   - head is any number of components rendered into the head after the title
//     and before the runtime tags — the viewport meta, the stylesheet, an
//     application's own script. It is variadic so that a page needing none pays
//     nothing for it, not even a nil.
//
// The <body> element carries no attributes and this component provides no way
// to give it any. Every hand-written shell in this repository has a bare
// <body>; if one ever needs otherwise, that is an argument for a parameter, and
// it should be made rather than worked around by abandoning the component.
//
// # Failure
//
// mountPath is validated exactly as [Script] validates it, by the same
// function, and both it and the title are checked BEFORE anything is written.
// A failure therefore emits zero bytes and returns the error, which is what
// keeps [App.PageHandler]'s buffered render honest: a page that cannot be
// rendered correctly is a 500 carrying a logged reason, never a 200 carrying
// half a document. The errors from the head components, from the runtime tags
// and from the children are returned unchanged for the same reason — this
// component swallows none of them.
//
// Pass [NoRuntime] as mountPath for a page in a live application that is
// deliberately not live, such as a login page: it emits no runtime tag, no
// inspector and no dev-reload tag, and it is the only spelling that does.
func (a *App[S, I]) Document(
	mountPath, title string,
	htmlAttrs templ.Attributes,
	head ...templ.Component,
) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		withRuntime := mountPath != NoRuntime
		if withRuntime {
			if _, err := normalizeMountFor("(*live.App).Document", mountPath); err != nil {
				return err
			}
		}
		if title == "" {
			return errNoTitle
		}

		// Taken before anything renders, and cleared for everything this
		// component renders itself: the children belong to the body, and a
		// head component that takes children of its own must not be handed
		// the page's.
		children := templ.GetChildren(ctx)
		ctx = templ.ClearChildren(ctx)

		if _, err := io.WriteString(w, "<!doctype html><html"); err != nil {
			return err
		}
		// templ's own function, not a hand-rolled writer: it escapes both key
		// and value, it renders a nil map as nothing, and it sorts, so the
		// same attributes always produce the same bytes.
		if err := templ.RenderAttributes(ctx, w, htmlAttrs); err != nil {
			return err
		}
		// The charset is first in the head because a browser that has not yet
		// found one is guessing, and the guess is only revised within the
		// first kilobyte. The title is second because everything after it is
		// the application's.
		if _, err := io.WriteString(w,
			`><head><meta charset="utf-8"><title>`+templ.EscapeString(title)+`</title>`); err != nil {
			return err
		}
		// The head content is the one region of this document where a runtime
		// tag of the application's own would land ABOVE the inspector, so it is
		// the one region where Script refuses to render. Marked here and
		// nowhere else: the three tags below render under the unmarked ctx
		// because one of them IS Script, and the children render under it
		// because a runtime tag down there is a duplicate rather than a
		// misordering. Only when this document is emitting the runtime — with
		// NoRuntime there is no inspector for anything to be ordered against.
		headCtx := ctx
		if withRuntime {
			headCtx = withRuntimeTagRefused(ctx)
		}
		for _, h := range head {
			if h == nil {
				continue
			}
			if err := h.Render(headCtx, w); err != nil {
				return err
			}
		}
		if withRuntime {
			// The order this whole component exists to own. InspectorScript
			// and DevReloadScript write zero bytes unless Config.Dev is set,
			// so in production this is one tag.
			for _, tag := range []templ.Component{
				a.InspectorScript(mountPath),
				Script(mountPath),
				a.DevReloadScript(mountPath),
			} {
				if err := tag.Render(ctx, w); err != nil {
					return err
				}
			}
		}
		if _, err := io.WriteString(w, "</head><body>"); err != nil {
			return err
		}
		if err := children.Render(ctx, w); err != nil {
			return err
		}
		_, err := io.WriteString(w, "</body></html>")
		return err
	})
}

// runtimeTagRefusedKey marks the context under which [Script] must refuse.
//
// It is an empty struct type rather than a string so that no other package can
// name it, which is the context.WithValue convention and is also the whole of
// the mark's blast radius: nothing outside this file sets it, one function
// reads it, and a context that has never been through Document's head content
// cannot carry it. The four hand-written shells still in this tree call Script
// under a context this never touches and are unaffected.
type runtimeTagRefusedKey struct{}

// withRuntimeTagRefused marks ctx so that [Script] refuses to render under it.
func withRuntimeTagRefused(ctx context.Context) context.Context {
	return context.WithValue(ctx, runtimeTagRefusedKey{}, true)
}

// runtimeTagRefused reports whether ctx carries the mark.
func runtimeTagRefused(ctx context.Context) bool {
	marked, _ := ctx.Value(runtimeTagRefusedKey{}).(bool)
	return marked
}

// errRuntimeTagInDocumentHead is [Script]'s refusal under Document's head mark.
//
// It is authored here rather than in templ.go because the condition is this
// file's: Document sets the mark, and the sentence has to explain a component
// the reader may not have been looking at. Package-level errors.New for the
// reasons errNoTitle and errNilPage are.
var errRuntimeTagInDocumentHead = errors.New(
	"gotth-live: live.Script was rendered inside the head content of (*live.App).Document, " +
		"which renders the runtime's script tag itself and renders it BELOW the dev inspector's: " +
		"a second tag from here lands above the inspector, both are deferred, deferred scripts run " +
		"in document order, and a runtime that opens its socket before the inspector wraps " +
		"WebSocket leaves the inspector showing nothing at all. Delete this call — Document emits " +
		"the runtime, the inspector and the dev-reload tag in the one order that works. A page " +
		"that must place its own runtime tag is a page that is not using Document's: pass " +
		"live.NoRuntime as the mount path, which makes Document emit none of the three")

// errNoTitle is the refusal of a document with no title.
//
// It is package-level and an ordinary errors.New for the reasons errNilPage is
// (see page.go): one sentence wherever it surfaces, and a message a human wrote
// is a message FR-58's census grades. It names the next step, because the
// caller holding this value is an application author with a page that did not
// render.
var errNoTitle = errors.New("gotth-live: (*live.App).Document was given an empty title: " +
	"a document's title is application content and this component has no default for it — " +
	"pass the page's own title as the second argument")
