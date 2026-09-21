# Extending CandaceOS from your own repository

CandaceOS Core exposes its extension points to the Go compiler, not to a plugin
loader. Your repository pins one published snapshot of this one, implements
ordinary Go interfaces, and links its own Core binary. Nothing is loaded at
runtime, nothing is forked, and nothing is mirrored.

There are four seams, and this guide is one section each:

| Seam | Option | What you supply |
|---|---|---|
| Agent harness | `bootstrap.WithHarnessFactory` | a `harness.IFactory` your repository owns |
| Composed services | `bootstrap.WithComponent` | ordered bring-up for values your repository owns |
| Identity | `bootstrap.WithBrand` | product name, agent name, wordmark, palette |
| Presentation | `bootstrap.WithUIOverlay`, `WithNavItem`, `WithHTTPService` | templates, assets, sidebar entries, routes |

Core keeps everything else: PostgreSQL state, Warden observation, approvals,
reconciliation, receipts, the HTTP API, and the operator UI. Every seam is
resolved by the Go compiler and linker, so a mistake is a build failure rather
than a runtime surprise.

Each section names the worked example that compiles the same code, and each
example has its own test suite. Start from the example; come back here for the
rules it obeys.

---

## Pin one snapshot

This repository is generated: it is the tracked `candace/` tree of a private
monorepo, published as a fresh snapshot with no history. Every published
snapshot carries an immutable `export-<sha12>` tag and a matching GitHub
Release, and each Release carries a deterministic source archive,
`candace-<sha12>.tar.gz`, with its `.sha256` beside it. That archive is the
consumption artifact.

The archive is the tracked tree re-rooted so `MODULE.bazel` sits at the archive
root. Nothing is generated, stripped, or rewritten at packaging time, because
the Bazel metadata is committed here. Packaging normalizes ordering,
timestamps, and ownership, runs twice, and fails unless the two runs are
byte-identical, so the checksum on the Release is a property of the revision
rather than of the machine that built it.

### From Bazel

