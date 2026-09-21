# CSF consumer contract

```json
{
  "package": "github.com/candacelabs/csf/csf",
  "role": "in_process_coordination",
  "read_first": [
    "README.md",
    "EXTENDING.md",
    "docs/standalone_onboarding.md",
    "docs/generated/ontology_cgen.md"
  ],
  "example": "../examples/csf-consumer",
  "mcp": {
    "template": ".mcp.json",
    "transport": "streamable_http",
    "default_endpoint": "http://127.0.0.1:14111/mcp",
    "discover": "tools/list",
    "schema_authority": "server_returned_tool_schemas"
  },
  "ownership": {
    "listener": "consumer_host",
    "configuration": "consumer_host",
    "worker_shutdown": "owning_service",
    "database_and_provider": "explicitly_configured_dependencies",
    "widgets": "gotth_live_web_layer"
  },
  "theme": {
    "filename": "workbench-theme.css",
    "directory": "host_configured",
    "reload_tool": "ReloadWorkbenchTheme"
  },
  "evidence": ["source_revision", "archive_sha256", "command", "exit_status", "artifact_location"],
  "release_states": ["candidate_pr", "reviewed_snapshot", "tagged_release", "consumer_verified"],
  "unverified_is_not_success": true
}
```

| Task | Action |
|---|---|
| Integrate in an existing Go host | Mount CSF on its Gin router; use the example's real HTTP/MCP tests. |
| Connect an agent | Copy the MCP template into the consumer harness configuration and select the existing host endpoint. Discovery does not prove optional dependencies are configured. |
| Extend behavior | Add caller-owned Go code first. Change canonical protobuf/API declarations when adding shared operations, then regenerate their projections. |
| Change the public source | Edit the canonical source and propose a new snapshot; do not patch the generated destination in place. A consumer may vendor and adapt its own checkout. |
| Report completion | Include the tested revision, actual command/result, and artifact link. Keep build, hosted checks, deployment and simulation evidence distinct. |

This JSON is an instruction record. Executable request validation comes from
CSF's generated schemas; this file is not a new validated wire protocol.

For a fresh standalone clone, the one-command runtime, private state boundary,
agent-owned Langfuse/OpenSearch configuration tools and generated-documentation
commands are in the [standalone onboarding guide](docs/standalone_onboarding.md).
