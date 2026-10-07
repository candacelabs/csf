# Evaluate

**Score every csf build on held-out, rotated past tickets before it goes live.**

The loop's [struggle rate](../../../csf/docs/generated/ontology_cgen.md#term-struggle_rate) is observational: harder work in a week hides an
improvement, and a gate accepted on its [backtest](../../../csf/docs/generated/ontology_cgen.md#term-backtest) says nothing about outcome.
This package fixes the tasks instead. A suite of past tickets, each with the
commit its pull request started from and the merged change as its reference,
is replayed on a build, and the build's score is the [struggle rate](../../../csf/docs/generated/ontology_cgen.md#term-struggle_rate) of those
replays at a fixed budget, with its interval. The suite is hidden from the
[miners](../../../csf/docs/generated/ontology_cgen.md#term-miner) and the loop's corpus readers, it rotates weekly, and the tickets that
evolve the harness are never in it.

## Citing a result

A pull request that claims an improvement cites a recorded score with one
line, exactly as `csf eval show` prints it:

```
eval-suite: build=<sha12> suite=v<N> score=<per 1k> [<low>, <high>] delta=<signed per 1k> vs=<sha12>
```

- `build` is the 12-character commit the scored binary was built from (`csf.BinaryVersion`).
- `suite` is the suite version; scores compare only within one version.
- `score` is struggle episodes per 1,000 tool calls over the suite's replays, each cut at the suite's budget, with the 95% Garwood interval. Lower is better.
- `delta` is `score` minus the score of build `vs` on the same suite version; negative is an improvement.

The [session gate](../../../csf/docs/generated/ontology_cgen.md#term-session_gate) refuses `gh pr ready` when the pull request's verdict, before
or now line claims an improvement and the body carries no citation that
matches a recorded score. `csf eval cite -body FILE` runs the same check by
hand.

## Verbs

| Verb | Does |
|---|---|
| `csf eval suite` | Select or rotate the suite and record it |
| `csf eval score` | Replay the suite on the host's build, sharded, and record the score |
| `csf eval show` | Print a build's score and its citation line |
| `csf eval cite` | [Check](../../../csf/docs/generated/ontology_cgen.md#term-check) a pull request body's improvement claims |
| `csf eval record` | Record replays a burst shard wrote |

`csf upgrade` refuses a build with no score on the current suite, or one that
regressed beyond the bound.
