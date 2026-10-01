# Standalone CSF operator

From the repository root of a fresh checkout, run `./install.sh`. It uses Docker-backed Bazel to build the locked Rust operator
and installs the `csf` launcher. `csf` is the same as
`csf up --dry`: it validates the checked-out runtime and prints the
startup plan without calling Docker or HTTP. `csf --help` prints the
command reference. `csf up` creates private
local credentials and evidence state, starts the pinned
Langfuse/OpenSearch/PostgreSQL dependency set in one `csf` Compose project,
provisions native search tools and the local embedding model, builds the CSF Go
runtime inside the pinned Go container, initializes its database schema once,
waits for HTTP readiness, and prints the loopback URL.

```sh
csf
csf up --dry
csf up
csf status
csf logs --service runtime --tail 80
csf down
```

`csf docs check` runs documentation acceptance in the prepared documentation
renderer environment:

```sh
csf docs check --repo /repo --work /tmp/docs-work --site /tmp/docs-site \
  --python /opt/docsite/venv/bin/python
```

The checkout must contain `docsite/build_docs.py`, its complete documentation
inputs, and both documentation and editor tests. The command checks Python
dependencies, runs those tests with no skips permitted, invokes the full
renderer including Go and CLI generation, and verifies the resulting pages,
source/build provenance, and actual CSF keyword highlighting. It fails explicitly
when the renderer is absent; the standalone source snapshot does not include
the private documentation site. Work and site directories must be separate
and outside the checkout. This command starts no CSF runtime or containers.

The default private state is `~/.local/state/csf`; set
`CANDACE_CSF_STATE_DIR` to keep it elsewhere. Credentials and database URLs are
mode `0600` under mode `0700` directories. Compose volumes survive `down`.
Startup does not require a prebuilt binary, an evidence path, service
credentials, or a pre-created Docker network. MLflow remains available as an
opt-in Compose service and does not start with CSF.

The runtime, Langfuse UI, OpenSearch API, and PostgreSQL host port bind to
loopback. The generated Compose network belongs to `csf`; it does not
join another project. First startup pulls pinned images and registers the
MiniLM model, which needs internet access and can take several minutes. The
configured memory limits total about 16 GiB.

The runtime image includes the pinned Copilot CLI. Persistent Workbench agents
and scheduled sessions need an authorized GitHub Copilot credential supplied
through `COPILOT_GITHUB_TOKEN`, `GH_TOKEN`, or `GITHUB_TOKEN`. The operator
copies that credential into private mode-0600 state and does not claim
scheduled execution when no token is available.

Workbench preparation uses Git from the built runtime image, so the operator
does not require Git on the host. A source archive becomes a new private Git
snapshot containing its `.candace-source.json` provenance; its local commit is
an import, not the upstream source commit. Installer build outputs and Bazel
convenience links are excluded, and no remote is added. A standalone Git
checkout is cloned locally instead. Existing private Workbench repositories
and edits are retained; failed preparation never publishes a partial repository.

The existing `brainspine` protobuf package and `brain-knowledge` OpenSearch
index remain compatibility identifiers. They are internal storage/wire names,
not the standalone operator name.

Operator progress uses concise `[RUN]`, `[PASS]`, and `[FAIL]` messages. A pass
reports a verified result; completion names what is ready and the next command.
Machine-readable command output stays separate from progress diagnostics.
