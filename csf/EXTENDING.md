# Add CSF to your Go process

Import the public module and mount the service into the Gin router
that your binary already owns:

```go
import (
    "github.com/candacelabs/csf/csf"
    "github.com/candacelabs/csf/pkg/httpserver"
    "github.com/gin-gonic/gin"
)

service, err := csf.New(csf.WithWorkbenchThemeDirectory("./theme"))
if err != nil {
    return err
}
router := httpserver.NewEngine("my-csf-host")
service.Register(router)
router.Any("/mcp", gin.WrapH(service.MCPHandler()))
```

If your host already has a router, register the service on that router. The
binary owns its `http.Server`, listener, configuration and shutdown context.
`httpserver.NewStreamingServer` and `httpserver.Serve` provide the repository's
shared streaming-server and bounded-shutdown behavior. CSF construction opens
no listener and starts no model provider or simulator.

Add your behavior with ordinary Go functions and routes. The
[complete consumer](../examples/csf-consumer/main.go) adds a human-readable
snapshot endpoint by calling `service.GetSnapshot` with a generated request.
It preserves CSF's generated HTTP and MCP operations alongside that route.
Contracts come from
`github.com/candacelabs/csf/proto/candace/brainspine/v1`; the generated HTTP
client is `csf.NewClient(endpoint, httpClient)`. MCP clients use the upstream
`github.com/modelcontextprotocol/go-sdk/mcp` client and discover the same
operations through `tools/list`.

Configuration is explicit. `WithDashboard(csf.NewDashboard(eventPath))` selects
an existing research event artifact; without it, snapshots report
`no event source configured`. Persistence, external simulations and providers
need their own configured dependencies. Tool discovery does not establish that
those optional capabilities are configured or that a job has succeeded.

`WithWorkbenchThemeDirectory(directory)` selects the directory for the fixed
`workbench-theme.css` filename. The host owns editing that file. Call
`ReloadWorkbenchTheme` with an empty request to load it, then read it with
`GetWorkbenchTheme`. A tool caller cannot select another path or submit CSS in
that request. Invalid UTF-8 or more than 65536 bytes preserves the previous
active theme. This capability supplies theme data; the consumer must separately
serve the browser Workbench bundle when it wants the UI.

Start with the [external consumer instructions](../examples/csf-consumer/README.md)
for a separate Git repository, archive pinning, vendoring and runnable
acceptance tests. Its custom route is host code; widgets belong to gotth-live
inside the host's web layer.
