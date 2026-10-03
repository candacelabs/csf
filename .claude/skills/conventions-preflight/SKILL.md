---
name: conventions-preflight
description: >-
  A short checklist to run before the first edit of any new feature, service,
  package, CLI verb or schema in this monorepo: find and run the generators
  that own the output (sqlc, oapi-codegen, protoc/buf and Liquid Proto
  generate.sh chains, mockgen, Gazelle), find the house libraries that already
  solve the primitive (guregu/null, pkg/httpserver, pkg/config,
  pkg/eventually, pgmem), and only then hand-write the remainder, ending
  with a generated / reused / hand-written report. Load it whenever you are
  about to create a new Go file, package, service, store, client, handler,
  helper or test double, or are tempted to write a conversion helper, an HTTP
  server, a config loader or a wait loop. It links CS-4, CS-10 and CS-14
  rather than restating them.
---

# Conventions preflight

Operators corrected the same conventions repeatedly across sessions: code
hand-written before the generator ran, helpers re-invented beside a library
that already had them, services that started their own server. 46 such
episodes over 13 sessions were measured on 2026-10-01
(lab entry (monorepo lab entry `2026-10-01-harness-operating-rules`)).
The fix is ordering: look, generate, reuse, then write.

The rules themselves are owned by
[house-code-style](../house-code-style/references/go-rules.md) — CS-4
(generate before you write), CS-10 (a service is a library that never owns the
process) and CS-14 (no hand-rolled twin of a library primitive). The worked
HTTP service shape here is `services/copilot-adapter` (OpenAPI and sqlc inputs,
generated projections, a handwritten boundary).

## 1. List the generators near your change

Search the nearest owning directory and its parents before writing:

```bash
git ls-files <dir> | grep -E '(sqlc\.ya?ml|oapi-codegen.*\.ya?ml|buf\.gen\.yaml|generate\.sh|gen\.sh)$'
git grep -n 'go:generate' -- <dir>
```

| Output you are about to write | Owner |
|---|---|
| Database access, row types | `sqlc.yaml` + migrations (the only schema source) |
| HTTP server/client types | `oapi-codegen.yaml` from the OpenAPI document |
| Wire types, validators | `.proto` + the directory's `generate.sh` (protoc/buf, Liquid Proto via `pkg/liquidproto`) |
| Test doubles | `//go:generate mockgen` |
| `BUILD.bazel` | Gazelle; checked by `tools/check-bazel-metadata.sh` |

Write or change the input (SQL, OpenAPI, proto), run the generator inside the
container ([tools/devcontainer/README.md](../../../tools/devcontainer/README.md)), commit the projection, then
write the handwritten boundary. Never edit generated output. If you believe a
generator cannot express something, show the generator's error or documentation
before hand-writing that part.

## 2. Find the house library before writing a helper

```bash
git grep -n '<concept>' -- pkg go/pkg
```

| Need | Reuse |
|---|---|
| Nullable values | `github.com/guregu/null/v5` via sqlc overrides (CS-14) |
| HTTP engine and mounting | `pkg/httpserver`; a service exposes a constructor and `Register`, the binary owns the server (CS-10) |
| Configuration | `pkg/config` |
| Waiting in tests | `pkg/eventually` (CS-9) |
| Postgres in tests | `pkg/pgmem` with the real migrations |
| Status names, keys, codes | a generated enum or a declared constant (CS-13) |

New files carry the house copyright line used by their neighbours; check one
sibling file and copy it.

## 3. Then hand-write, and report

End the slice's report with three short lists:

- **Generated:** each projection and the command that produced it.
- **Reused:** each library or package you imported instead of writing.
- **Hand-written:** each file, with one clause on why no generator or library
  owns it.

When an operator corrects a convention this checklist missed, add the row to
the owning rule (house-code-style) or to a table above in the same session.
