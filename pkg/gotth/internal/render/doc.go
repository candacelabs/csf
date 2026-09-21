// Package render turns state into whole HTML fragments.
//
// A patch carries the complete rendered markup of each changed fragment.
// There is no server-side diff of consecutive renders and no compile-time
// decomposition of templates into static and dynamic parts: the diff that
// matters already happens on the client, against the live DOM, and a
// server-side copy of what the server believes the browser shows is a second
// source of truth that can drift. The developer's lever on patch size is the
// granularity at which fragments are declared, which is legible in the
// template rather than dependent on whether a template happened to introduce
// a local variable.
//
// # Identity and dirty tracking
//
// Fragments are registered with stable identifiers; a duplicate registration
// is an error naming both call sites, never a silent last-write-wins. A
// transition may declare which fragments it touched. Over-declaring is safe,
// because a render whose bytes are unchanged is dropped by the suppression
// below; under-declaring is a correctness bug, and livetest.AssertDirtyComplete
// is what catches it before it reaches production.
//
// # Suppression
//
// This package retains a 64-bit hash of each fragment's last emitted bytes —
// the hash, not the bytes, because retaining previous renders would cost more
// per session than the whole memory budget allows. A re-render producing the
// same hash emits nothing, but still advances the transition counter, so the
// provenance record shows that the transition happened and produced no patch.
//
// # Determinism
//
// The same state must render byte-identical HTML, across runs and across
// processes. The known hazard is ranging over a Go map in a template: range a
// sorted slice instead. A repeated-render byte-equality test enforces it.
//
// # Status
//
// Implemented: the registry, per-session dirty tracking, identical-render
// suppression, and panic containment at both the render and change-declaration
// sites.
package render
