# Vendored third-party test fixtures

Everything in this directory is **test scaffolding for QA-1's conformance
suite**. None of it is served to a consumer, bundled into the client runtime,
embedded in the Go library, or referenced by anything under `live/`,
`internal/`, `client/` or `examples/`. It exists so that `htmx_test.go` can
assert FR-30, FR-31, FR-32 and G8 against the real HTMX rather than a stub.

## Why it is vendored rather than fetched

Two rules make this the only available shape:

- The project's images are fixed. The bench image is not rebuilt to add an
  asset, and the library image has no node and no package manager at all.
- A test that downloads a dependency at run time is a test that fails when the
  network does, and — worse — a test whose subject changes without a commit.
  The bytes under test are recorded here with a digest so a run either uses
  exactly them or fails saying so.

`htmx_test.go` re-checks the digest on every run (`htmxBundle`), so this file
is provenance that is enforced rather than provenance that is documented.

## `htmx-2.0.10.min.js`

| | |
|---|---|
| **Package** | `htmx.org` |
| **Version** | 2.0.10 |
| **Published** | 2026-04-21T16:29:49Z (npm registry `time` field) |
| **File** | `dist/htmx.min.js` |
| **Size** | 51,238 bytes |
| **SHA-256** | `71ea67185bfa8c98c39d31717c6fce5d852370fcdfd129db4543774d3145c0de` |
| **Licence** | BSD 2-Clause ("Zero Clause"-adjacent; see the htmx repository) |
| **Fetched** | 2026-08-04, inside `dis-gotth-live-bench:latest` |

Retrieved and cross-checked from three independent origins, all of which
produced byte-identical files:

```
https://unpkg.com/htmx.org@2.0.10/dist/htmx.min.js
https://cdn.jsdelivr.net/npm/htmx.org@2.0.10/dist/htmx.min.js
https://registry.npmjs.org/htmx.org/-/htmx.org-2.0.10.tgz   ->  package/dist/htmx.min.js
```

The tarball's own digest matches the integrity value the npm registry records
for 2.0.10:

```
sha512-kdeJe7ZVwaS6QMz/ebBIVtZdpwen6L0OQ5GOhPV9MKBb196TCZeZu4yA7ZIQsaLKv7EpXz+So7KSXNuHXhj7Cw==
```

Reproduce the check:

```bash
docker run --rm dis-gotth-live-bench:latest bash -c '
  curl -fsSL -o /tmp/a.js https://unpkg.com/htmx.org@2.0.10/dist/htmx.min.js
  curl -fsSL -o /tmp/b.js https://cdn.jsdelivr.net/npm/htmx.org@2.0.10/dist/htmx.min.js
  curl -fsSL -o /tmp/t.tgz https://registry.npmjs.org/htmx.org/-/htmx.org-2.0.10.tgz
  mkdir -p /tmp/x && tar -xzf /tmp/t.tgz -C /tmp/x
  sha256sum /tmp/a.js /tmp/b.js /tmp/x/package/dist/htmx.min.js
  node -e "const c=require(\"crypto\"),f=require(\"fs\");
           console.log(\"sha512-\"+c.createHash(\"sha512\").update(f.readFileSync(\"/tmp/t.tgz\")).digest(\"base64\"))"
'
```

2.0.10 was `dist-tags.latest` on the day it was vendored. NFR-9 and FR-74 are
untouched by it: it appears in no `go.mod`, no `package.json` (there is none),
and no image definition.
