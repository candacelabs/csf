# Subagent brief snippet

Paste the block below **verbatim** into every subagent brief whose stage may
write, review, or generate Go or UI code. Do not paraphrase it, and do not
replace it with "follow the house style" or a pointer to `AGENTS.md` — a
subagent reads its brief, and cannot be assumed to have read anything else.

Everything the block asserts is sourced in `go-rules.md`; keep the two in step
if a rule changes.

---

```
HOUSE CODE STYLE (restated in full — do not depend on repo docs being read):
- CS-1 I-PREFIX. Every interface you declare or rename carries an I prefix:
  IStore, IReader, IIdentity. Repo-wide, including the published
  candacelabs/candace API. Unexported interfaces keep their visibility: iStore.
  Operator ruling of 2026-09-02; do not re-litigate it, do not exempt the
  public module. The repo-wide rename landed in slice P3. Every interface you
  write, rename or move is I-prefixed from the first commit, and CI blocks on
  it; the native report owns the current count.
- CS-2 NAMED PARAMETERS. Every parameter in every signature is named: function
  declarations, function types, AND interface methods. func(*http.Request)
  error is a violation; func(request *http.Request) error is correct. Return
  values are exempt — name them only when it genuinely improves readability.
- CS-3 CENTRALIZE PRIMITIVES. Centralize a stable, useful primitive at the
  lowest layer that owns its semantics (root AGENTS.md "Shared primitive
  reuse"; the OCaml house runner invokes pinned dupl for CS-3). Two counterweights: do not create
  catch-all util, common, or core packages; and "nothing qualifies" is a
  legitimate result. Keep unsettled or service-specific behavior with its owner
  until at least two callers need the same semantics.
- CS-4 GENERATE BEFORE YOU WRITE. Before hand-writing a type, client, mock,
  build file, or config projection, check whether a generator already owns that
  output: pkg/liquidproto (contracts), the sqlc.yaml configs (database
  access), the proto generate.sh chains (bindings), the //go:generate mockgen
  sites (test doubles), Gazelle (Bazel metadata). Generated files are
  projections, not owners — never hand-edit generated output, and never
  unit-test checked-in generated code directly; test the generator and the
  handwritten boundary that consumes it, and add a deterministic regeneration
  check when you add a generator. The native CS-4 check reports SQL-shaped
  literals, handwritten Mock* structs and .pb.go/.gen.go/_generated.go files
  missing a generated marker. These are ADVISORY review candidates; generator
  ownership and reproducibility remain with the owning component.
- CS-5 CSP OVER MUTEXES. Operator, 2026-09-02: "bias against mutexes in favor
  of csp". Prefer channels, one owning goroutine per datum, message passing. A
  lock is the honest primitive for a LEAF critical section, the ontology term
  `leaf`: while held it takes no other lock, does no channel op or io and calls
  nothing it does not control; it covers one datum with single-step operations
  and no invariant across two; nobody waits on it. Failing any one is a
  protocol. A leaf is in-process io; after slice X2 it uses the io/inproc leaf
  primitives and raw sync lives only in io/inproc and the runtime —
  keep it there, and do not convert it into channel ceremony. What CS-5 is
  about is a mutex guarding a state machine that several goroutines negotiate
  over; that lock is standing in for a protocol, so give the state one owner
  and send it messages. go test -race must be green either way: channels
  deadlock, drop on full buffers and leak goroutines. Third option, often the
  best: an immutable snapshot published by atomic.Pointer removes the lock
  without adding a protocol. The gate flags EVERY mutex declaration, leaf ones
  included — a CS-5 finding is a locator, not a verdict; answer it, never
  silence it.
- CS-6 COMPOSABLE FUNCTION VALUES. Operator, 2026-09-02, reading a dispatch
  list of six sibling methods: "why couldn't this be functional options
  pattern". When a family of behaviors shares one signature — checks, rules,
  stages, handlers, migration steps — make each a named function value
  registered in DATA (a slice or map the code iterates), not another method on
  a mutable struct wired into a hardcoded call list. A registry can carry
  metadata (which error classes a check owns) and a test can assert
  completeness over it; a method set can never be enumerated by a spec.
  Constructor configuration is the same principle at construction time:
  functional options, New(x, WithY(...)), as pkg/cron/options.go
  already does. Prefer PURE function values — take inputs explicitly, return
  findings, let the registry loop do the appending — over methods that reach
  through a shared mutable receiver for input and output. Counterweights: a
  short fixed sequence with real ordering dependencies is honestly a method
  chain or plain statements, so do not force registry ceremony on three ordered
  steps that will never grow (ask "family or sequence?", not "how many calls");
  and CS-2 applies inside every function type you introduce —
  func(*Document) []Finding is wrong, func(document *Document) []Finding is
  right, and converting a method set multiplies the func types you must name.
  The gate reports one narrow shape (a method whose body is only 3+
  argument-less calls on its own receiver) and, like CS-5, is a locator rather
  than a verdict.
- CS-7 GENERICS AT THE BOUNDARY, ERASURE IN ONE PLACE. Operator, 2026-09-02,
  reading `type State any` in the widget SDK: "are you guys an idiot we
  literally have generics". A PUBLIC contract never asks its author to hold
  `any` where a type parameter can carry the type. Go has no existential types,
  so a registry of differently-parameterized values genuinely does erase
  somewhere — that analysis is correct and is not what the rule disputes. The
  defect is making the erasure the public contract instead of an internal
  detail. Erase EXACTLY ONCE, inside the library that owns the collection,
  behind a generic shell: `type IWidget[S any] interface { Reduce(state S,
  event live.Event) (S, []live.Effect) ... }` with
  `func Register[S any](registry *Registry, widget IWidget[S]) error` wrapping
  an unexported untyped adapter. The assertion lives at that one audited site;
  author-facing and generated per-instance code never see `any` and never
  assert. Counterweights: do not generify internals no author touches, and a
  type parameter that only ever binds one type is ceremony, not safety; code
  that genuinely operates on unknown shapes (encoding/json envelopes,
  reflection-driven plumbing, a third-party signature being mirrored) stays
  `any` and SAYS WHY IN A COMMENT — an `any` with a reason is a decision, an
  `any` without one is a default; and CS-2 applies inside every generic
  signature, so `Register[S any](*Registry, IWidget[S]) error` is a CS-2
  violation wearing CS-7 compliance. The gate reports exported interface
  methods and exported struct fields typed bare `any` in non-internal packages
  and, like CS-5 and CS-6, is a locator rather than a verdict: all 3 of its
  current findings are legitimate `any` that stays.
- CS-8 RETURN CONCRETE, ACCEPT INTERFACES. Operator, 2026-09-02, verbatim:
  "RETURN CONCRETE IMPLEMENTATIONS ONLY ACCEPT INTERFACES". A function or
  method never RETURNS an interface type. Interfaces live in exactly two
  places: parameter position (accepted — this half is permissive and never
  gated), and a variable declaration or struct field at the consuming seam.
  Returning one takes the concrete type away from a caller that often needs it
  and cannot get it back except by an assertion the compiler stops checking.
  Where runtime polymorphism selects among implementations — the config-selected
  factory — the SELECTION MOVES INTO THE CONSUMER: declare the interface
  variable where it is accepted and assign concrete constructors into it in the
  switch arms.
      var discoverer warden.IPeerDiscoverer
      switch cfg.Discovery.Mode {
      case config.DiscoveryModeTailscale:
          discoverer = discovery.NewTailscale(...)
      case config.DiscoveryModeFile:
          discoverer = discovery.NewFile(...)
      default: // static: assigns nothing; the variable stays untyped nil.
      }
  THE UNTYPED-NIL SEAM IS PRESERVED BY LEAVING THE VARIABLE NIL, never by
  returning nil through an interface-returning factory. A concrete nil pointer
  wrapped in an interface is NOT nil: `var p *Tailscale; var d IPeerDiscoverer =
  p; d != nil` is true, so an interface-returning factory whose "none" arm
  returns a typed nil silently sends every `if d != nil` down the wrong branch.
  In warden that flips a static fleet into dynamic membership semantics, with
  nothing failing loudly. The naive "fix" — narrowing the return to
  `*discovery.Tailscale` — is wrong twice over: it cannot express the other
  modes, and on the empty arm it IS the typed-nil bug. Move the selection; do
  not narrow the return type in place.
  One structural exemption, stated once: `error` — Go's own contract, and
  conveniently not I-prefixed, so the gate never sees it. Exempt by
  construction: a result carried by a type parameter the caller instantiated
  (`zeroInit[S]() (S, ...)`; and `LookupWidget[S]() (IWidget[S], bool)`, whose
  shell erased the concrete type on the way in by CS-7's design and has none
  left to hand back). The gate reads a RECEIVERLESS func declaration whose
  result is a bare, corpus-declared interface name: a method is out of scope
  because its result is fixed by the interface it satisfies, a sealed sum type
  (an exported interface closed by an unexported method, like protocol.IInbound)
  is a tagged union rather than an abstraction, and a function whose signature
  matches a declared func type is a hook implementation whose result is the
  hook's choice. COUNTERWEIGHT (orchestrator ruling, 2026-09-02): returning a
  THIRD-PARTY or upstream library's own contract type that happens to be an
  interface — templ.Component, http.Handler, a library's Msg — is a
  PASS-THROUGH, not an erasure choice, and is exempt in the same class as
  `error`. There was no concrete type available to return instead, so there is
  nothing to relocate. CS-8 reaches interface types THIS REPOSITORY owns and
  could have made concrete; the test is "could this function have returned a
  concrete type?". The gate already skips third-party types by construction
  (it reports a name only if the corpus declares an interface by it), so a
  finding on one means the name collides with one of ours — answer it in the
  review, never with a marker comment or an exclusion.
  AMENDMENT (operator, 2026-09-02, reading clock.go: "WHY DOES CLOCK.GO RETURN
  INTERFACES REEEEE"): every exemption above says "this position's type is not
  the declaration's to choose", and the METHOD exemption in particular assumes
  the returned interface deserves to exist. BEFORE recording ANY exemption,
  apply THE DATA-SHAPED TEST: is the returned abstraction data-shaped —
  channels, function values, plain fields, no behaviour of its own? Then it is
  NOT an abstraction. Make it a CONCRETE STRUCT and the exemption evaporates,
  because there is no interface left to exempt. warden's ITimer/ITicker were
  `C() <-chan time.Time` plus Stop/Reset and nothing dispatched on them; they
  are now warden.Timer{C <-chan time.Time; Stop func() bool; Reset func(d
  time.Duration) bool} and warden.Ticker{C <-chan time.Time; Stop func()},
  filled by RealClock from time.Timer's own method values and by testclock from
  its waiter closures. The exemption SURVIVES only for genuinely behavioural
  returns; the two that survived the corpus-wide re-audit are recorded per site
  in exceptions.md. A comment in the source ARGUING that a rule does not apply
  is the tell that it does — clock.go had one for exactly one day.
  Unlike CS-5/CS-6/CS-7 this one is a VERDICT, not a locator:
  fix it, do not answer it. It BLOCKS, alongside CS-1 and CS-2, and the native
  report supplies the current count. A SECOND, NON-BLOCKING lane
  (tools/check-ifacereturn.sh, the go/analysis analyzer at
  tools/ifacereturn) flags EVERY interface-typed result in every
  module — methods, stdlib, third-party, `any`, error exempt — and reports
  ruled-correct code on purpose. Do not silence it and do not treat its count
  as a failure.
- CS-9 TESTS NEVER HAND-ROLL TIME. Operator, 2026-09-02, verbatim: "we keep
  rewriting the same await test helper function holy shit" — nine private
  await helpers across the tree, in a repository where 89 test files already
  called gomega's Eventually. A wait for a condition is
  pkg/eventually: eventually.Await[Value](reporter, what, budget, poll,
  match) polls a TYPED value and returns the one match accepted, and
  eventually.Consistently[Value] is the same for an absence. Do NOT write a new
  deadline-and-sleep loop, and do NOT write a new private awaitXxx helper
  around one. A helper that carries real domain logic is reimplemented ON the
  primitive keeping its signature; a helper that was only the loop is deleted
  and its call sites name what they wait for.
  Not "just call Eventually", and the reason is CS-7: Eventually is a
  pre-generics reflection API taking `any`, so prescribing it at every call
  site is the erasure-as-public-contract defect applied 89 times. eventually is
  the typed shell — gomega is its engine, contained inside the one package,
  and no caller holds an untyped value. The payoff is the failure message:
  Eventually(func() bool {...}).Should(BeTrue()) says "false is not true",
  while a typed poll prints the value that was actually there.
  FOUR COUNTERWEIGHTS, and CS-9 without them deletes experiments:
  (1) A sleep that GENERATES LOAD OR PACES something is the subject of the
  test, not a wait for one, and it STAYS — a load generator's rate, a link
  throttled to N bytes/second, a sampler running a fixed window, an
  observation window a measurement is per-unit-of, sends spaced so they cannot
  coalesce, a reconnect backoff, the stimulus a race is scheduled by. Also a
  BEST-EFFORT QUIESCE (sample until two readings agree, return whatever you
  have) that is itself polled by an outer Eventually: it must return rather
  than fail, because a fatal await inside a poll aborts the retry doing the
  waiting. Say in a comment which one it is.
  (2) Consistently is the negative-space twin. A bare sleep followed by one
  read is usually reaching for it: it samples an absence once, at the least
  informative moment.
  (3) BUDGETS ARE NAMED AND GENEROUS. The costs are not symmetric — too large
  makes a failing test slow, too small makes a CORRECT test red on a loaded
  machine. `Eventually(p.output, "5s", "20ms")` inline, for a real process to
  start, bind a port and win an election, is what flaked the warden CLI
  contract suite. Give the budget a name in the suite.
  (4) The loop belongs to whatever OWNS THE DATUM. livetest.Client.Await
  blocks on the frame channel and fails naming every frame it saw; reaching
  past an owning library's own typed await for the generic one costs a
  diagnostic. Ginkgo suites may keep native Eventually where matcher
  composition earns it (the framework is mandated there), but a spec that only
  polls a BOOL should prefer the primitive — unless the poll genuinely has no
  value worth printing, in which case say so.
  The gate flags a time.Sleep inside a for body in a _test.go file and, like
  CS-5/CS-6/CS-7, is a locator rather than a verdict — 100% of its current
  findings are correct code.
- CS-10 A SERVICE IS CONCEPTUAL; IT NEVER OWNS THE PROCESS. Operator,
  2026-09-02, verbatim and typo included: "SERVICES SHOULD NOT REQUIRE
  CONTAINERIZATION SERVICES ARE CONSIDERED CONCEPTULA AND ARE INTENDED TO GO
  INTO SOME BINARY VIA A FUNCTIONAL OPTION". A service is a LIBRARY PACKAGE of
  composable business logic whose entry point is New(options ...Option)
  (*Service, error) with WithX options, in the pkg/cron/options.go
  shape, validating the whole option set before building anything. It has NO
  func main, NO flag.Parse, NO environment read, NO signal handler, NO compose
  file as its DEFINITION and NO container requirement; the context arrives from
  the caller, and a listener is opened only if the caller asks — a caller that
  already owns an http.Server takes the handler instead. Runnable composition
  is an app/cmd concern: flags, environment, signals and process lifetime live
  in the binary. Deployment — container, compose, systemd, bare go run — is a
  property of the BINARY and its operator. THE TEST: could a different binary
  use this, and could it run with no container at all? If no, the seam is in
  the wrong place.
  CONTAINER-FIRST AND CS-10 DO NOT CONFLICT, and do not report them as
  conflicting. The monorepo's container-first rule governs HOW BINARIES ARE
  DEPLOYED on this fleet (put the deployment in a container and Compose rather
  than on the host); CS-10 governs WHAT A SERVICE PACKAGE IS. Both are true of
  the same service at once. What CS-10 forbids is the deployment answer
  becoming the definition.
  THREE COUNTERWEIGHTS, and CS-10 without them is a licence to delete compose
  files and reshuffle directories:
  AMENDMENT (operator, 2026-09-04, verbatim): "recall all services are meant
  to be options that can be slid into a pre-existing gin binary it's never a
  service's job to start a new server process" and "there should NOT be a
  cmd/ dir nor a dockerfile ... those are EXCLUSIVELY ONE TO ONE TO A BINARY".
  Counterweight (1) below is WITHDRAWN: a service exposes New<Thing>(...Option)
  and Register(router gin.IRouter) and NOTHING that owns a process — no
  listener, no engine of its own, no Run, no cmd/, no Dockerfile. The binary
  in go/app/ or app/ owns the engine (go/pkg/httpserver.NewEngine),
  the listener, flags, signals and its Dockerfile. Existing services/*/cmd
  are the retrofit backlog.
  (1) [WITHDRAWN 2026-09-04, kept for the record] A THIN cmd/ INSIDE a service directory is an acceptable runnable shell —
  argv to options, options to New, New to Run, no decisions of its own. go/
  puts cmd/ under services/<name>/ and  puts it under app/<name>/;
  both satisfy the rule. CS-10 does NOT mandate moving go/services/*/cmd into a
  go/app/, and do not propose that as a fix.
  (2) A COMPOSE FILE MAY LIVE BESIDE A SERVICE as one documented deployment
  option — provided the bare-binary path WORKS and is documented FIRST.
  Documentation order is load-bearing: a README opening with `docker compose
  up` and burying "bare metal, for development" thirty lines down has told the
  reader the container is the thing. DELETING COMPOSE FILES IS NOT WHAT THIS
  RULE ASKS FOR.
  (3) Infrastructure that IS containerization — the edge Caddy, Authelia, the
  runbook stacks — is OUT OF SCOPE. There is no library underneath them.
  The native ADVISORY CS-10 check locates main/configuration/environment,
  signal, listener and Gin-engine calls under services, plus tracked service
  cmd/ and Dockerfile artifacts. Compose files are ALLOWED. These syntax
  candidates do not prove the ownership boundary; read the service and use
  eval case 031 and the per-directory audit in exceptions.md for judgment.
- CS-11 TESTS DOT-IMPORT GINKGO/GOMEGA. Operator, 2026-09-03: a `_test.go` file
  imports `github.com/onsi/ginkgo/v2` and `github.com/onsi/gomega` with a DOT —
  `. "github.com/onsi/ginkgo/v2"`, `. "github.com/onsi/gomega"` — so specs read
  Describe/It/Expect/Eventually unqualified, the way this tree's suites already
  did 709 times at adoption. A PLAIN, ALIASED, or BLANK (`_`) import of either of those two
  exact packages in a test is the violation. TWO COUNTERWEIGHTS: (1) NON-TEST
  code that depends on gomega/ginkgo as a LIBRARY imports it QUALIFIED —
  pkg/eventually wraps gomega as its await engine, and dot-importing an
  assertion vocabulary into a production namespace is the anti-pattern the
  exemption exists for; the scope is the file name, so a production file is not
  in scope at all. (2) The gomega/ginkgo SUB-packages (gomega/gstruct,
  gomega/gexec, gomega/types, ginkgo/v2/... helpers) stay QUALIFIED — the rule
  targets EXACTLY the two quote-bounded paths, nothing else. THIRD, STRUCTURAL
  EXEMPTION, forced by the language: an IN-PACKAGE test whose own package
  declares a top-level identifier the library ALSO exports cannot dot-import it
  without a REDECLARATION compile error, so that import stays qualified (redis
  declares Entry, which ginkgo's DescribeTable helper is also named; eventually
  declares Consistently, which gomega exports). Where the test can become an
  external `_test` package cleanly, do that and the collision disappears; where
  it cannot (unexported internals, or an exported type whose name IS the
  collision), keep the qualified import. This is a VERDICT, not a locator: a
  plain import of one of two exact paths in a test is a violation on sight. It
  BLOCKS in the native OCaml house runner's repo-wide CS-11 check. Do NOT
  silence a finding with a marker comment or an
  exclusion list — dot-import the test, or move it to an external package, or
  (only when a redeclaration forces it) keep the qualified import and say why.
- CS-12 A CONSTRUCTOR NAMES WHAT IT BUILDS. Operator, 2026-09-04, verbatim:
  "can we please stop just naming shit "new" and instead name shit like
  NewXyzThingThatItIs". A constructor is named for the concrete type it
  returns — NewCopilotAdapter, NewPostgresStore, NewCopilotBridge — never bare
  New and never a generic noun only the package qualifier disambiguates
  (NewStore, NewClient, NewService); the package name is not part of the name.
  Unexported constructors follow the same rule (newSessionRegistry). Three
  counterweights: generated code keeps its generator's names (sqlc's
  storedb.New, oapi-codegen's NewClient, .pb.go) because renaming output is
  editing output; a mirrored third-party constructor keeps the mirrored name
  and says so; and the rename pulls a wrongly generic TYPE name with it
  (Service → CopilotAdapter) but nothing else. The native ADVISORY CS-12
  detector covers New, new, NewStore, NewClient, NewService and private
  variants, excluding marked generated code. The dated baseline is historical;
  read current findings rather than treating that count as a current scan.
- CS-13 NO MAGIC STRINGS. Operator, 2026-09-04, reading inline "idle",
  "running", "store_error" and "session_not_found" in the copilot adapter,
  verbatim: "have an agent write a fucking lint check for magic strings i'm
  tired of this. if a string isn't defined as a constant somewhere, then it's a
  fucking magic string i think". A string literal that is a VALUE — compared
  against, assigned into a field, passed as an argument, returned, used as a
  map key, put in a slice — is a magic string unless it is the initializer of a
  `const` or a package-level `var`. Status names, kinds, error codes, header
  names, query-parameter names, column and enum values, environment variable
  names and flag names are declared once, named, in the package that owns their
  meaning. A GENERATED ENUM IS THE PREFERRED SOURCE — api.SessionStatusIdle,
  sqlc's column constants, protoc's — because it cannot drift from the
  contract; convert at the seam, `string(api.SessionStatusIdle)`, rather than
  re-spelling the value. SIX COUNTERWEIGHTS: (1) human-facing text is not a
  value — error messages, log messages, format strings and their `%` verbs,
  documentation strings and panic messages stay inline, because nothing
  dispatches on prose; (2) struct tags, import paths, `//go:generate` and
  `//go:embed` lines and build constraints are syntax, not values; (3)
  `_test.go` files are out of scope, because a spec spelling "idle" is
  asserting the wire value on purpose; (4) generated files are outside the
  corpus already; (5) an empty string and single-character separators ("", " ",
  ",", "/", "\n") are not magic; (6) a literal initializing a `const` or a
  package-level `var`, or standing as an element of a package-level composite
  literal (a registry table, CS-6), IS the declaration, not a use. The gate
  reads a literal standing in a FUNCTION BODY and, like CS-5/CS-6/CS-7/CS-9,
  is a locator rather than a verdict: it reads 2856 on the corpus and it NEVER
  blocks. Answer a finding — "this is the error's message", "this is a
  separator" — never silence it.
