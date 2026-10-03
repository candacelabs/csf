| What we measured | Result |
|---|---|
| Backtest $\Lambda$ | 3 labeled: 2 +, 1 −; labeled by #376 13-minute flag, #376 named run |
| Backtest split $t$ | 2026-10-02T18:04:45Z: fit on 2, accept on 1 |
| Backtest $\kappa^\star$ | 468 (argmax precision on $\Lambda_{\le t}$ s.t. FN = 0; 3 candidates from `score`) |
| Backtest TP | 1: 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60 |
| Backtest FP | 0 |
| Backtest FN | 0 |
| Backtest lead | 304 s |
| Backtest reproduce | `tools/bazel.sh build //services/ouroboros/miners/_template:miner && bazel-bin/services/ouroboros/miners/_template/miner.exe backtest services/ouroboros/miners/_template/fixtures/labels.tsv 'services/ouroboros/miners/_template/fixtures/*/events.jsonl'` |
