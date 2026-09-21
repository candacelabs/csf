# CPU driving experiment

This experiment runs an actual HighwayEnv kinematic vehicle through the Go
CSF runtime. Cross-entropy search tunes four integer coefficients in two
small checked expression trees: steering uses lateral error, heading error and
curvature; acceleration uses speed error. It is numerical controller search,
not neural-network training or an LLM call.

The protobuf at `proto/candace/brainspine/v1/brainspine.proto` owns all runtime
messages, scenarios and measurement events. Source archives include the generated
Python bindings; regenerate them in the monorepo after changing the schema.
Python imports `csf/tools/codegen/generated/python`; there is no handwritten
DTO copy. A public archive records its `.candace-source.json` hash, source
revision and selected Git tree. A selective archive instead reads
`SOURCE_RECEIPT.json` at the archive root, above its `candace/` directory. Both
record `git_dirty: null` because Git state is unavailable. Per-file source
hashes record the actual training inputs. Checkout runs retain Git revision and
dirty state. Missing or invalid provenance fails the run; an unrelated enclosing
Git repository is not used as the source identity. These markers identify the
packaged source, while per-file hashes record local changes to training inputs.

## Run

Start the live dashboard before the main training run. It consumes the flushed
`events.jsonl` stream, whose metric definitions precede measurements. Register
the intended output directory with the dashboard, then use a fresh run directory:

```sh
uv sync --locked
uv run --locked python train.py \
  --runtime '/absolute/path/to/csf' \
  --output /absolute/path/to/new-run
```

The default run evaluates a modest fixed feedback baseline and 48 CEM proposals
over eight training and six validation seeds. Each nominal episode lasts at most
120 ticks of 100 ms. A lane departure ends the episode and adds a fixed penalty.
Every proposal is compiled by the runtime, activated between episodes, and given
a fresh epoch on each reset. An invalid-schema canary must be rejected first.

For a short integration smoke, use `--generations 1 --population 4 --steps 20
--scenario-seed-offset 20000`.
That is a pipeline check, not the full evaluation. Both commands call only local
CPU software and record external spending as zero.

## Fixed evaluation

- Training seeds are 100–107; validation seeds are 200–205; held-out seeds are
  300–307, all shifted by `--scenario-seed-offset` (default 10000). The optimizer
  has a separate seed, default 601. Offset 0 was consumed by the retained short
  smoke; the default reserves fresh seeds for a later full run.
- The objective is the episode mean of normalized lateral error squared,
  `0.25 * heading_error²`, `0.5 * speed_error²` and
  `0.005 * normalized_steering²`, plus 50 on centre-lane departure. Physical
  normalizations are the protobuf numeric profile.
- CEM updates its distribution using training loss. Promotion requires zero
  validation departures, zero fault-contract mismatches, and an improvement
  exceeding `0.000001` in mean validation loss. These rules never change during a
  run. The baseline is retained as a reference even when it fails those rules.
- Selection is written before held-out evaluation. Held-out results never feed
  the optimizer or promotion. Reusing these test results to design a subsequent
  search would make these seeds development data; reserve new test seeds then.
- Initial speed is fixed at 60% of target speed; all other realized scenario
  values are saved. Position axes are right-handed: x forward, y left, z up.
  Heading and lateral errors are vehicle state minus lane reference. Input
  conversion rounds nearest with ties-to-even and clips to `[-10000,10000]`.
  Expression SCALE uses the separately specified truncation toward zero.

The three post-selection fault scenarios cover stale observations, wrong epoch,
and absent brain proposals. The freeze case checks continued execution of an
already activated controller: no external model process is killed. Inspect both
`fault_ticks_observed` and `fault_ticks_requested`; an early departure can leave
a planned fault unexercised. A fallback command is not itself a proof that the
vehicle stays in the lane.

## Evidence and replay

`manifest.json` freezes configuration, policy, seeds, dependency versions,
contract/source hashes and runtime binary hash when a binary path is supplied.
`candidates.jsonl` retains accepted, rejected and failed candidates; candidate
folders contain source controllers and compiled programs. `episodes.jsonl`
contains metrics; every episode has a compressed trajectory, including failures.
`selection.json`, `best-controller.json`, `replay.jsonl`, `receipt.json` and
`events.jsonl` connect the selected controller to its evidence. Runtime transport
errors retain the partial trajectory and stop the run.

```sh
uv run --locked python replay.py \
  --runtime '/absolute/path/to/csf' \
  --controller /absolute/path/to/run/best-controller.json \
  --scenario /absolute/path/to/run/scenarios/held_out/lane-tracking-10300.json \
  --expected /absolute/path/to/run/replay.jsonl \
  --output /absolute/path/to/replay-receipt.json
uv run --locked pytest -q
```

Replay compares physical states with absolute tolerance `1e-12`, normalized
features and actions. Absolute epoch values differ between runtime sessions;
their behavior must still agree. Retain the pinned environment when replaying:
this comparison is not a cross-platform floating-point determinism theorem.

## Bounds

HighwayEnv 1.12.0 is MIT-licensed; the full transitive environment is in
`uv.lock`. We use its `Vehicle`, `Road`, `StraightLane` and `CircularLane` directly,
without rendering or a GPU. This is a single-vehicle kinematic test with no
traffic, perception, tire dynamics, reward learning or hardware actuation. A
departure concerns the vehicle centre, not footprint clearance. The finite seed
set and restricted AST family do not establish physical safety or general driving
ability. The compiler proof does not prove the simulator's floating-point physics.

Primary dependency sources: [HighwayEnv release](https://pypi.org/project/highway-env/1.12.0/)
and [vehicle model](https://github.com/Farama-Foundation/HighwayEnv/blob/main/highway_env/vehicle/kinematics.py).
