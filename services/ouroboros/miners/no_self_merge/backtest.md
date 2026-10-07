| What we measured | Result |
|---|---|
| Backtest $\Lambda$ | 3 labeled: 2 +, 1 −; labeled by #249 orchestrator self-merge, #249 orchestrator with external review |
| Backtest split $t$ | 2026-10-01T14:00:00Z: fit on 2, accept on 1 |
| Backtest $\kappa^\star$ | none (no `score` facts) |
| Backtest TP | 1: 245 |
| Backtest FP | 0 |
| Backtest FN | 0 |
| Backtest lead | 300 s |
| Backtest reproduce | `tools/bazel.sh build //services/ouroboros/miners/no_self_merge:miner && bazel-bin/services/ouroboros/miners/no_self_merge/miner.exe backtest services/ouroboros/miners/no_self_merge/fixtures/labels.tsv 'services/ouroboros/miners/no_self_merge/fixtures/*/events.jsonl'` |
