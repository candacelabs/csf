# Make a widget that shows @NEED@

Make one widget for the Workbench, named `@NAME@`, that shows @NEED@. A widget
here is data, not code: a definition directory `widgets/@NAME@/` with three
files, which the running Workbench checks and draws the moment it lands on its
checkout, with no restart. You write those three files and nothing else.

Your assignment is `@ASSIGNMENT_ID@`; every tool call below that takes an
`assignment_id` takes that one.

## Read first

1. `widgets/README.md`: the three files, the sources a widget may bind and the
   checks.
2. `widgets/merge-train/`: a complete definition that passes every check.
3. `pkg/widget/docs/dialect.md`, sections 5 to 7: the dialect's blocks. Read
   `pkg/widget/docs/errors.md` only for a class a check names.

## Steps

1. Call `mcp__csf__list_widgets`. It lists the installed widgets and the
   sources a stream may bind. Pick the source that carries what the need
   asks for. The source's schema is its owner's Go type, so the fields you
   may bind are that type's JSON names joined by dots: `fixers.budget_usd`.
   A check that refuses a path lists every field the source has.
2. Write `widgets/@NAME@/definition.widget`, `binding.json` and
   `fixtures.json`. Give at least two fixtures that render differently, with
   the text each must show.
3. Call `mcp__csf__check_widget` with `{"name": "@NAME@", "assignment_id":
   "@ASSIGNMENT_ID@"}`. Repair every reason it gives, each at the line it
   names, with the fix it names, and check again until `installable` is
   true. The Workbench runs this same check before it installs a widget.
4. Take the screenshot: the browser spec installs every definition in
   `widgets/` into a live Workbench page and writes the page to the file
   `OPSVIEW_SCREENSHOT` names. Run, as one command from the worktree root:

   ```
   docker run --rm --user 1000:1000 -v @WORKTREE@:/workspace -w /workspace -v @RUN_DIRECTORY@:/out -v @GOMODCACHE@:/gomod -e GOMODCACHE=/gomod -e GOFLAGS=-mod=mod -e GOCACHE=/tmp/gocache -e HOME=/tmp -e OPSVIEW_SCREENSHOT=/out/widget.png dis-gotth-live-bench:latest go test -count=1 ./services/opsview/ -run TestOpsView -args -ginkgo.label-filter=browser
   ```

   Then Read `@RUN_DIRECTORY@/widget.png` and confirm your widget is on the
   page and shows what the need asks for. If it does not, change the
   definition and repeat from step 3.
5. Call `mcp__csf__propose_widget` with `{"name": "@NAME@", "assignment_id":
   "@ASSIGNMENT_ID@", "need": "show @NEED@"}`. It runs the check again,
   commits `widgets/@NAME@/` in your worktree and opens your pull request
   through the harness. Do not commit with git.

## Acceptance

- `check_widget` reports `installable: true` for `@NAME@`.
- The screenshot shows the widget drawn on the Workbench page.
- `propose_widget` returned the commit and the pull request URL.

## Stop

Stop after step 5. Your final message is three lines: the pull request URL,
the commit, and the screenshot's path. If a tool is missing or refuses you,
say which and what it answered, and stop.
