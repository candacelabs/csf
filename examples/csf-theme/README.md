# CSF Workbench theme consumer

This runnable example composes the CSF service in one Go process and exposes its
HTTP operations and MCP tools. It reads one operator-owned CSS file from a
consumer-selected directory. The filename is always
`csf.WorkbenchThemeFileName` (`workbench-theme.css`); neither the CSS contents
nor a filepath can be supplied as operation input.

From the public archive or repository root containing `go.mod`, start it with:

```bash
go run ./examples/csf-theme \
  -listen 127.0.0.1:8089 \
  -theme-dir ./examples/csf-theme
```

`-listen` selects the HTTP address, and `-theme-dir` selects only the directory
that contains `workbench-theme.css`. The example file in this directory is a
small sample. CSF loads the file during startup; a missing or empty file uses
the default Workbench appearance. The configured file is limited to 65536
UTF-8 bytes.

The HTTP API reads the current in-memory theme and its resolved host file path:

```bash
curl -sS http://127.0.0.1:8089/api/workbench/theme/get \
  -H 'Content-Type: application/json' \
  -d '{}'
```

`GetWorkbenchTheme` returns `theme.customCss` and `theme.filePath`, the resolved
host path to the fixed file.

After editing that file, reload it without restarting the process:

```bash
curl -sS http://127.0.0.1:8089/api/workbench/theme/reload \
  -H 'Content-Type: application/json' \
  -d '{}'
```

Edit the host file directly. The `ReloadWorkbenchTheme` operation takes `{}`
and rereads only the fixed filename
under the configured directory. It updates the active theme on successful
validation and returns the theme snapshot. Missing or empty CSS restores the
default theme; invalid UTF-8 or CSS larger than 65536 bytes leaves the active
theme unchanged. The MCP surface offers the same `GetWorkbenchTheme` and
`ReloadWorkbenchTheme` capabilities at `/mcp`; reload takes no CSS or filepath
arguments:

```bash
curl -sS http://127.0.0.1:8089/mcp \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -H 'MCP-Protocol-Version: 2025-11-25' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ReloadWorkbenchTheme","arguments":{},"_meta":{"io.modelcontextprotocol/protocolVersion":"2025-11-25"}}}'
```

The example exposes CSF HTTP and MCP endpoints only. It does not serve or embed
the packaged React Workbench. A consumer that wants the browser UI must build
and serve that bundle explicitly and configure its CSF API origin to reach this
process.

## Cleanup and file ownership

Two separate defers provide no extra guarantee over one deferred block that
closes a temporary file and then removes it. Go runs separate defers in reverse
registration order; grouping related cleanup makes that ordering explicit.
For a file writer, checking `Close` before renaming the temporary file matters:
a close failure must prevent publication. A later deferred close/remove is
best-effort cleanup and should preserve the original failure.

CSF only reads this stylesheet; the editor owns saving it. Its single deferred
close releases the read descriptor, while read and validation errors are
returned to the caller. It does not need a file-writing cleanup helper.
