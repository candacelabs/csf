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

Download and verify one `csf-<sha12>.tar.gz` source archive and extract it
beside your new repository. Copy `main.go`, `consumer_test.go` and
`workbench-theme.css` from this directory into your repository, then run:

```sh
git init
go mod init example.invalid/my-csf-host
go mod edit -require=github.com/candacelabs/csf@v0.1.0
go mod edit -replace=github.com/candacelabs/csf=../csf-<sha12>
go mod tidy
go mod vendor
go test -mod=vendor -race ./...
go build -mod=vendor -o my-csf-host .
./my-csf-host -theme-dir .
```

Replace `<sha12>` with the archive's actual directory name. The `replace`
selects the verified extraction while resolving dependencies; the vendored
build needs only your repository. This path also works for an authenticated
download from private staging. Only after the version is published to the
public module repository, use `go get github.com/candacelabs/csf@v0.1.0`
and omit that replace. A staging release alone does not make that command valid.

The repeatable acceptance command creates this separate Git repository itself:

```sh
bash examples/csf-consumer/test-archive.sh \
  /path/to/csf-<sha12>.tar.gz /tmp/my-csf-consumer-check
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
bazel_dep(name = "csf", version = "0.1.0")
archive_override(
    module_name = "csf",
    integrity = "sha256-<archive integrity>",
    strip_prefix = "csf-<sha12>",
    urls = ["https://example.invalid/releases/csf-<sha12>.tar.gz"],
)
```

Use the actual release URL and integrity from your archive receipt. The public
labels used by this host are `@csf//csf`, `@csf//pkg/httpserver`, and
`@csf//proto/candace/brainspine/v1:brainspine`. The existing
[external-consumer guide](../external-consumer/README.md) explains dependency
mapping and the complete `archive_override` consumer setup.

The canonical monorepo's external-consumer acceptance harness also has an opt-in `csf-consumer`
mode. It creates a fresh Bazel module whose only target is an alias to the
CSF-owned `@csf//app/csf/cmd:cmd` binary, then configures that binary through
`CSF_LISTEN`, `CSF_WORKBENCH_THEME_DIR` and `CSF_AGENT_MCP_KEY_FILE`:

```sh
bash tools/test_candace_external_consumer.sh HEAD csf-consumer
```

Set `CANDACE_EXPECT_ENV_CONFIGURATION=true` for release acceptance. The mode
requests the snapshot and Workbench theme operations, checks that a
raw agent MCP signing key is rejected, sends `SIGTERM`, and verifies that the
listener closes. It uses synthetic theme and key files only; it does not claim
provider delivery or deploy a service. Without that setting it also accepts an
older CSF revision, recording whether the same consumer's environment was
adopted so that an upgrade can be compared without changing consumer code.
The `all` mode continues to run the
existing Go, `archive_override` and `http_archive` consumers.

[EXTENDING](../../csf/EXTENDING.md) walks through the composition seam.
