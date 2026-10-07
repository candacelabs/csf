| What we measured | Result |
|---|---|
| Backtest $\Lambda$ | 16 labeled: 12 +, 4 −; labeled by #371 csf await, #371 gpu-idle, #371 harness-stopped, #371 labeler-awake, #371 long-watch, #371 url-status, #371 workbench-ready, loop without a sleep, session poll loop on a merge, sleep 0 without a loop |
| Backtest split $t$ | 2026-10-05T01:18:12Z: fit on 8, accept on 8 |
| Backtest $\kappa^\star$ | none (no `score` facts) |
| Backtest TP | 5: toolu_017LUfFXJygGamPZDCEn9wJS, toolu_017DuHBBL6DYo4QeT7WhT35F, toolu_011RLFqSozPhqaqxua3by58D, toolu_01N6ucWty2V8skLA2HSmiJGq, toolu_01DnMSdHzvmW7aJBHNZiwvvv |
| Backtest FP | 0 |
| Backtest FN | 0 |
| Backtest lead | 253 s |
| Backtest reproduce | `tools/bazel.sh build //services/ouroboros/miners/hand_rolled_wait:miner && bazel-bin/services/ouroboros/miners/hand_rolled_wait/miner.exe backtest services/ouroboros/miners/hand_rolled_wait/fixtures/labels.tsv 'services/ouroboros/miners/hand_rolled_wait/fixtures/*/events.jsonl'` |
