# A CSF host owned by another repository

This example adds a custom `/consumer/snapshot` endpoint beside CSF's generated
HTTP operations and `/mcp` endpoint. All capabilities run in the consumer's Go
process. The binary owns its listener and cancellation context.

From the Candace module root:

```sh
go run ./examples/csf-consumer -theme-dir ./examples/csf-consumer
curl http://127.0.0.1:8089/consumer/snapshot
```

With no `-events` file configured, the response reports zero events and
`no event source configured`. That is a useful missing-data result, not a
successful simulation. Supply `-events /path/to/research-events.jsonl` to inspect
an existing CSF event artifact. `-listen` defaults to `127.0.0.1:8089`.

The host reads `workbench-theme.css` from `-theme-dir`. The filename is fixed by
`csf.WorkbenchThemeFileName`. Edit that file, then call `ReloadWorkbenchTheme`
through HTTP or MCP. This example serves the theme capability; it does not serve
the browser Workbench bundle or start a model provider, simulator or database.

## Copy into your own Go repository

Download and verify one `candace-<sha12>.tar.gz` source archive and extract it
beside your new repository. Copy `main.go`, `consumer_test.go` and
`workbench-theme.css` from this directory into your repository, then run:

```sh
git init
go mod init example.invalid/my-csf-host
go mod edit -require=github.com/candacelabs/csf@v0.0.0
go mod edit -replace=github.com/candacelabs/csf=../candace-<sha12>
go mod tidy
go mod vendor
go test -mod=vendor -race ./...
go build -mod=vendor -o my-csf-host .
./my-csf-host -theme-dir .
```

Replace `<sha12>` with the archive's actual directory name. The `replace`
selects the verified extraction while resolving dependencies; the vendored
build needs only your repository. When consuming a published export tag instead,
use `go get github.com/candacelabs/csf@export-<sha12>` and omit that replace.

The repeatable acceptance command creates this separate Git repository itself:

```sh
bash examples/csf-consumer/test-archive.sh \
  /path/to/candace-<sha12>.tar.gz /tmp/my-csf-consumer-check
```

The output directory must be new. It retains the extraction, consumer Git
repository, vendored dependencies, binary, logs and archive digest. After
vendoring, it runs race tests and builds in a container with networking disabled
and only the consumer repository mounted. It also starts the built binary,
requests its custom endpoint and theme, then verifies clean signal shutdown.
The tests exercise real HTTP and MCP,
read the generated snapshot, invoke the custom endpoint, reload CSS, reject an
invalid path argument, and verify listener cleanup.

The harness requires Docker, Git and standard Unix tools; compilation uses the
pinned Go container. It does not deploy anything.

## Bazel dependency

The same archive is a Bazel module. A consumer's `MODULE.bazel` can select it:

```starlark
bazel_dep(name = "candace", version = "0.0.0")
archive_override(
    module_name = "candace",
    integrity = "sha256-<archive integrity>",
    strip_prefix = "candace-<sha12>",
    urls = ["https://example.invalid/releases/candace-<sha12>.tar.gz"],
)
```

Use the actual release URL and integrity from your archive receipt. The public
labels used by this host are `@candace//csf`, `@candace//pkg/httpserver`, and
`@candace//proto/candace/brainspine/v1:brainspine`. The existing
[external-consumer guide](../external-consumer/README.md) explains dependency
mapping and the complete `archive_override` consumer setup.

[EXTENDING](../../csf/EXTENDING.md) walks through the composition seam.
