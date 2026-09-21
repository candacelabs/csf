"""Re-run a retained controller/scenario and compare observable trajectories."""

import argparse
import gzip
import hashlib
import json
from pathlib import Path
import shlex

import numpy as np

from contract import digest, pb, read_message
from driving import run_episode
from runtime import Runtime


def compare_records(actual, expected, atol: float = 1e-12) -> None:
    if len(actual) != len(expected):
        raise ValueError(f"trajectory length differs: {len(actual)} != {len(expected)}")
    for current, old in zip(actual, expected, strict=True):
        if current["tick"] != old["tick"]:
            raise ValueError("replay tick differs")
        for key in ("state_before", "state_after"):
            for field, value in current[key].items():
                if not np.isclose(value, old[key][field], rtol=0, atol=atol):
                    raise ValueError(f"physical trajectory differs at tick {current['tick']}: {field}")
        for field in ("steering", "acceleration", "fallback", "reason"):
            default = "" if field == "reason" else 0
            if current["action"].get(field, default) != old["action"].get(field, default):
                raise ValueError(f"action differs at tick {current['tick']}: {field}")
        if current["observation"]["features"] != old["observation"]["features"]:
            raise ValueError(f"normalized observation differs at tick {current['tick']}")
    # Epochs are deliberately relative to a new runtime session. Their absolute
    # values differ; action/fault behavior must still match.


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--runtime", required=True)
    parser.add_argument("--controller", required=True, type=Path)
    parser.add_argument("--scenario", required=True, type=Path)
    parser.add_argument("--expected", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    source = read_message(args.controller, pb.Controller())
    scenario = read_message(args.scenario, pb.Scenario())
    raw = args.expected.read_bytes()
    if args.expected.suffix == ".gz":
        raw = gzip.decompress(raw)
    expected = [json.loads(line) for line in raw.splitlines()]
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("x") as receipt:
        with Runtime(shlex.split(args.runtime), args.output.with_suffix(".stderr.log")) as runtime:
            program = runtime.compile(source)
            runtime.activate(source)
            summary, actual = run_episode(runtime, scenario)
            compare_records(actual, expected)
        result = {
            "status": "matched", "controller_hash": program.controller_hash,
            "controller_sha256": digest(args.controller),
            "scenario_sha256": digest(args.scenario),
            "expected_sha256": hashlib.sha256(raw).hexdigest(),
            "compared_fields": ["physical states", "normalized features", "actions", "fallback reasons"],
            "absolute_epoch_compared": False, "absolute_tolerance": 1e-12,
            "episode": summary,
        }
        json.dump(result, receipt, sort_keys=True, indent=2)
        receipt.write("\n")
    print(json.dumps(result, sort_keys=True))


if __name__ == "__main__":
    main()
