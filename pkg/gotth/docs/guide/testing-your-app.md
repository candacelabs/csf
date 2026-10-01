# Testing your app

At the end of this page you can hold your reducer to the determinism the library
depends on, catch a fragment that declared itself unchanged while its markup
moved, and unit-test the `Config` hooks that take a `Session`.

Compiled source: [`_samples/apptest`](_samples/apptest).

---

## The package

Test helpers live in `live/livetest`, a **separate package** from `live` so that
production binaries do not link `testing` — and with it `flag`, `regexp`,
`runtime/pprof` and `runtime/trace` — merely because they import a UI library.
The precedent is `net/http/httptest`.

**What is implemented today:**

| Symbol | Status |
|---|---|
| `livetest.ReplayN` | shipped |
| `livetest.AssertDirtyComplete` | shipped |
| `livetest.NewSession` | shipped |
| `livetest.NewClient`, `livetest.Client` and its methods | shipped |
| `livetest.Audit`, `livetest.Report` | **not yet implemented.** Ledgered; no consumer has written their shape yet. |

`Client` dials a real `http.Handler`, speaks the real protocol, and hands back
decoded frames as plain values — so a frame-level assertion is a few lines
rather than the ~400-line `protowire` decoder the examples used to carry. See
[`docs/api-surface.md` §6](../api-surface.md) for the full surface.

---

## Ginkgo: `GinkgoTB()` is the `testing.TB`

Every helper on this page takes a `testing.TB`. Ginkgo's **`GinkgoT()` is not
one** — `testing.TB` carries an unexported method that nothing outside package
`testing` can implement, so `GinkgoT()` returns Ginkgo's own interface instead.
Reaching for it here is the mistake this section exists to head off.

**`GinkgoTB()` is the one you want.** Ginkgo ships it for exactly this case:

```text
ginkgo.GinkgoTB(optionalOffset ...int) *ginkgo.GinkgoTBWrapper
```

Its godoc calls it *"a wrapper that exactly matches the `testing.TB` interface …
intended to be used as a drop-in replacement with third party libraries that
accept `testing.TB`"*, and it has been in Ginkgo since well before `v2.32.0`,
the version this project pins. In a suite that dot-imports Ginkgo — which
`RegisterFailHandler(Fail)` already assumes — it is in scope with no import to
add, and every call site is one expression:

<!-- sample: apptest/app_test.go -->
```go
	It("is deterministic", func() {
		livetest.ReplayN(GinkgoTB(), apptest.Reduce, apptest.State{}, log(), 25)
	})
```

**`livetest` does not import Ginkgo, and this is why it does not have to.**
`GinkgoTB()` is called from *your* test file, so the wrapper never crosses the
package boundary as a dependency. That matters to somebody: a module that
imports `live/livetest` and nothing else of Ginkgo's would pay **+17 modules in
the build list and +3,484,016 bytes** for the import, because Ginkgo's root
package reaches `golang.org/x/tools/go/packages`, `html/template`, `go-logr` and
`Masterminds/semver`. A plain `go test` project pays none of it and passes its
own `t`.

**Failures are fatal, whichever `testing.TB` you pass.** `GinkgoTB()` routes
`Error`/`Errorf` through `Fail`, and these helpers have no record-and-continue
mode to offer: a determinism harness that logged a mismatch and returned would
report success for a reducer that is not deterministic.

`GinkgoTB()` implements the whole interface — `Cleanup`, `Helper`, `Name`,
`TempDir` and the rest — so a helper that grows a `tb.Cleanup` call keeps
working. `livetest`'s own suite asserts that, so it is a property with a spec
behind it rather than an expectation.

With a plain `go test` suite, pass `t` and skip all of this.

---

## `ReplayN`: the determinism harness

```text
livetest.ReplayN[S](tb testing.TB, reduce live.Reducer[S], initial S, log []live.Event, n int)
```

It replays the log `n` times and fails unless **the resulting state and the
emitted effects are identical on every run**. `n` must be at least 2, and the
log must not be empty — replaying nothing proves nothing.

<!-- sample: apptest/app_test.go -->
```go
	It("is deterministic", func() {
		livetest.ReplayN(GinkgoTB(), apptest.Reduce, apptest.State{}, log(), 25)
	})
```

Three things fail it, and they are the whole of what usually goes wrong in a
pure function of two values: **a clock, a random source, and the iteration order
of a map.** Nothing else can differ between runs.

The effect comparison is deep, which is why effects must be plain values and
never closures over live handles: a closure compares by identity and tells you
nothing.

Build the log with `live.NewFields`, which is the only way to construct the form
values an event carries:

<!-- sample: apptest/app_test.go -->
```go
func log() []live.Event {
	return []live.Event{
		{Name: apptest.EventInc, FragmentID: apptest.FragmentValue},
		{Name: apptest.EventInc, FragmentID: apptest.FragmentValue},
		{Name: apptest.EventReset, FragmentID: apptest.FragmentValue, Fields: live.NewFields(map[string]string{"by": "admin"})},
		{Name: apptest.EventInc, FragmentID: apptest.FragmentValue},
	}
}
```