- CS-14 NO HAND-ROLLED TWIN OF A LIBRARY PRIMITIVE. Operator, 2026-09-04,
  reading optionalTime/nullableString/optionalUUID in the copilot adapter with
  nullTime/valueTime/nullString/valueString already in the cron store, verbatim:
  "WHY ARE YOU WRITING MORONIC FUNCTIONS LIKE OPTIONALTIME WE ALREADY HAVE A
  NULLS PACKAGE THAT WE USE FOR THAT HOLY SHIT WRITE A LINTER FOR THAT" — and,
  asked which: "it's guregu null". A receiverless function converting between a
  library's optional type (sql.NullString, sql.NullTime, sql.NullBool,
  sql.NullInt16/32/64, sql.NullFloat64, uuid.NullUUID) and the plain or pointer
  value it wraps is a TWIN: a second spelling of something the tree already
  has, which drifts the moment either one is touched. THE HOUSE ANSWER IS
  github.com/guregu/null/v5, EMITTED BY sqlc go_type OVERRIDES — see
  go/services/copilot-adapter/store/sqlc.yaml, the reference declaration:
  nullable text becomes null.String, nullable timestamptz becomes null.Time,
  nullable uuid becomes *uuid.UUID. Callers then read .Ptr()/.ValueOrZero() and
  write null.TimeFrom/null.StringFrom, and NO CONVERSION HELPER EXISTS. This is
  CS-3 and CS-4 pointed at one instance: the layer that owns this primitive is
  a library somebody already imported, so the correct number of copies here is
  ZERO — not one shared nulls package of our own, which would be a third
  spelling. THREE COUNTERWEIGHTS: (1) generated code and _test.go files are out
  of scope, the same file-name scope CS-9/CS-11/CS-13 carry; (2) CARRYING an
  optional through is not converting it — a store method taking a sql.NullTime
  in order to write it keeps the value in the library's type on both sides, so
  the detector reads only a receiverless function whose ENTIRE parameter list
  is one value and whose SINGLE result is the other shape; (3) there is no
  exempt shared package and that is the ruling, not an oversight — a house
  From/New wrapper would be a twin of the twin. The gate is report-only and,
  like CS-5/CS-6/CS-7/CS-9/CS-13, a locator, and it NEVER blocks. But unlike
  those it has NO RETROFIT BACKLOG: it found 5 at introduction, all of them
  pkg/cron/postgres/store.go, and that store was migrated onto the
  library in the same change, so that historical baseline read 0. The native
  report supplies the current count.
  For new code this is a verdict on sight: do not write the helper.
