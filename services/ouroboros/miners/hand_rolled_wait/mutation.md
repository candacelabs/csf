| What we measured | Result |
|---|---|
| Mutation score | 23/23 = 1.00; floor 1.00 |
| Mutation floor | the template miner's measured 10/10, `Mutate.template`, checked by its own test |
| Mutation excluded | 3: 0 equivalent, 3 refused |
| Mutation surviving | 0 |
| Mutation reproduce | `tools/bazel.sh build //services/ouroboros/miners/hand_rolled_wait:miner && bazel-bin/services/ouroboros/miners/hand_rolled_wait/miner.exe mutate services/ouroboros/miners/hand_rolled_wait/fixtures/labels.tsv 'services/ouroboros/miners/hand_rolled_wait/fixtures/*/events.jsonl'` |

| Mutant | Kind | Change | Outcome | Changed instances |
|---|---|---|---|---|
| 1 | drop atom | rule 1: drop `loops(C)` | killed: the labeled positives fire and the negatives do not | toolu_01VNdxFmSpxcQrdWywrw8W9f |
| 2 | drop atom | rule 1: drop `sleeps(C)` | killed: the labeled positives fire and the negatives do not | toolu_01VjPYeYyY5emwhiScifkmSK |
| 3 | negate atom | rule 1: `~loops(C)` for `loops(C)` | excluded, refused | — |
| 4 | negate atom | rule 1: `~sleeps(C)` for `sleeps(C)` | killed: the labeled positives fire and the negatives do not | toolu_01RaFPzPqM5cDdyCnbs69ewR, toolu_01VjPYeYyY5emwhiScifkmSK |
| 5 | drop rule | drop rule 1: `hand_rolled_wait(C)` | killed: the labeled positives fire and the negatives do not | toolu_01RaFPzPqM5cDdyCnbs69ewR |
| 6 | drop atom | rule 2: drop `watches(C)` | excluded, refused | — |
| 7 | drop atom | rule 2: drop `~awaits(C)` | killed: the labeled positives fire and the negatives do not | toolu_01RiEwxhRQSibBcVAdZvoQzC |
| 8 | negate atom | rule 2: `~watches(C)` for `watches(C)` | excluded, refused | — |
| 9 | drop rule | drop rule 2: `hand_rolled_wait(C)` | killed: the labeled positives fire and the negatives do not | toolu_01Rx8Uvt6AtMTSLJLmokRfrM, toolu_01Y3qycaG7r1yTALN5Aqj7PM |
| 10 | flip label | flip toolu_01RaFPzPqM5cDdyCnbs69ewR to − | killed: the labeled positives fire and the negatives do not | toolu_01RaFPzPqM5cDdyCnbs69ewR |
| 11 | flip label | flip toolu_01VjPYeYyY5emwhiScifkmSK to + | killed: the labeled positives fire and the negatives do not | toolu_01VjPYeYyY5emwhiScifkmSK |
| 12 | flip label | flip toolu_01KKjaEt59dVYerKRHUW1MNV to − | killed: the labeled positives fire and the negatives do not | toolu_01KKjaEt59dVYerKRHUW1MNV |
| 13 | flip label | flip toolu_0116J9yGkHb6TNA4ntNZRrdU to − | killed: the labeled positives fire and the negatives do not | toolu_0116J9yGkHb6TNA4ntNZRrdU |
| 14 | flip label | flip toolu_01Tz3RsoyM71wXghjXZQbETy to − | killed: the labeled positives fire and the negatives do not | toolu_01Tz3RsoyM71wXghjXZQbETy |
| 15 | flip label | flip toolu_01Rx8Uvt6AtMTSLJLmokRfrM to − | killed: the labeled positives fire and the negatives do not | toolu_01Rx8Uvt6AtMTSLJLmokRfrM |
| 16 | flip label | flip toolu_01Y3qycaG7r1yTALN5Aqj7PM to − | killed: the labeled positives fire and the negatives do not | toolu_01Y3qycaG7r1yTALN5Aqj7PM |
| 17 | flip label | flip toolu_01RJ72Kjrra29jyxCr3hEYEL to − | killed: the labeled positives fire and the negatives do not | toolu_01RJ72Kjrra29jyxCr3hEYEL |
| 18 | flip label | flip toolu_017LUfFXJygGamPZDCEn9wJS to − | killed: the labeled positives fire and the negatives do not | toolu_017LUfFXJygGamPZDCEn9wJS |
| 19 | flip label | flip toolu_01VNdxFmSpxcQrdWywrw8W9f to + | killed: the labeled positives fire and the negatives do not | toolu_01VNdxFmSpxcQrdWywrw8W9f |
| 20 | flip label | flip toolu_017DuHBBL6DYo4QeT7WhT35F to − | killed: the labeled positives fire and the negatives do not | toolu_017DuHBBL6DYo4QeT7WhT35F |
| 21 | flip label | flip toolu_011RLFqSozPhqaqxua3by58D to − | killed: the labeled positives fire and the negatives do not | toolu_011RLFqSozPhqaqxua3by58D |
| 22 | flip label | flip toolu_01N6ucWty2V8skLA2HSmiJGq to − | killed: the labeled positives fire and the negatives do not | toolu_01N6ucWty2V8skLA2HSmiJGq |
| 23 | flip label | flip toolu_01DnMSdHzvmW7aJBHNZiwvvv to − | killed: the labeled positives fire and the negatives do not | toolu_01DnMSdHzvmW7aJBHNZiwvvv |
| 24 | flip label | flip toolu_01RiEwxhRQSibBcVAdZvoQzC to + | killed: the labeled positives fire and the negatives do not | toolu_01RiEwxhRQSibBcVAdZvoQzC |
| 25 | flip label | flip toolu_01XQ56jif6tye7ixtkZTwabX to + | killed: the labeled positives fire and the negatives do not | toolu_01XQ56jif6tye7ixtkZTwabX |
| 26 | move split | move the split: toolu_01RJ72Kjrra29jyxCr3hEYEL and toolu_017LUfFXJygGamPZDCEn9wJS exchange starts | killed: backtest.md is the generated block | toolu_017LUfFXJygGamPZDCEn9wJS, toolu_01RJ72Kjrra29jyxCr3hEYEL |
