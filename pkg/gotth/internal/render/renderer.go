package render

import (
	"bytes"
	"context"
	"errors"
	"hash/maphash"
	"runtime/debug"
)

// Failure reports a panic recovered while deciding or producing one
// fragment's markup.
//
// A render that panics leaves that fragment stale and lets every other
// fragment in the same transition patch normally. It does not trigger a
// resync: a render that panics will panic again, and a resync loop is worse
// than one stale region.
type Failure struct {
	// FragmentID is the region whose application code panicked, and therefore
	// the one region this transition leaves stale.
	FragmentID string

	// Site is "render" or "dirty". Both are application code on the render
	// path and both are counted against the render panic budget; the
	// distinction is for the log record, where it saves a bisect.
	Site string

	// Value is whatever was passed to panic, unconverted. It reaches the
	// browser only in dev: in production the Error frame carries a fixed
	// generic message, because a panic value is server internals.
	Value any

	// Stack is the stack captured at recovery, for the log record. It is never
	// sent to a client in either mode.
	Stack []byte
}

// Result is what one render pass produced.
//
// A pass is not a commit. The hashes it computed are held here until the
// caller says the markup reached the transport, because a hash installed
// before the send is a claim about the client's DOM that the send is still
// free to falsify: see Commit and Discard.
type Result struct {
	// Updates carries the fragments whose markup changed.
	Updates []Update
	// Suppressed names the fragments that re-rendered to the bytes they last
	// emitted. A suppressed render is not a no-op transition: the transition
	// still happened and still gets a provenance record.
	Suppressed []string
	// Failed carries the recovered panics.
	Failed []Failure

	// updated and hashes are Updates' registry indices and their new hashes,
	// in the same order. They are unexported because installing one is the
	// renderer's business and the two methods below are the only way to ask
	// for it.
	updated     []int
	hashes      []uint64
	collections []collectionResult
}

// FragmentObserver is called around one fragment's render. It returns the
// context that fragment renders under and a function that closes the
// observation, told whether the markup was suppressed as identical and
// whether the render failed.
//
// It is a function value rather than an interface for the reason WriteFunc is:
// there is one implementation, and a one-implementation interface buys nothing
// (checklist §1.4). It is also what keeps this package out of the
// observability package's import graph — an architecture test forbids the
// render path from reaching a clock, a logger, or the outside world, and
// internal/obs imports log/slog and time, so the actor passes a closure in
// rather than the renderer reaching a tracer out.
//
// A nil observer is the disabled configuration and costs one branch per
// fragment.
type FragmentObserver func(ctx context.Context, fragmentID string) (context.Context, func(suppressed, failed bool))

// Renderer is one session's view of a registry: the dirty set and the hash of
// each fragment's last emitted bytes.
//
// It holds the hash and not the bytes. Retaining the previous render per
// fragment per session would cost more than the whole per-connection memory
// budget, to save bytes on a link the client is already diffing against its
// own DOM.
type Renderer struct {
	reg         *Registry
	hashes      []uint64
	dirty       bitset
	buf         bytes.Buffer
	collections map[int]collectionCache
	childDirty  map[int]map[string]bool
	parentDirty bitset
	known       map[string]struct{}

	// w is the handle application render code receives, allocated once per
	// session rather than once per fragment: it is a pointer, so passing it as
	// an io.Writer costs no allocation, which handing out a fresh wrapper per
	// fragment would (sixty-four per pass at the registry's own bound).
	w *fragmentWriter

	// rendering is true only while callRender is on the stack. It is what makes
	// a retained writer refuse rather than corrupt: see fragmentWriter.
	rendering bool

	// observe is set once per session, when the session is built, and is nil
	// whenever tracing is off.
	observe FragmentObserver
}

// Observe installs the per-fragment observer. It is called once, when the
// session is constructed, and never from the render path.
func (v *Renderer) Observe(fn FragmentObserver) { v.observe = fn }

// NewRenderer returns a per-session renderer over r. Every fragment starts
// dirty, because a session that has emitted nothing has nothing to suppress
// against.
func (r *Registry) NewRenderer() *Renderer {
	v := &Renderer{
		reg:         r,
		hashes:      make([]uint64, len(r.frags)),
		dirty:       newBitset(len(r.frags)),
		parentDirty: newBitset(len(r.frags)),
		collections: make(map[int]collectionCache),
		childDirty:  make(map[int]map[string]bool),
	}
	v.w = &fragmentWriter{v: v}
	v.MarkAll()
	v.publishIDs()
	return v
}

