# Rubric — 027 factory judgment

Every criterion is required. The case passes only if all of them pass.

R27-6 was added 2026-09-02, when the operator read the clock the rule's own
method-position exemption had been written about.

---

## R27-1 — `buildDiscoverer` stops being an interface-returning factory, and the selection moves into the consumer

The answer relocates the `switch` to where the interface variable is declared:
`var discoverer warden.IPeerDiscoverer` in the wiring, with the arms assigning
`discovery.NewTailscale(...)` and `discovery.NewFile(...)` — each of which
already returns its own concrete type.

**Fails if** the answer keeps a function whose declared result is
`warden.IPeerDiscoverer`, however it is renamed or reorganized; or if it
"satisfies" CS-8 by having the factory return `any`, a generic type parameter
bound at one call site, or a struct wrapping the interface.

## R27-2 — the typed-nil trap is named, and it is why the static arm assigns nothing

The answer states that a concrete nil pointer stored in an interface is not a
nil interface — `var p *discovery.Tailscale = nil; var d warden.IPeerDiscoverer
= p; d != nil` is **true** — and concludes that the static arm must assign
*nothing at all*, leaving the variable's untyped nil intact.

**Fails if** the trap is not mentioned; if the answer proposes a helper that
returns a concrete pointer and `nil` on the static arm (which is the bug, one
indirection later); or if it "preserves" the static case by assigning
`discovery.NewStatic(...)`, a zero value, or a no-op implementation, all of
which change the fleet's membership semantics.

**This is the criterion the corpus's own history cannot be pattern-matched
into**, and it is deliberately the load-bearing one. See the judge's note.

## R27-3 — the static semantics are stated, not merely preserved by accident

The answer says what the nil selects — the election manager's static
membership: the config peer list is authoritative and any persisted roster is
ignored — and therefore why a non-nil discoverer there would silently convert a
static fleet to dynamic membership. A change that keeps the behaviour without
knowing what the behaviour is has not been reviewed.

**Fails if** the answer treats the `nil` return as an oversight, a missing
implementation, or something to be "tidied up".

## R27-4 — the naive narrowing is rejected explicitly

`func buildDiscoverer(cfg config.Config) *discovery.Tailscale` is the reading
CS-8's headline invites and it is wrong twice: it cannot express the file or
static modes at all, and on the static arm its `return nil` is precisely the
typed nil R27-2 is about.

**Fails if** the answer proposes it. An answer whose whole content is "return
`*discovery.Tailscale` instead" scores **zero** — not partial credit — because
it is a compile-time regression and a behaviour change presented as a style fix.

## R27-5 — `Anonymous` is left alone, and for the right reason

`Anonymous`'s result type is not its own: it exists to be assigned to
`Config.Authenticate func(request *http.Request) (IIdentity, error)`, and a
function that narrowed its result would stop fitting the field. The interface
there is already exactly where CS-8 wants one — a declaration at the consuming
seam — and the hook's own type is what fixes the signature.

**Fails if** the answer changes `Anonymous`; if it proposes exporting
`anonymous` and returning it concretely (Go has no covariance here, so every
`Authenticate: live.Anonymous` in the wild stops compiling); or if it leaves
`Anonymous` alone while giving a reason that would apply equally to
`buildDiscoverer` — "it only has one implementation", "it is small", "it is
idiomatic" — since that is the right verdict reached by a wrong rule.

## R27-6 — the clock's interfaces are deleted, and the defending comment is the evidence

The answer does **not** accept the comment's argument. It observes that
`ITimer` declares a channel accessor and two function values — `C()`,
`Stop() bool`, `Reset(d time.Duration) bool` — that nothing dispatches on and
nothing could, since a timer holds no state of its own, and concludes that
`ITimer` and `ITicker` are **data, not abstractions**. The fix it proposes is
a concrete struct:

```go
type Timer struct {
	C     <-chan time.Time
	Stop  func() bool
	Reset func(d time.Duration) bool
}
```

with `IClock.NewTimer` declared to return it, `RealClock` filling it from
`time.Timer`'s own method values, the simulated clock filling it from its
channel and its closures, and `realTimer`/`fakeTimer` deleted. `IClock`
survives: `Now` and `NewTimer` genuinely differ in behaviour between a real
clock and a simulated one.

**Fails if** the answer accepts the method-position exemption and leaves the
clock alone, however well it restates the exemption — the exemption is real and
it is answering the wrong question, because it never asks whether `ITimer`
should exist. **Fails if** it proposes keeping `ITimer` and "documenting it
better". **Fails if** it converts `IClock` itself to a struct: a real clock and
a simulated one differ in what they *do*, which is what an interface is for.

**Credit, not required:** noticing that the defending comment is itself the
signal. A comment that argues a rule does not apply is addressed to a reviewer
rather than to a reader, and it is the artifact of a decision that felt like it
needed permission. An answer that says so has found the general form.

---

---

## Judge's note — the plausible wrong answer

The fluent failure here is **a correct relocation with no mention of nil.**

By this point the judged corpus has taught a strategy: cases `021`, `023` and
`025` all reward a split verdict justified by substance, and `025` in particular
rewards proposing a *third* option — relocate rather than accept either
alternative. A model that has read those and nothing else produces exactly the
right shape on `buildDiscoverer`: "move the switch into the consumer, declare
the interface there". It will very often then write the static arm as
`discoverer = nil`, or restructure into a helper returning a concrete type with
a `nil` default, and say nothing about why either is or is not safe.

That answer *looks* complete and passes R27-1, R27-3 and R27-5. It fails R27-2,
which is the point: the relocation is only behaviour-preserving because the
unassigned variable's nil is untyped, and an answer that does not know that got
the right diff by pattern-matching. CS-8 exists because of the interface's nil,
not because of the interface's aesthetics.

The second failure to watch for is the **blanket** one, in the other direction:
"all three are CS-8 violations, fix them all." It scores R27-1 and fails R27-5,
and it is what a reader of the rule's headline sentence produces without its
exemptions.

The third is specific to `ITimer` and is the most fluent of the three: **a
model that has read the rule's exemptions correctly restates the method-position
one and stops.** "A method's result type is fixed by the interface it satisfies,
so `RealClock.NewTimer` cannot narrow; the decision lives in `IClock`" is true,
is exactly what the rule says, and is what the source comment already claims.
It fails R27-6 because the exemption is a rule about *where a decision lives*
and the question here is *whether the thing being decided about should exist*.
That is why the third snippet ships with its defending comment intact rather
than stripped: the case is not "spot the interface", it is "notice that
something is arguing".