Leave `Event.ID` and `Event.At` zero unless the reducer reads them. If it reads
`At` — for a relative timestamp, say — set it explicitly in the log, because
that is the value replay has to hold constant.

---

## `AssertDirtyComplete`: the rendering half

```text
livetest.AssertDirtyComplete[S](tb testing.TB, cfg live.Config[S], initial S, log []live.Event)
```

It replays the log against the whole `Config` and fails if any fragment declared
itself unchanged while its rendered bytes moved.

<!-- sample: apptest/app_test.go -->
```go
	It("declares every fragment that moved", func() {
		livetest.AssertDirtyComplete(GinkgoTB(),
			apptest.Config([]string{"http://127.0.0.1:8080"}), apptest.State{}, log())
	})
```

Under-declaring is the one rendering mistake that produces a stale region in
production and **nothing at all in development**, because some later transition
usually re-renders the fragment before anybody looks.

**It does not catch over-declaring, and says so.** Over-declaring costs a
suppressed render and never a wrong pixel, so this helper treats it as free. It
is free in correctness and not free in CPU: the signal is
`gotthlive_patches_suppressed_total` —
[fragments-and-dirty-tracking.md](fragments-and-dirty-tracking.md).

Point the log at the events that move each fragment. A log that never triggers a
fragment's `Dirty` proves nothing about it.

---

## `NewSession`: testing the hooks that take a `Session`

```text
livetest.NewSession[I live.IIdentity](tb testing.TB, id live.ID, identity I) live.Session[I]
```

`Init`, `Authorize`, `Teardown` and `Execute` all take a `live.Session`, whose
fields are unexported because identity is bound at the handshake and nothing
downstream may mint one. The consequence for a test is sharper than it looks:
**`live.Session{}` compiles and is useless** — an empty composite literal names
no field, so its `ID()` is all-zero and its `Identity()` is nil, and identity is
the reason those hooks take a `Session` at all.

<!-- sample: apptest/app_test.go -->
```go
	newSession := func(b byte, identity apptest.User) live.Session[apptest.User] {
		return livetest.NewSession(GinkgoTB(), live.ID{b}, identity)
	}

	It("denies a reset from a guest without closing the connection", func() {
		err := apptest.Authorize(context.Background(),
			newSession(1, apptest.Guest), live.Event{Name: apptest.EventReset})

		var deny *live.DenyError
		Expect(errors.As(err, &deny)).To(BeTrue())
		Expect(deny.Reason).To(Equal("only an admin may reset"))
	})
```

**Both values are the caller's, deliberately.** Deriving the identifier from the
identity would be wrong: one subject holds many concurrent sessions — that is
what `Limits.MaxSessionsPerIdentity` (default **20**) is about — so two tabs
belonging to one user are two identifiers and one identity.

A nil identity is a fatal test failure rather than a returned value: reproducing
the trap the zero `Session` already sets is not scaffolding.

Without this helper, every application invents the same workaround — a second
unexported method taking the values the hook would have read, with the exported
hook reduced to an adapter over it. If you find yourself writing that split,
this is the symbol you were missing.

---

## Testing the configuration itself

`live.New` is a cheap unit test, and it is the one that catches a security field
you meant to fill in:

<!-- sample: apptest/app_test.go -->
```go
	It("is rejected at startup when a security hook is missing", func() {
		cfg := apptest.Config([]string{"http://127.0.0.1:8080"})
		cfg.CSRF = nil

		_, err := live.New(cfg)

		var cfgErr *live.ConfigError
		Expect(errors.As(err, &cfgErr)).To(BeTrue())
		Expect(cfgErr.Field).To(Equal("CSRF"))
		Expect(cfgErr.Detail).To(ContainSubstring("live.NoCSRFCheck"))
	})
```

There are no error sentinels to match on: one `*live.ConfigError` with `Field`
and `Detail`, because the text is more actionable than an `errors.Is` target.

---

## A test plan that covers the contracts

| Assert | With |
|---|---|
| the reducer is pure | `ReplayN`, at least twice, over a log that exercises every event name |
| every fragment that moved said so | `AssertDirtyComplete`, over a log that moves each one |
| the authorization rules | call `Config.Authorize` directly with `NewSession` |
| the mount registers and the teardown releases | call `Init` and `Teardown` directly with `NewSession`; assert on your own registry |
| a failed effect is classified correctly | call your `Execute` and assert `live.IsRetryable(err)` — `Expect(live.IsRetryable(err)).To(BeTrue())` composes with `Eventually` and `DescribeTable` where an assertion helper would not |
| the configuration is valid | `live.New`, and a negative case per required field |
| the rendered attributes | render a fragment to a string and assert on `data-gotth-region` / `data-gotth-on` |

Run with `-race`. The library's concurrency contract is that one goroutine owns
each session's state; anything you share **between** sessions is yours to get
right, and the race detector is what tells you that you did not.