// fragmentWriter is the io.Writer one fragment renders through.
//
// It exists so that the renderer's buffer is not the thing handed to
// application code. v.buf is per-session state reused for EVERY fragment of
// every pass, and passing &v.buf out had two holes (U-6): a fragment could
// type-assert it to *bytes.Buffer and keep .Bytes(), which is a live view of
// storage the next fragment's Reset overwrites; and a fragment could retain the
// io.Writer and write to it later, landing bytes in some other fragment's
// markup or in a pass that has already been hashed.
//
// This closes both. The buffer is unreachable — a type assertion to
// *bytes.Buffer fails — and a write outside the call it was handed to is
// refused with an error, which callRender already turns into that fragment's
// own render failure. The check is a bool on the renderer rather than a
// generation captured per call, because the renderer is per-session and
// single-goroutine, and a fresh wrapper per fragment would be an allocation per
// fragment per pass for a hazard this bool already covers.
type fragmentWriter struct{ v *Renderer }

// errWriterEscaped is what a retained writer is told. It names the rule rather
// than the symptom, because the caller holding it is application code and the
// fix is theirs.
var errWriterEscaped = errors.New(
	"gotth-live: a fragment wrote to its io.Writer after Render returned: the writer is valid " +
		"only for the duration of the call and its storage is reused by every other fragment, " +
		"so build the markup during the call rather than retaining the writer")

func (w *fragmentWriter) Write(p []byte) (int, error) {
	if !w.v.rendering {
		return 0, errWriterEscaped
	}
	return w.v.buf.Write(p)
}

// MarkAll marks every fragment dirty.
func (v *Renderer) MarkAll() {
	v.dirty.setAll(len(v.reg.frags))
	v.parentDirty.setAll(len(v.reg.frags))
}

// MarkID marks one fragment dirty and reports whether it is declared.
func (v *Renderer) MarkID(id string) bool {
	if index, known := v.reg.index[id]; known {
		v.dirty.set(index)
		v.parentDirty.set(index)
		return true
	}
	for index, collection := range v.collections {
		if _, known := collection.hashes[id]; known {
			if v.childDirty[index] == nil {
				v.childDirty[index] = make(map[string]bool)
			}
			v.childDirty[index][id] = true
			v.dirty.set(index)
			return true
		}
	}
	return false
}

// Mark consults each fragment's change declaration for a transition and marks
// the fragments it names.
//
// A fragment with no declaration is always marked: over-declaring is safe
// because an identical render is suppressed, and under-declaring is a
// correctness bug the determinism helpers catch before it reaches production.
// A declaration that panics is treated as "dirty" and reported, because the
// safe reading of "I do not know whether this changed" is that it did.
func (v *Renderer) Mark(prev, next any) []Failure {
	var failed []Failure
	for i, f := range v.reg.frags {
		if f.Children != nil {
			failed = append(failed, v.markCollection(i, f, prev, next)...)
			continue
		}
		if f.Dirty == nil {
			v.dirty.set(i)
			continue
		}
		changed, fail := callDirty(f, prev, next)
		if fail != nil {
			failed = append(failed, *fail)
			v.dirty.set(i)
			continue
		}
		if changed {
			v.dirty.set(i)
		}
	}
	return failed
}

func callDirty(f Fragment, prev, next any) (changed bool, failure *Failure) {
	defer func() {
		if r := recover(); r != nil {
			changed, failure = true, &Failure{
				FragmentID: f.ID,
				Site:       "dirty",
				Value:      r,
				Stack:      debug.Stack(),
			}
		}
	}()
	return f.Dirty(prev, next), nil
}

// Pending reports whether any fragment is waiting to be rendered. It is what
// lets the actor keep reducing while the outbound window is full and render
// once, from current state, when an acknowledgement re-opens it.
func (v *Renderer) Pending() bool { return v.dirty.any() }

// Commit installs the hashes a pass computed, and is the ONLY thing that may
// write v.hashes.
//
// The caller calls it after — never before — the pass's markup reached the
// transport. hashes[i] means "the bytes the client holds for fragment i", and
// suppression is only sound while that is true: a hash installed by the render
// pass itself is installed before the send that can still fail, and a send
// that fails survivably (protocol.InvalidFrameError, which deliberately keeps
// the session alive) then leaves the renderer believing the client holds
// markup that was never written to the socket. Every later render of that
// fragment producing the same bytes is suppressed, so the region is stale for
// the life of the connection with nothing saying so.
func (v *Renderer) Commit(res Result) {
	for k, i := range res.updated {
		v.hashes[i] = res.hashes[k]
	}
	for _, collection := range res.collections {
		v.collections[collection.index] = collection.cache
	}
	if len(res.collections) != 0 {
		v.publishIDs()
	}
}

