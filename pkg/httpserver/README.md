# Shared HTTP server

This package owns the repository's Gin engine and HTTP server lifecycle.
Services register routes on a caller-owned engine; the composing binary owns
the listener, shutdown and deployment.

## OpenAPI request validation

[`ValidateOpenAPIRequests`](openapi.go) builds a Gin middleware from an
`openapi3.T`, `openapi3filter.Options` and a validation-error callback. It
returns an error if the contract cannot initialize the schema router. Gin
continues to route HTTP requests; kin-openapi's `routers/legacy` resolves the
schema operation and `openapi3filter` validates the request. No Gorilla router
or `oapi-codegen/gin-middleware` is used.

Install the middleware only on routes declared by that contract. The
maintained consumers show both mounting patterns:

- [Copilot Adapter](../../services/copilot-adapter/service.go) passes it as
  middleware to the generated Gin registration.
- [Bookmarks](../../examples/agent-openapi-sqlc/internal/bookmarks/routes.go)
  attaches it to the group that owns the generated routes, with a custom URI
  format validator.

The caller supplies a non-nil error callback and owns its response body and
authentication policy. Undeclared paths produce 404; other route lookup or
request-validation failures produce 400. The adapter aborts the Gin chain
after the callback. Successful validation preserves the request body for the
handler, passes the request context to validation, and keeps Gin's complete
path parameter values, including decoded trailing slashes. Treat the contract
and validation options as immutable after registration.

The [regression suite](openapi_test.go) covers body preservation, unrelated
routes, path/query/body validation, encoded path values, format options,
request-context cancellation and invalid contracts. From the private `go/`
module, run:

```bash
go test -mod=readonly -race ./pkg/httpserver ./services/copilot-adapter/...
```

The repository-wide [dependency gate](../../tools/gorilla_mux_lint/README.md)
also checks manifests and imports. See its documentation before dependency
maintenance: upstream kin-openapi still declares mux for its own tests.