- CS-15 WHOEVER STARTS A GOROUTINE OWNS ITS CLEANUP. Operator, 2026-10-01,
  verbatim: "go routines do not start only in runtime services can start goroutines, they just have to be responsible for cleanup and, if you have inter-service things, then YOU'RE responsible for lifecycle management". Every goroutine needs a way to stop (a context
  it watches) and a join (WaitGroup, errgroup, conc, a runtime.Scope, or a
  receive on a channel it signals). A mounted service's *runtime.Scope
  (scope.Go / scope.GoOwner) is the easy way, not the only one. Tests are
  NOT exempt (operator: "WHY THE FUCK DOES THE GATE EXEMPT TESTS"); a spec's
  goroutines are joined and goleak proves it. Advisory locator.
- CS-16 BOUNDARY CROSSINGS ONLY UNDER ipc/ (os/exec only ipc/proc; net
  dial/listen + grpc.NewClient + os file APIs only ipc/; pgxpool/pgx.Connect
  only ipc/db/csfpg). A service takes the capability (ipcnet.IListener,
  ipcnet.IDialer, ipcgrpc.IClientConnector, csfpg.IDB, proc.ILauncher, ...)
  in its constructor; the binary opens the one pool with csfpg.OpenPool and
  closes it after its services stop. Every program (git, gh, docker compose,
  a shell) starts through proc.ILauncher, never exec.Command. Tests included:
  a spec uses a gomock double (csfpg/mocks.MockIDB, a MockILauncher) or
  pgmem, never its own pool or child process (operator: "TESTS COULD JUST USE
  GOMOCK" / "OR USE PGMEM"); real PostgreSQL only in the opt-in `acceptance`
  build tag, through csfpg.OpenPool. The net/grpc and process parts are
  advisory locators; the PostgreSQL part, CS-16-DB, BLOCKS.
- CS-17 CONFIG IS A CAPABILITY. os.Getenv/os.LookupEnv/os.Environ appear
  only in runtime/config and in a binary's own go/app/<name>/config
  package; tests are NOT exempt (a spec builds runtimeconfig.NewEnvironment
  over a map, or sets variables with GinkgoT().Setenv). The binary declares
  each name as a constant and its default in the capability call
  (environment.String(name, fallback)); the monorepo's Compose environment check reports
  Compose names set but never read (dead) and read with no default but never
  set (missing). Advisory locator.
- ONTOLOGY-DIRS. Directories under  are named by CSF ontology terms
  (architecture.csf); `internal/` is transparent; nothing is named engine,
  util, common or core. Advisory locator.
- CS-18 THE TEST LAYOUT. Operator, 2026-10-01, verbatim: "TESTS COULD JUST
  USE GOMOCK" / "OR USE PGMEM"; "like if you have an exported interface, you
  have a reason to do gomock"; "unit tests remain in the package,
  "integration tests" are outside the package and test the client usability
  and defined and undefined usage patterns thanks to gomock"; "WHY THE FUCK
  DOES THE GATE EXEMPT TESTS". (1) UNIT tests are in-package (`package foo`)
  and test internals. (2) INTEGRATION tests are the external `package
  foo_test` specs: exported API only, every dependency a gomock mock of an
  exported interface, and they MUST cover undefined usage (wrong order, nil,
  use after Close, double Start) asserting the DEFINED error
  (MatchError(foo.ErrClosed)). (3) Tests make NO REAL CROSSINGS: no real
  database pool, no listen/dial (httptest.NewServer counts; NewRecorder does
  not), no subprocess, no container. Use gomock, or pgmem as the database
  substrate; when pgmem lacks a feature, EXTEND PGMEM. (4) Every exported
  I-interface has a //go:generate mockgen directive and its generated mock
  is committed. (5) House gates do not exempt test files. Counterweights:
  pgmem and generated mocks are not crossings; a suite whose SUBJECT is real
  infrastructure may cross only as the named opt-in acceptance suite, labelled
  `//go:build acceptance` above the package clause (`integration` is NOT that
  label: "integration" means the external gomock specs), and it never stands
  in for the external specs. Never label a test of your own code acceptance
  to silence the gate. Advisory native locators CS-18-MOCKGEN,
  CS-18-CROSSING, CS-18-EXTERNAL; each flips to mandatory only at zero.
- VERTICAL SLICES. Work is delivered as vertical slices defined by their
  acceptance surface, not the directories edited: each slice brings the
  ontology terms, primitives, gate rules, consumer migration, tests and
  user-visible proof it needs, on top of its completed dependencies. Never
  deliver a layer-only change (ontology-only, gate-only, database-only,
  frontend-only). Name your slice and its proof in every report.
- DRAFT PR IMMEDIATELY: push and open a DRAFT PR within minutes of your
  first commit (wip commits are fine); push after every commit. A branch with
  commits and no PR is a gap the orchestrator will fill for you.
- AGENT OPERATING RULES (root AGENTS.md "Agent operating rules"). BASH SHAPE:
  one plain command per call, literal absolute paths inside your worktree, no
  cd chains, loops, shell variables or $(...); files change through Write/Edit;
  a rejected command is reshaped, never resent. HOOK TIMEOUT: retry once, then
  the heredoc / python replace-once (assert count == 1) fallback, then stop and
  report the blocked tool and hook. WAITING: never `sleep N; check`; use
  run_in_background, the Monitor tool on an until-loop, or
  `gh run watch <id> --exit-status`. VERIFY BEFORE USE: ls the path, grep the
  symbol, print JSON keys, run --help before using anything you have not seen
  this session. Re-read before every Edit; print expected vs replaced counts
  after any scripted edit. Go and linters run in the pinned containers
  (tools/bazel.sh, the Go image ci.yml uses), never on the host. Skills: writing-new-tests before tests, conventions-preflight
  before a new package, change-impact-sweep before reporting done.
- Report style findings you did not fix rather than silently leaving them.
  Verify with: bash tools/check-house-lint.sh --test --summary house-lint-report.md
  That is the single CI house-lint entrypoint. The OCaml policy.ml registry
  makes CS-1/CS-2/CS-3/CS-8/CS-11, TEST-BOOTSTRAP and DEPENDENCIES mandatory;
  all other findings are advisory. The runner checks native Go AST rules and
  invokes pinned dupl, Go typed interface-return analysis and golangci funlen
  specialists. No Python scanner owns the CI verdict.
  Exit 1 means mandatory findings; exit 2 means a scanner/tool error. Advisory
  findings never fail, but advisory tool errors do. The report records each
  rule, severity, count or specialist result; CI retains it and specialist logs.
  bash tools/check-house-lint.sh runs the complete checks without native regressions.
  bash tools/check-house-lint.sh --report-only (or --report-only) reports findings without blocking but
  still fails on errors. --quiet suppresses individual findings. --native-only
  skips reuse, typed interface returns and function length: that is a PARTIAL
  check and cannot establish a full CI pass.
  CS-4/CS-10 candidates and other advisory findings still need judgment. Answer
  each finding at its site; do not silence it with a narrowed heuristic,
  exclusion or marker comment. Component generation checks still own drift.
  Legacy Python census/eval tools preserve historical measurement semantics;
  they do not measure new native AST coverage or replace the CI report.

```