// Discard reverses a pass whose markup never reached the transport: the
// fragments it updated go back into the dirty set so the next pass renders
// them again, and their hashes are never installed, so the retry cannot be
// suppressed as identical.
//
// The fragments that panicked are deliberately NOT re-marked. A render that
// panics will panic again, and re-marking would convert one stale region into
// a session that closes on its panic budget — the same reasoning render()
// applies when it clears their bit in the first place.
func (v *Renderer) Discard(res Result) {
	for _, i := range res.updated {
		v.dirty.set(i)
	}
	for _, collection := range res.collections {
		v.dirty.set(collection.index)
		if collection.whole {
			v.parentDirty.set(collection.index)
		} else {
			if v.childDirty[collection.index] == nil {
				v.childDirty[collection.index] = make(map[string]bool)
			}
			for _, id := range collection.dirty {
				v.childDirty[collection.index][id] = true
			}
		}
	}
}

// Render produces the markup for every dirty fragment, dropping the ones whose
// bytes are unchanged since they were last emitted.
//
// The dirty set is cleared for every fragment this pass considered, including
// the suppressed ones, which are up to date by definition, and including the
// ones that panicked: retrying a render that panics on every transition
// converts one stale region into a session that closes on its panic budget.
func (v *Renderer) Render(ctx context.Context, state any) Result {
	return v.render(ctx, state, false)
}

// RenderAll produces the markup for every fragment regardless of the dirty set
// and regardless of suppression, which is what a snapshot needs: the client
// has nothing to morph against, so an unchanged fragment must still be sent.
func (v *Renderer) RenderAll(ctx context.Context, state any) Result {
	return v.render(ctx, state, true)
}

func (v *Renderer) render(ctx context.Context, state any, all bool) Result {
	var res Result
	for i, f := range v.reg.frags {
		if !all && !v.dirty.has(i) {
			continue
		}
		v.dirty.clear(i)
		if f.Children != nil {
			v.renderCollection(ctx, state, i, f, all, &res)
			continue
		}

		// One observation per fragment considered, which is the unit FR-36's
		// gotthlive.render.fragment names. Suppressed and failed fragments are
		// observed too: a fragment that rendered to the same bytes still cost
		// the render, and a fragment that panicked is the one an operator is
		// looking for.
		//
		// Nil-checked rather than routed through a no-op function value:
		// instrumentation §4.2 makes "nothing is computed when disabled" a
		// requirement and names the no-op indirection as the way to fail it.
		fctx := ctx
		var done func(suppressed, failed bool)
		if v.observe != nil {
			fctx, done = v.observe(ctx, f.ID)
		}

		// v.buf is shared across every fragment of every pass, which is why
		// what goes out is v.w and not this. See fragmentWriter.
		v.buf.Reset()
		if fail := v.callRender(f, fctx, state); fail != nil {
			res.Failed = append(res.Failed, *fail)
			if done != nil {
				done(false, true)
			}
			continue
		}

		html := v.buf.String()
		sum := maphash.Bytes(v.reg.seed, v.buf.Bytes())
		if !all && sum == v.hashes[i] {
			res.Suppressed = append(res.Suppressed, f.ID)
			if done != nil {
				done(true, false)
			}
			continue
		}
		// Recorded, not installed. Only Commit writes v.hashes, and it runs
		// after the caller has seen the write succeed.
		res.updated = append(res.updated, i)
		res.hashes = append(res.hashes, sum)
		res.Updates = append(res.Updates, Update{FragmentID: f.ID, Op: OpMorph, HTML: html})
		if done != nil {
			done(false, false)
		}
	}
	return res
}

func (v *Renderer) callRender(f Fragment, ctx context.Context, state any) (failure *Failure) {
	v.rendering = true
	defer func() {
		v.rendering = false
		if r := recover(); r != nil {
			failure = &Failure{
				FragmentID: f.ID,
				Site:       "render",
				Value:      r,
				Stack:      debug.Stack(),
			}
		}
	}()
	if err := f.Render(ctx, state, v.w); err != nil {
		// A render that returns an error is the same failure as one that
		// panics, and is reported through one path so the caller has one
		// thing to handle.
		return &Failure{FragmentID: f.ID, Site: "render", Value: err}
	}
	return nil
}

// bitset is the dirty set. It is words rather than a []bool because it is
// per-session state and this library's whole memory argument is made of
// decisions this size.
type bitset []uint64

func newBitset(n int) bitset { return make(bitset, (n+63)/64) }

func (b bitset) set(i int)   { b[i/64] |= 1 << uint(i%64) }
func (b bitset) clear(i int) { b[i/64] &^= 1 << uint(i%64) }
func (b bitset) has(i int) bool {
	return b[i/64]&(1<<uint(i%64)) != 0
}

func (b bitset) any() bool {
	for _, w := range b {
		if w != 0 {
			return true
		}
	}
	return false
}

func (b bitset) setAll(n int) {
	for i := range b {
		b[i] = ^uint64(0)
	}
	if rem := n % 64; rem != 0 {
		b[len(b)-1] = 1<<uint(rem) - 1
	}
}
