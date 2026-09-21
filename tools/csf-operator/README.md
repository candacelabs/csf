# Standalone CSF operator

From the repository root of a fresh checkout, run `./install.sh`. It builds the locked Rust operator
and installs the `candace` launcher. `candace csf` is the same as
`candace csf up --dry`: it validates the checked-out runtime and prints the
startup plan without calling Docker or HTTP. `candace csf --help` prints the
command reference. `candace csf up` creates private
local credentials and evidence state, starts the pinned
Langfuse/OpenSearch/PostgreSQL dependency set in one `csf` Compose project,
provisions native search tools and the local embedding model, builds the CSF Go
runtime inside the pinned Go container, initializes its database schema once,
waits for HTTP readiness, and prints the loopback URL.

```sh
candace csf
candace csf up --dry
candace csf up
candace csf status
candace csf logs --service runtime --tail 80
candace csf down
```

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

The existing `brainspine` protobuf package and `brain-knowledge` OpenSearch
index remain compatibility identifiers. They are internal storage/wire names,
not the standalone operator name.
