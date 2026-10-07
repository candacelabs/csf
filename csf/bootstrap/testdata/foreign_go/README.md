# foreign_go

A hermetic fixture repository for the bootstrap loop's acceptance: a foreign Go
repository whose violation shape the loop must drive to a fixpoint locally, with
no job, no network and no database.

The per-directory checker that computes a directory's chief counts from the tree
is not landed yet, so the loop's measurer reads the marker convention the merge
quality gate uses: a top-level directory's count for a chief row is the presence
of a file named after the row's id inside it (`services/bootstrap`, `Measure`).
Each directory below therefore carries one marker, one chief violation each:

| directory  | marker                          | chief row                        |
|------------|---------------------------------|----------------------------------|
| `legacy`   | `fanout_at_most_16`             | `fanout_gt_16`                   |
| `tooling`  | `cli_purpose_declared`          | `undeclared_cli`                 |
| `grammar`  | `grammar_decision_fits_one_pick`| `free_text_grammar`              |
| `vendored` | `every_directory_declared`      | `undeclared_dirs`                |

The loop test copies this tree, clears the four markers, and asserts it reaches
`chief_violations(0)` and emits an archive. The committed fixture is never
written to.
