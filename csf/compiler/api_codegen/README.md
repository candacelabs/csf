# Native API projection

This OCaml compiler projects the owned protobuf RPC descriptor and upstream
OpenAPI document into CSF's Go client, HTTP/MCP registration and CLI catalog.
Protobuf `go_package` owns each message's Go import, including operations from
different packages. Only the supported empty-input GET and whole-message POST
routes are accepted. MCP schemas retain only reachable definitions.

```sh
bash tools/bazel.sh test //csf/compiler/api_codegen:tests
bash csf/tools/codegen/generate.sh write
bash csf/tools/codegen/generate.sh check
```

Bazel owns the pinned native build. The existing pinned protoc/OpenAPI plugins
produce descriptors and transport schemas; pinned Buf converts a descriptor to
JSON using the standard protobuf descriptor schema. OCaml owns validation and
projection. There is no Python generator or host protobuf dependency. Generated
Python bindings remain outputs for Python consumers, not build orchestration.

The adapter declaration stays at `csf/tools/codegen/api/adapter.proto`; wire
messages stay under `proto/`. Generation drift checks compare every committed
projection. No schema, operation or route is maintained independently here.
