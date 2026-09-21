package render

import (
	"context"
	"fmt"
	"hash/maphash"
	"io"
	"strings"
)

// Op is how a fragment update is applied to the DOM. It mirrors the wire
// enumeration without naming it: the render path is a pure function of state
// and does not depend on the protocol package, which is what keeps its import
// allowlist meaningful.
type Op int

// The operations. OpUnspecified is the zero value and is never emitted; it
// exists so that a fragment that forgot to name an operation fails at the
// outbound boundary rather than arriving as a silent morph.
const (
	OpUnspecified Op = iota
	OpMorph
	OpAppend
	OpPrepend
	OpRemove
)

// Update is the new markup for one live region.
type Update struct {
	// FragmentID is the region the markup belongs to, unique within an
	// application.
	FragmentID string

	// Op is how the client applies it. OpUnspecified never reaches here: the
	// outbound boundary refuses it rather than letting it arrive as a silent
	// morph.
	Op Op

	// HTML is the fragment's complete rendered markup, not a diff. The diff
	// happens in the browser, against the live DOM.
	HTML string
}

// RenderFunc writes one fragment's markup for a state value.
//
// It takes any rather than a type parameter because the session actor holds
// application state as an opaque value; the public package closes over the
// type and hands this package a function that has already asserted it.
//
// # The writer is valid only for the duration of the call
//
// w is a handle onto per-session storage that is reused for every fragment of
// every pass, and the renderer reads what was written the moment this function
// returns. Writing to it afterwards — from a goroutine the render started, or
// from a handle a later call retained — is refused with an error rather than
// silently landing in another fragment's markup. Nothing about w may be
// retained, and the underlying buffer is deliberately unreachable: w is not a
// *bytes.Buffer and a type assertion to one fails, because .Bytes() would hand
// out a live view of storage the next fragment overwrites (U-6).
type RenderFunc func(ctx context.Context, state any, w io.Writer) error

// DirtyFunc reports whether a transition may have changed a fragment. A nil
// DirtyFunc means "re-render on every transition", which is always safe:
// over-declaring costs a suppressed render, under-declaring costs a stale
// fragment in production.
type DirtyFunc func(prev, next any) bool

// Fragment declares one server-owned live region.
type Fragment struct {
	// ID is the fragment's stable identity. It must match the schema's
	// pattern and be unique within an application.
	ID string
	// Render writes the fragment's markup. It must be a pure function of
	// state: same state, byte-identical output, across runs and processes.
	Render RenderFunc
	// Dirty is the optional change declaration.
	Dirty DirtyFunc

	// Children projects ordered nested regions from session state.
	Children func(state any) []Fragment
}

// Registry is an application's fragment set. It is immutable after
// construction and shared by every session, which is why it holds no
// per-session state: the render hashes live in a Renderer.
type Registry struct {
	frags []Fragment
	index map[string]int
	seed  maphash.Seed
}

// NewRegistry validates a fragment set and returns it.
//
// A duplicate identifier is an error naming both declarations rather than a
// silent last-write-wins, and it is reported at construction so it fails at
// startup instead of at the first patch that goes to the wrong region.
func NewRegistry(frags []Fragment) (*Registry, error) {
	if len(frags) == 0 {
		return nil, fmt.Errorf("gotth-live: an application declares no fragments: declare at least one live region")
	}

	index := make(map[string]int, len(frags))
	for i, f := range frags {
		if f.ID == "" {
			return nil, fmt.Errorf("gotth-live: fragments[%d] declares no ID: every live region needs a stable identity", i)
		}
		if err := validFragmentID(f.ID); err != nil {
			return nil, fmt.Errorf("gotth-live: fragments[%d] (%q): %w", i, f.ID, err)
		}
		if f.Render == nil {
			return nil, fmt.Errorf("gotth-live: fragments[%d] (%q) declares no Render: a live region must render", i, f.ID)
		}
		if prev, dup := index[f.ID]; dup {
			return nil, fmt.Errorf(
				"gotth-live: fragments[%d] and fragments[%d] both declare the fragment ID %q: "+
					"give each live region a distinct identity", prev, i, f.ID)
		}
		index[f.ID] = i
	}

	for _, parent := range frags {
		if parent.Children == nil {
			continue
		}
		for _, other := range frags {
			if other.ID != parent.ID && strings.HasPrefix(other.ID, parent.ID+":") {
				return nil, fmt.Errorf("gotth-live: fragment %q overlaps the child namespace of %q: use disjoint region identities", other.ID, parent.ID)
			}
		}
	}

	return &Registry{
		frags: append([]Fragment(nil), frags...),
		index: index,
		seed:  maphash.MakeSeed(),
	}, nil
}

// validFragmentID applies the schema's pattern here rather than at the
// outbound boundary, so a typo is a startup error naming the fragment instead
// of a dropped patch at runtime.
func validFragmentID(id string) error {
	if len(id) > 64 {
		return fmt.Errorf("a fragment ID is at most 64 bytes and this one is %d: shorten it — "+
			"the bound is the wire schema's, so a longer identity is a patch this library "+
			"builds and then refuses to send", len(id))
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '_', c == ':', c == '.', c == '-':
		default:
			return fmt.Errorf("a fragment ID may hold only letters, digits and _:.- , not %q: "+
				"remove that byte — the charset is the wire schema's, and a patch naming this "+
				"region could not be sent", string(c))
		}
	}
	return nil
}

// Len reports how many fragments the application declares.
func (r *Registry) Len() int { return len(r.frags) }

// IDs returns the declared fragment identifiers, in declaration order.
func (r *Registry) IDs() []string {
	out := make([]string, len(r.frags))
	for i, f := range r.frags {
		out[i] = f.ID
	}
	return out
}

// Index returns a fragment's position, and whether it is declared at all. An
// event naming an undeclared fragment is refused rather than dispatched.
func (r *Registry) Index(id string) (int, bool) {
	i, ok := r.index[id]
	return i, ok
}

// AdmitsID is the immutable ingress check. A dynamic child passes only its
// declared namespace here; the actor checks exact committed membership before
// dispatch, after any in-progress send and membership publication finish.
func (r *Registry) AdmitsID(id string) bool {
	if _, known := r.index[id]; known {
		return true
	}
	for _, parent := range r.frags {
		if parent.Children != nil && id != parent.ID+":" && strings.HasPrefix(id, parent.ID+":") {
			return true
		}
	}
	return false
}
