| What we measured | Result |
|---|---|
| Backtest $\Lambda$ | 3 labeled: 2 +, 1 −; labeled by invalid arm (attach), invalid arm (new), valid arms (attach_warm, attach_topic) |
| Backtest split $t$ | 2026-10-03T02:00:00Z: fit on 2, accept on 1 |
| Backtest $\kappa^\star$ | none (no `score` facts) |
| Backtest TP | 1: 3321228e-c5d5-4563-979f-fd619237838e |
| Backtest FP | 0 |
| Backtest FN | 0 |
| Backtest lead | none flagged |
| Backtest reproduce | `tools/bazel.sh build //services/ouroboros/miners/router_v1_two_arms:miner && bazel-bin/services/ouroboros/miners/router_v1_two_arms/miner.exe backtest services/ouroboros/miners/router_v1_two_arms/fixtures/labels.tsv 'services/ouroboros/miners/router_v1_two_arms/fixtures/*/events.jsonl'` |