Download the archive and its `.sha256` from the same Release, then use the
[checksum verification and SRI command](../README.md#consume-it-in-60-seconds).
The sidecar contains a hexadecimal checksum; `archive_override.integrity`
requires the command's complete `sha256-...` base64 SRI output.

[`examples/external-consumer`](../examples/external-consumer) is the worked
consumer *and* the acceptance test every archive passes before it is published:
a complete Bazel repository that chooses every seam at once — its own identity,
its own overlay, its own sidebar entry and page, three components of its own,
and a custom harness — links a Core binary from them, and runs its own suites.
Every target in it is built and every suite in it is run both supported ways
before an archive is kept. Start there rather than from a snippet — its README
explains the choice:

- **`bazel_dep` + `archive_override`** — the recommended shape. candace is a
  real Bazel module, so its own `MODULE.bazel` supplies its dependency
  versions, registers the Go SDK, and declares the repositories it packages
  itself; labels inside candace resolve through candace's mapping rather than
  yours.

  ```python
  bazel_dep(name = "candace", version = "0.0.0")

  archive_override(
      module_name = "candace",
      integrity = "sha256-...",          # verified archive's base64 SRI value
      strip_prefix = "candace-<sha12>",
      urls = ["https://github.com/candacelabs/csf/releases/download/export-<sha12>/candace-<sha12>.tar.gz"],
  )
  ```

- **`use_repo_rule` `http_archive`** — the fallback, for a URL or a workflow
  that cannot treat the tarball as a module. A repository fetched this way is
  not a module, so the labels inside candace's BUILD files resolve through
  *your* repository mapping: mirroring candace's `use_repo(go_deps, ...)` list
  becomes load-bearing rather than advisory, and the repositories candace
  declares for itself — `@pg_query_go`, which `pkg/pgmem` names — do not
  resolve until you copy those declarations too. It also downloads the Go SDK
  itself, because candace registers no toolchain when it is not a module.

Either way you point `go_deps.from_file` at candace's `go.mod` if your
repository has none of its own, and then `bazel mod tidy` writes candace's
whole `use_repo` list into yours; with a `go.mod` of your own, the list stays
your own direct dependencies.

A repository still building from a `WORKSPACE` file has a documented
second-class path in [`bazel/README.md`](../bazel/README.md). It is not tested
anywhere: Bazel 9, which this module pins, removed WORKSPACE support.

### From the go command

The module path is the repository path, so the `go` command needs nothing
special:

```bash
go get github.com/candacelabs/csf@export-<sha12>
```

There is no semantic-version tag. `@latest` therefore resolves a pseudo-version
of the default branch, which moves with each snapshot; naming the export tag
pins the exact revision the Release documents, and is what a reproducible build
should say. A Go-only consumer gets every package here, but not the Bazel
targets, the committed BUILD files, or the Rust workspace under `xetcas/`.

---

## Compose services alongside Core

[`services/candaceos/component`](../services/candaceos/component) orders
bring-up; it is not a dependency-injection container. Core never constructs a
component's value, never reads its configuration, and never hands it Core
state. A definition carries a name, a required assembly function, optional
start and stop hooks, and the other definitions it requires. Whatever a
component builds it stores in your own variables through the closure, so
ownership never moves.

Core owns exactly one guarantee, a bracket around the agent harness:

- every registered component is assembled before the harness factory is
  invoked;
- every start hook runs, in resolved order, immediately before the harness
  starts, and receives Core's lifecycle context, so goroutines started there
  are children of the Core lifecycle;
- every stop hook runs after the harness closes and before Core's store closes,
  in the exact reverse of the resolved order.

Core's own bring-up steps are definitions in the same graph, so built-ins and
registered components resolve as one topologically ordered list, with
components taking their place between reconciliation and harness construction.
The resolver is pure: Kahn's algorithm with the ready set processed in
registration order, never map iteration, with no I/O, no environment access,
and no component function invoked while resolving. One registration order
therefore produces exactly one bring-up order.

Requirements are declared by pointer identity through
`WithRequires(otherDefinition)`, never by name. Core's own steps are unexported
definitions, so your repository cannot name, require, replace, or observe them;
the capability boundary is enforced by the type system rather than by
convention. Pointer identity also means an edge can only point at an
already-constructed definition, so your own graph is acyclic by construction.
`Order` still reports a cycle in Core's graph with its full path, as in
`component: dependency cycle: a -> b -> c -> a`, and reports a requirement that
was never registered by naming both the component that declared it and the
requirement itself. Nil definitions, invalid or oversized names, and duplicate
names are rejected the same way. Because built-ins and registered components
share one graph, Core's own step names — `configuration`, `database`,
`database-recovery`, `fleet`, `node-agent`, `reconciler`, `harness`, `runtime`,
and `http` — are effectively reserved: a component using one is rejected as a
duplicate name. Every failure is a distinct package-level sentinel matched with
`errors.Is`.

`Capabilities` is the only Core surface a component receives, and in this
version it carries one method. `Log` writes a single INFO JSONL record through
Core's reporter as the event `component.<name>.<event>`, and the message is
redacted of configuration-derived secrets unless the embedding binary opted
into `bootstrap.WithPII`. Names are bounded to 64 bytes and events to 48 bytes
so the namespaced event stays inside Core's log-record bound. Component
configuration is read by your repository from its own environment or files;
Core exposes no component configuration surface.

### A two-component chain

[`examples/external-consumer/_workspace/steering/steering.go`](../examples/external-consumer/_workspace/steering/steering.go)
is the compiled version of this: a bounded store of operator steering inputs
and the service that reads it. The service requires the store, so the store
assembles and starts first and stops last, and the harness factory captures the
service value the consumer owns. The two definitions are the whole of the
Core-facing surface:

```go
// StoreComponent returns the definition that assembles the bounded steering
// store. The closure owns the whole value; nothing is injected into it.
func StoreComponent() (*component.Definition, error) {
	return component.New(
		"steering-store",
		component.WithAssemble(func(ctx context.Context, capabilities component.ICapabilities) error {
			steeringStore.mutex.Lock()
			steeringStore.capacity = Capacity
			steeringStore.inputs = make([]string, 0, Capacity)
			steeringStore.mutex.Unlock()
			return capabilities.Log(
				ctx,
				"assembled",
				fmt.Sprintf("steering store retains %d inputs", Capacity),
			)
		}),
	)
}

// ServiceComponent returns the definition that assembles the steering service.
// It requires the store definition by pointer identity, so Core assembles and
// starts the store first and stops it last.
func ServiceComponent(storeComponent *component.Definition) (*component.Definition, error) {
	return component.New(
		"steering-service",
		component.WithRequires(storeComponent),
		component.WithAssemble(func(ctx context.Context, capabilities component.ICapabilities) error {
			steeringService.mutex.Lock()
			steeringService.store = steeringStore
			steeringService.mutex.Unlock()
			return capabilities.Log(ctx, "assembled", "steering service bound to the steering store")
		}),
		component.WithStart(func(ctx context.Context) error {
			steeringService.mutex.Lock()
			defer steeringService.mutex.Unlock()
			if steeringService.store == nil {
				return ErrUnassembled
			}
			steeringService.running = true
			return ctx.Err()
		}),
		component.WithStop(func(context.Context) error {
			steeringService.mutex.Lock()
			defer steeringService.mutex.Unlock()
			steeringService.running = false
			return nil
		}),
	)
}
```

The composition root builds both definitions, registers them in order, and
hands the service value to its own harness factory. In the example these lines
sit among the presentation options rather than alone, but the component half of
the list is exactly this:

```go
steeringStore, err := steering.StoreComponent()
// ...
steeringService, err := steering.ServiceComponent(steeringStore)
// ...
options := []bootstrap.Option{
	bootstrap.WithComponent(steeringStore),
	bootstrap.WithComponent(steeringService),
	bootstrap.WithHarnessFactory(customharness.NewFactory(steering.Instance())),
}
```

Its `BUILD.bazel` names one dependency:

```starlark
load("@rules_go//go:def.bzl", "go_library")

go_library(
    name = "steering",
    srcs = ["steering.go"],
    importpath = "example.com/candace-external-consumer/steering",
    visibility = ["//visibility:public"],
    deps = ["@candace//services/candaceos/component"],
)
```

The example's own Bazel test resolves the same definitions with
`component.Order` and asserts the store precedes the service in either
registration order. A third component of the example's own — its noteboard
service — requires the steering service in turn, so the resolved order it
asserts is a chain of three that Core never constructed a link of.

### Failure attribution

A component failure during assembly or start is attributed to the startup
component `extension`, with the failing component's name carried in a separate
structured attribute. A stop-hook failure during teardown is reported on Core's
existing shutdown event, again naming the component in the `extension`
attribute, while a harness close failure keeps its own Core event. Core's eight
built-in attribution strings are frozen, so a component failure never
masquerades as a Core step.

A binary that registers no component emits exactly the telemetry bytes stock
Core emits. When components are registered, the existing `core.started` event
gains one attribute listing the resolved component order, truncated to the
telemetry attribute bound with a `,+<count>` tail when a very large set would
not fit.

---

## Rebrand the operator UI

`bootstrap.WithBrand` takes one `webui.Brand`, and Core reads it in exactly two
places: the control runtime stamps the product and agent names into every
snapshot it produces, and the web UI renders the wordmark and serves the
palette. Both receive the same resolved value, so the server-rendered pages and
the browser client cannot disagree about who they are.

```go
if err := bootstrap.Run(
	"dev",
	bootstrap.WithBrand(webui.Brand{
		ProductName: "Atlas",
		AgentName:   "Scout",
		Wordmark:    template.HTML(`<span class="brand-mark" aria-hidden="true"><span></span></span><span>Atlas</span>`),
		Palette:     webui.Palette{Canvas: "#101014", Ink: "#f2f4f2", Forest: "#2b1b4d"},
	}),
); err != nil {
	panic(err)
}
```

[`examples/custom-brand`](../examples/custom-brand) is the whole seam exercised
at once, with a hermetic suite that needs no database and no container.

The zero `Brand` is the stock CandaceOS identity, so you name only what you
change. Every unset field falls back: an unset `Wordmark` becomes the escaped
`ProductName` when a product was named, and the stock lockup when it was not.

Only brand-bearing strings are data. The product name and the agent name are
replaced everywhere they appear — page titles, meta descriptions, aria-labels,
and the sentences that name them — and every other string in the UI stays
literal. The browser routes, including the `/claws/...` paths, are unchanged by
a rebrand.

`Wordmark` is **operator-trusted markup**. It is emitted into the page
verbatim, without escaping, exactly like a template the operator wrote. Never
populate it from a browser request, a fleet node, an agent, or any other
untrusted source. It cannot smuggle script past the page's
Content-Security-Policy, which permits only same-origin scripts, but it can
still restyle or deface the shell.

`Palette` overrides the design tokens the stylesheet declares on `:root`. They
are delivered as a generated same-origin stylesheet under the asset space,
linked after `app.css` so its declarations win, with an ETag for revalidation.
There is no CSS rebuild and no inline style block: the page keeps
`style-src 'self'`, and the stock brand simply serves an empty body behind a
link the markup always carries. Every field of `Palette` names a token the
shipped stylesheet declares on `:root`, and a spec in the `webui` suite fails if
one ever stops doing so.

Palette values are validated rather than escaped, because a custom property is
substituted into the stylesheet as CSS rather than as text. Braces, semicolons,
at-rules, comment markers, CSS escapes, unbalanced groups or strings, control
characters, and resource-fetching functions such as `url()` are all rejected, so
one token cannot smuggle a second declaration or a network fetch into the page.
An invalid brand fails assembly before any infrastructure is opened, rather than
rendering a half-branded page.

The two brand-bearing names also travel in the WebUI snapshot as `system.name`
and `system.agent_name`. The embedded browser client reads both from there, so a
rebranded core ships the stock `app.js` and still names itself correctly in
every toast, transcript, and empty state.

---

## Extend the operator UI

Two further options widen the same seam without touching Core's routes. They
compose with `WithBrand` and with each other, and both fail assembly rather than
serving something broken. [`examples/custom-ui-page`](../examples/custom-ui-page)
is the smallest useful shape of them: stock identity, one entry, one page.

`bootstrap.WithNavItem` appends an entry to the sidebar, after Core's own Home,
Apps, Fleet, and Activity entries. It is repeatable and entries render in
registration order.

```go
bootstrap.WithHTTPService(reportsService{}),
bootstrap.WithNavItem(webui.NavItem{Label: "Reports", Href: "/reports", Glyph: "§"}),
```

A registered entry renders with the same markup, keyboard behavior, and aria
semantics as the shipped four, and it is a plain link: it carries no live count,
and following it is ordinary navigation. Setting `View` to the name of a section
the page renders instead switches to that section in place, the way Home and
Apps do. `Label` and `Href` are required; both are bounded, control-character
free, and escaped, and a target the browser must not follow — a `javascript:`
URL, say — is neutralized by the template rather than emitted.

`bootstrap.WithUIOverlay` supplies one filesystem shaped like the web UI's own,
and both subtrees are resolved overlay-first with embedded fallback:

```
templates/*.html   {{define}} blocks that redefine the shipped ones
assets/*           files served in place of the embedded asset of that name
```

Anything the overlay does not name keeps shipping from Core, so an overlay
replaces only what it names. Overlay assets are served by the same handler at
the same URLs and keep its cache, content-type, and `nosniff` headers; the
generated brand stylesheet still answers ahead of both layers; and an overlay
file outside `assets/` is never reachable through the asset route.

An overlay template file contributes `{{define}}` blocks and its text outside a
define is ignored, so replacing a whole page means defining that page's block
exactly as the embedded files do. These are the supported block names, the whole
of them:

| Block | What it renders | Receives |
|---|---|---|
| `index.html` | the operator home page | the page data |
| `chat.html` | the live session page | the page data |
| `primaryNav` | the sidebar navigation list | the bound entries: `Label`, `Href`, `Glyph`, `View`, `Active`, `Count`, `CountAttribute` |
| `statusPill` | one status chip | the status string |
| `browserRoutes` | the body's `data-route-*` attributes | the route table |
| `browserEnums` | the body's `data-enum-*` attributes | the enum table |

The two page blocks receive `.Brand`, `.Snapshot`, `.Nav`, `.Routes`, `.Enums`,
`.InitialJSON`, `.Unavailable`, and `.ChatSessionID`, along with every template
helper the shipped blocks use.

Two names are load-bearing beyond their markup. `browserRoutes` and
`browserEnums` are how the embedded browser client learns its URLs and enum
spellings, so an override that drops an attribute silently disables the behavior
behind it. The client reads the navigation back out of the rendered DOM rather
than from a list it hard-codes, so an overridden `primaryNav` keeps in-page view
switching as long as its entries carry `data-nav` values naming sections the
page rendered.

Overlay templates are **operator-trusted markup** on the same footing as
`Wordmark`: they are page source, not escaped content. Never assemble one from a
browser request, a fleet node, or an agent. The page's Content-Security-Policy
is unchanged and still permits only same-origin styles and scripts, so an
overlay that inlines either simply will not run — put a stylesheet in `assets/`
and link it instead.

---

## Run the custom Core

The custom binary inherits normal Core configuration. It does not require
`CANDACEOS_HARNESS_BACKEND` or built-in Copilot/Ollama settings; the compiled
factory selects `HARNESS_BACKEND_EMBEDDED`. Provider-specific configuration is
owned and read by your repository.

At minimum, Core needs:

- an absolute writable data directory and workspace;
- `CANDACEOS_DATABASE_URL` pointing at PostgreSQL;
- a reachable Warden for fleet observation;
- a reachable node agent for reconciliation.

With those services available:

```bash
mkdir -p "$PWD/.candaceos" "$PWD/workspace"
export CANDACEOS_DATA_DIR="$PWD/.candaceos"
export CANDACEOS_WORKSPACE="$PWD/workspace"
export CANDACEOS_DATABASE_URL='postgres://candaceos:password@127.0.0.1:5432/candaceos?sslmode=disable'
export CANDACEOS_WARDEN_URL='http://127.0.0.1:7717'
export CANDACEOS_AGENT_URL='http://127.0.0.1:8094'
bazel run //cmd:candaceos
```

### Deploying it to a fleet

For the standard three-node CandaceOS fleet, resolve the regular executable that
Bazel built:

```bash
bazel build //cmd:candaceos
CUSTOM_CORE="$(realpath "$(bazel cquery --output=files //cmd:candaceos)")"
```

Then run one deploy command from a checkout of the canonical monorepo whose
current pushed revision equals the export revision the binary was built from:

```bash
./candace/candaceos/fleet.sh deploy --harness custom \
  --core-binary "$CUSTOM_CORE" \
  --core-export-revision "$EXPORT_REVISION"
```

The deployer snapshots and hashes the executable, layers it over the standard
Core runtime image with
[`candaceos/Dockerfile.core.external`](../candaceos/Dockerfile.core.external),
deploys the usual Core, Warden, agents, and PostgreSQL topology, and records the
binary SHA-256 and export revision in the receipt. It rejects a source-revision
mismatch, so the custom Core and the canonical fleet services use one compatible
CandaceOS contract. Your repository needs no custom Compose files and no image
registry.

`fleet.sh` is the one part of the deployment kit that a clone of this repository
cannot run: it resolves a monorepo layout two levels above itself and reads the
export root out of that repository's Git history. Everything else in
`candaceos/` — `install.sh`, the updater, and the hermetic suites — runs here,
because this repository *is* the Go module root those build contexts reach for.

---

## What the archive contains

Everything tracked in this repository at the published revision, and nothing
else: the Go module and its committed BUILD files, the generated protobuf and
Liquid Proto bindings, the embedded Web UI assets and SQL migrations, the xetcas
Rust workspace, the deployment kit under `candaceos/`, the examples, and the
legacy WORKSPACE shim under `bazel/`. There is no allowlist and no generated
manifest, because the repository is the unit of publication and the Bazel
metadata is committed rather than stamped on at packaging time.

The toolchain the archive was built and tested with is stated inside it:
`.bazelversion` (Bazel 9.2.0) and `MODULE.bazel` (rules_go 0.62.0, Gazelle
0.52.2, Go SDK 1.26.5, rules_rust 0.73.0). [`bazel/versions.bzl`](../bazel/versions.bzl)
mirrors those pins for Starlark, and a test in the module fails if the mirror
drifts.
