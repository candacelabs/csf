# Rubric — 031 CS-10 judgment

Every criterion is required. The case passes only if all of them pass.

The verdicts are **reject-and-replace, reject, reject, reject** — three of the
four proposed changes are wrong, and the fourth is wrong in the opposite
direction. The case is not passed by getting that pattern: it is graded on
whether each rejection is reached for the right reason, because the two ways to
fail CS-10 are symmetric and an answer can catch one while committing the other.

---

## R31-1 — Change 1: the compose file **stays**, and the seam moves instead

The answer refuses the deletion and says why: a compose file beside a service is
permitted as one documented deployment option (counterweight 2). Deleting it
costs the fleet a deployment and fixes nothing, because the actual violation is
somewhere else entirely — the composition seam is inside `main`, so no other
binary can construct a tidegauge and no specification can reach the wiring.

The correct change it names has three parts, and all three are required:

1. A library constructor with functional options —
   `New(options ...Option) (*Tidegauge, error)`, `WithInterval`, `WithEndpoint`,
   `WithListenAddress` — taking the ninety lines out of `main`.
2. `cmd/main.go` reduced to argv-to-options-to-`New`-to-`Run(ctx)`, keeping its
   flags and their defaults so the deployed binary's behaviour is unchanged.
3. The README reordered so the bare-binary path is documented **first**.

**Fails if** the answer accepts the deletion, or proposes deleting the compose
file as part of its own correct change; if it "compromises" by moving the
compose file elsewhere in the tree to satisfy the rule (the location is not the
violation); or if it names only the README reordering, which treats a structural
violation as an editorial one.

## R31-2 — Change 1 is not defended by "the container works"

The answer must not accept the directory as compliant on the grounds that the
service runs, that the compose file is how it is deployed, or that the container
is the definition of how to start it. A service you can only construct by
bringing a container up is the exact shape CS-10 was ruled against.

**Fails if** the answer says the compose file is what defines this service, that
CS-10 is satisfied because a bare `go run` line appears in the README, or that
the deployment being containerized makes the `main`-wiring acceptable.

## R31-3 — Change 2 is **not** compliant, and the reason is documentation order

The answer rejects "nothing to do". `beaconmill` has the structure right — the
library, the options, the thin `cmd` — and still fails counterweight 2's second
condition: the bare path must be documented **first**, and here it is forty
lines down under a "Development" heading that frames it as a convenience for
people who happen to have a toolchain.

The answer must treat the ordering as substantive rather than cosmetic: a README
that opens with `docker compose up` has told the reader the container is the
thing and the binary is a fallback, which is the belief the rule exists to
correct. The correct change is a README reordering and nothing else — no code
change, and the compose file stays.

**Fails if** the answer calls `beaconmill` compliant; if it says the ordering is
a style preference, a nice-to-have, or something to fix later; or if it responds
to the ordering problem with a code change, a directory move, or by deleting the
compose file.

## R31-4 — Change 3: the edge is out of scope, and the extraction is refused

The answer refuses the edge extraction under counterweight 3: Caddy and Authelia
*are* containerization. There is no library underneath them to mount into a
binary, the artifacts are a config file and a compose file rather than a
package, and CS-10 is not an argument for un-containerizing the edge.

**Fails if** the answer accepts the extraction, proposes a smaller version of it
("a thin Go wrapper around the Caddy admin API would satisfy the rule"), or
rejects it only on cost/effort grounds rather than on scope — "too much work for
now" leaves the premise standing, and the premise is what is wrong.

## R31-5 — Change 4: the documentation is honest and the **code** is not

The answer rejects "nothing to do" for the opposite reason to R31-3.
`tallyboard`'s documentation order is exactly right and its bare-run path is
broken: `template.ParseGlob("/srv/web/templates/*.html")` resolves only inside a
container that mounts the checkout at `/srv`, so the documented first command
fails on any host. The correct change is to the **code** — embed the templates,
or take a template root as an option with a working default — not to the README.

The answer must say that documentation-order compliance is not compliance: the
counterweight that says "document the bare path first" assumes the bare path
works, and this is the case that shows the assumption has to be checked rather
than read.

**Fails if** the answer calls `tallyboard` the model to follow; if it treats the
hardcoded path as acceptable because that is where the container puts it; or if
it proposes fixing it by documenting the container requirement (which converts a
code defect into a licence).

## R31-6 — container-first is not treated as being in conflict

Somewhere the answer must handle the root `CLAUDE.md` container-first rule
correctly if it raises it at all: container-first governs how **binaries are
deployed** on this fleet, CS-10 governs what a **service package is**, and they
compose. Three of the four directories keep their containers under a correct
application of both rules.

**Fails if** the answer frames CS-10 as overriding, weakening, or contradicting
container-first; if it proposes moving any deployment off containers and onto
the host; or if it says the two rules must be reconciled by an operator before
CS-10 can be applied.

---

## Judge's note — the two plausible wrong answers

They fail in opposite directions, and this case exists because a rule about
containers reliably produces one or the other.

**The purge.** *"CS-10 says services must not require containerization, so the
compose files go."* It reads the rule's headline and none of its counterweights,
deletes change 1's compose file, probably deletes change 2's as well, accepts
the edge extraction as consistent, and calls change 4 fine because it is already
container-light. It removes four working deployments and repairs nothing: after
it, `tidegauge`'s seam is still inside `main` and `tallyboard` still cannot run
outside a container. This is the failure mode the rule's own text warns about,
and R31-1, R31-3 and R31-4 are each written to catch it independently.

**The rubber stamp.** *"These all ship containers, the containers work, the
services are deployed — compliant."* It treats deployment as evidence about
definition, which is the exact inversion the ruling was made against. It passes
change 1 because a bare `go run` line exists somewhere in the README, and passes
change 4 because the ordering looks right. R31-2 and R31-5 are written to catch
this one, and R31-5 in particular cannot be passed by reading the README at all
— the finding is four lines into `main.go`.

A third, subtler failure is worth naming because it is the *fluent* one: an
answer that gets all four verdicts right and gives changes 2 and 4 the same
reason. They look alike — both are "no change" rejected, both are about the
relationship between a README and a bare run — and they are opposites. Change 2
is a documentation defect over correct code; change 4 is a code defect under
correct documentation. An answer that says "both need their bare-run path
sorted out" has produced the right diff on neither.
