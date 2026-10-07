# widgets

[Widget](../csf/docs/generated/ontology_cgen.md#term-widget) definitions the [Workbench](../csf/docs/generated/ontology_cgen.md#term-bench) installs while it runs. Each directory here
is one [widget](../csf/docs/generated/ontology_cgen.md#term-widget), as data: the [Workbench](../csf/docs/generated/ontology_cgen.md#term-bench) (the [Workbench control plane](../csf/docs/generated/ontology_cgen.md#term-workbench) in `csf serve`) watches
this directory of the checkout it runs from, checks every definition that
lands, and draws it with no restart; a definition that is removed disappears
the same way. Go cannot load code into a running process and unload it again,
so nothing here is code: the [widget](../csf/docs/generated/ontology_cgen.md#term-widget) SDK's dialect interpreter draws the
definition directly (`pkg/widget`, `InterpretedWidget`).

## A definition

`widgets/<name>/`, where the name is lower-case letters, digits and hyphens:

| File | Holds |
|---|---|
| `definition.widget` | the dialect document, [`pkg/widget/docs/dialect.md`](../pkg/widget/docs/dialect.md) |
| `binding.json` | which stream of the document the [Workbench](../csf/docs/generated/ontology_cgen.md#term-bench) fills, and from which source field each of its event's wire fields reads |
| `fixtures.json` | source documents and the text the [widget](../csf/docs/generated/ontology_cgen.md#term-widget) must show for each |

```json
{"stream": "loopWatch", "fields": [{"wire": "merged", "path": "merges.merged"}]}
```

The stream's own `source` line names the source. A path is the source's JSON
names joined by dots. A flag reads a bool; a count or counter reads an integer,
or a number truncated toward zero; text reads any of the three, a number with
two decimals. A field the source omits or carries as null reads as false, 0 or
"–".

## Sources

| Source | [Schema](../csf/docs/generated/ontology_cgen.md#term-schema) |
|---|---|
| `ouroboros.json` | `ouroboros.Snapshot`, the [mining loop](../csf/docs/generated/ontology_cgen.md#term-ouroboros)'s numbers, in the harness state directory |

A source's schema is its owner's Go type, read by `widget.SchemaOf`, so a
binding cannot name a field the owner does not write.

## The checks

One check, run by the [Workbench](../csf/docs/generated/ontology_cgen.md#term-bench) before it installs a definition, by the
`check_widget` and `propose_widget` operations, and by this directory's spec
(`go test ./widgets/`, and its Bazel target) over every definition here:

1. the document parses and validates, with each finding's line, class and fix;
2. the dialect's refinements hold of its region, placements, identifiers and
   wire names;
3. an interpreted [widget](../csf/docs/generated/ontology_cgen.md#term-widget) draws all of it: no motion, control, signal field,
   toggle or event no stream delivers;
4. every bound field exists in the source's schema with a kind its state
   field reads, and every wire field is bound once;
5. every fixture renders the text it expects.

A definition failing any of them is refused with every reason and never drawn.
Two definitions claiming one region or [widget](../csf/docs/generated/ontology_cgen.md#term-widget) name: the second, in name
order, is refused.

## Making one

The `list_widgets`, `check_widget` and `propose_widget` operations are on the
CSF [service](../csf/docs/generated/ontology_cgen.md#term-service), over [MCP](../csf/docs/generated/ontology_cgen.md#term-mcp) (tool names as written) and HTTP (`POST
/api/widgets/<operation>`), served by `csf serve` when this directory exists.
An [agent](../csf/docs/generated/ontology_cgen.md#term-agent) session makes a [widget](../csf/docs/generated/ontology_cgen.md#term-widget) from the recipe template in
[`services/opsview/recipe`](../services/opsview/recipe): it writes the three
files in its worktree, checks them, takes a screenshot with the browser spec,
and proposes them, which commits the directory and opens the pull request
through the harness. The [widget](../csf/docs/generated/ontology_cgen.md#term-widget) is live once the pull request merges into the
checkout the [Workbench](../csf/docs/generated/ontology_cgen.md#term-bench) runs from.
