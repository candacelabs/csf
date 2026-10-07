| What we measured | Result |
|---|---|
| Mutation score | 10/10 = 1.00; floor 1.00 |
| Mutation floor | the template miner's measured 10/10, `Mutate.template`, checked by its own test |
| Mutation excluded | 6: 1 equivalent, 5 refused |
| Mutation surviving | 0 |
| Mutation reproduce | `tools/bazel.sh build //services/ouroboros/miners/_template:miner && bazel-bin/services/ouroboros/miners/_template/miner.exe mutate services/ouroboros/miners/_template/fixtures/labels.tsv 'services/ouroboros/miners/_template/fixtures/*/events.jsonl'` |

| Mutant | Kind | Change | Outcome | Changed instances |
|---|---|---|---|---|
| 1 | drop atom | rule 1: drop `gated(R)` | excluded, equivalent | — |
| 2 | drop atom | rule 1: drop `score(R, S)` | excluded, refused | — |
| 3 | drop atom | rule 1: drop `knee(K)` | excluded, refused | — |
| 4 | drop atom | rule 1: drop `gt(S, K)` | killed: the labeled positives fire and the negatives do not | 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e31, 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e59, 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60 |
| 5 | swap comparison | rule 1: `ge(S, K)` for `gt(S, K)` | killed: backtest.md is the generated block | 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e31, 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e59, 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60 |
| 6 | write knee | rule 1: write 468 for `knee(K)` | killed: the knee binds | 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e31, 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60 |
| 7 | write knee | rule 1: write 1259 for `knee(K)` | killed: the labeled positives fire and the negatives do not | 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e31, 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60 |
| 8 | write knee | rule 1: write 3089 for `knee(K)` | killed: the labeled positives fire and the negatives do not | 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e31, 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60 |
| 9 | negate atom | rule 1: `~gated(R)` for `gated(R)` | excluded, refused | — |
| 10 | negate atom | rule 1: `~score(R, S)` for `score(R, S)` | excluded, refused | — |
| 11 | negate atom | rule 1: `~knee(K)` for `knee(K)` | excluded, refused | — |
| 12 | drop rule | drop rule 1: `invisible(R)` | killed: the labeled positives fire and the negatives do not | 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e31, 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60 |
| 13 | flip label | flip 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e31 to − | killed: the labeled positives fire and the negatives do not | 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e31 |
| 14 | flip label | flip 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e59 to + | killed: the labeled positives fire and the negatives do not | 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e59 |
| 15 | flip label | flip 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60 to − | killed: the labeled positives fire and the negatives do not | 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60 |
| 16 | move split | move the split: 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e59 and 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60 exchange starts | killed: the walk-forward backtest has no false negative | 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e59, 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60 |
