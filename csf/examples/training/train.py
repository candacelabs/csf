"""Small CEM search over checked controller ASTs, using the real Go runtime."""

from __future__ import annotations

import argparse
from datetime import datetime, timezone
import gzip
import hashlib
import importlib.metadata
import json
from pathlib import Path
import platform
import re
import shlex
import subprocess
import time

import numpy as np

from contract import ROOT, digest, encode, pb, write_message
from driving import DEPARTURE_PENALTY, LOSS_WEIGHTS, fault_scenarios, make_scenario, run_episode
from events import Events
from runtime import Runtime


BASELINE = [-250, -500, 500, 0]
ACCEPTANCE = {
    "version": 1,
    "minimum_validation_improvement": 0.000001,
    "maximum_validation_lane_departures": 0,
    "maximum_validation_fault_mismatches": 0,
    "selection_split": "validation",
    "held_out_used_for_selection": False,
    "loss_weights": LOSS_WEIGHTS,
    "lane_departure_penalty": DEPARTURE_PENALTY,
}


def controller(weights, name: str):
    def scaled(index, value):
        return pb.Expression(
            opcode=pb.OPCODE_SCALE, value=int(value),
            arguments=[pb.Expression(opcode=pb.OPCODE_INPUT, input_index=index)],
        )

    def bounded(expression):
        return pb.Expression(
            opcode=pb.OPCODE_CLAMP, lower=-1000, upper=1000, arguments=[expression]
        )

    steering = pb.Expression(
        opcode=pb.OPCODE_ADD,
        arguments=[
            pb.Expression(opcode=pb.OPCODE_ADD, arguments=[scaled(0, weights[0]), scaled(1, weights[1])]),
            scaled(3, weights[3]),
        ],
    )
    return pb.Controller(
        schema_version=1, name=name,
        steering=bounded(steering), acceleration=bounded(scaled(2, weights[2])),
    )


def dump(path: Path, value) -> None:
    path.write_text(json.dumps(value, sort_keys=True, indent=2, allow_nan=False) + "\n")


def write_trace(path: Path, records: list[dict]) -> None:
    # Empty filename and fixed mtime make compressed replay bytes reproducible.
    with path.open("wb") as raw:
        with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=0) as compressed:
            for record in records:
                compressed.write((json.dumps(record, sort_keys=True, separators=(",", ":")) + "\n").encode())


class Experiment:
    def __init__(self, output: Path, runtime: Runtime, events: Events, scenarios: dict):
        self.output, self.runtime, self.events, self.scenarios = output, runtime, events, scenarios
        self.rows = (output / "candidates.jsonl").open("x", buffering=1)
        self.episodes = (output / "episodes.jsonl").open("x", buffering=1)
        self.episode_step = 0
        self.candidate_count = 0
        self.promotions = 0

    def append_candidate(self, row: dict) -> None:
        self.rows.write(json.dumps(row, sort_keys=True, allow_nan=False) + "\n")
        self.rows.flush()

    def evaluate_split(self, candidate_id: str, split: str, scenarios=None) -> dict:
        self.events.status("evaluating", f"{candidate_id}: {split}")
        summaries = []
        for scenario in (scenarios if scenarios is not None else self.scenarios[split]):
            episode_dir = self.output / "episodes" / candidate_id / split
            episode_dir.mkdir(parents=True, exist_ok=True)
            started = time.monotonic()
            records = []
            try:
                summary, records = run_episode(self.runtime, scenario, on_step=records.append)
            except Exception as error:
                # Preserve even a partial trajectory if the transport fails.
                write_trace(episode_dir / f"{scenario.name}.jsonl.gz", records)
                self.episodes.write(json.dumps({
                    "candidate_id": candidate_id, "split": split,
                    "scenario": scenario.name, "error": f"{type(error).__name__}: {error}",
                    "partial_steps": len(records),
                }, sort_keys=True) + "\n")
                self.episodes.flush()
                raise
            write_trace(episode_dir / f"{scenario.name}.jsonl.gz", records)
            summary.update(candidate_id=candidate_id, split=split)
            summary["wall_seconds"] = time.monotonic() - started
            self.episodes.write(json.dumps(summary, sort_keys=True, allow_nan=False) + "\n")
            self.episodes.flush()
            for metric, value in (
                ("episode_loss", summary["loss"]),
                ("lane_departures", int(summary["lane_departure"])),
                ("runtime_fallbacks", summary["fallback_count"]),
                ("fault_contract_mismatches", summary["fault_mismatches"]),
                ("episode_duration_seconds", summary["wall_seconds"]),
            ):
                self.events.metric(metric, value, self.episode_step, candidate_id, split)
            self.episode_step += 1
            summaries.append(summary)
        return {
            "mean_loss": float(np.mean([row["loss"] for row in summaries])),
            "lane_departures": sum(row["lane_departure"] for row in summaries),
            "fault_mismatches": sum(row["fault_mismatches"] for row in summaries),
            "episodes": len(summaries),
            "fault_ticks_observed": sum(row["fault_ticks_observed"] for row in summaries),
            "fault_ticks_requested": sum(row["fault_ticks_requested"] for row in summaries),
        }

    def evaluate(self, weights, candidate_id: str) -> tuple[dict, object]:
        source = controller(weights, candidate_id)
        directory = self.output / "candidates" / candidate_id
        directory.mkdir(parents=True)
        write_message(directory / "controller.json", source)
        row = {"id": candidate_id, "weights": [int(w) for w in weights]}
        try:
            program = self.runtime.compile(source)
            write_message(directory / "program.json", program)
            row["controller_hash"] = program.controller_hash
            self.runtime.activate(source)
            row["train"] = self.evaluate_split(candidate_id, "train")
            row["validation"] = self.evaluate_split(candidate_id, "validation")
            row["status"] = "evaluated"
            self.candidate_count += 1
            for split in ("train", "validation"):
                self.events.metric(f"candidate_{split}_loss", row[split]["mean_loss"], self.candidate_count, candidate_id, split)
            self.events.metric("candidates_evaluated", self.candidate_count, self.candidate_count)
        except Exception as error:
            row.update(status="failed", error=f"{type(error).__name__}: {error}")
            self.append_candidate(row)
            self.events.status("candidate_failed", row["error"], str(directory.relative_to(self.output)))
            raise
        return row, source

    def canary(self) -> None:
        source = controller(BASELINE, "invalid-schema-canary")
        source.schema_version = 2
        path = self.output / "invalid-controller.json"
        write_message(path, source)
        try:
            self.runtime.compile(source)
        except ValueError as error:
            self.append_candidate({"id": "invalid-schema-canary", "status": "rejected", "expected_rejection": True, "error": str(error)})
            self.events.status("admission_checked", "Invalid schema rejected by the runtime", path.name)
            return
        raise RuntimeError("runtime admitted the deliberately invalid controller")

    def close(self):
        self.rows.close()
        self.episodes.close()


def acceptable(candidate: dict, incumbent: dict) -> bool:
    result, previous = candidate["validation"], incumbent["validation"]
    return (
        result["lane_departures"] <= ACCEPTANCE["maximum_validation_lane_departures"]
        and result["fault_mismatches"] <= ACCEPTANCE["maximum_validation_fault_mismatches"]
        and result["mean_loss"] < previous["mean_loss"] - ACCEPTANCE["minimum_validation_improvement"]
    )


def source_provenance(root: Path) -> dict:
    root = root.resolve()
    receipts = [root / ".candace-source.json", root / "SOURCE_RECEIPT.json"]
    if root.name == "candace":
        receipts.append(root.parent / "SOURCE_RECEIPT.json")
    for receipt_path in receipts:
        if not receipt_path.is_file():
            continue
        receipt = json.loads(receipt_path.read_text())
        public_archive = receipt_path.name == ".candace-source.json"
        revision = receipt.get("source_revision" if public_archive else "source_commit", "")
        if (receipt.get("format_version") != 1 or receipt.get("artifact_kind") != "source_archive"
                or not isinstance(revision, str) or not re.fullmatch(r"[0-9a-f]{40}", revision)):
            raise ValueError("unsupported source archive receipt")
        identity = {"git_commit": revision, "git_dirty": None,
                    "source_archive_receipt_sha256": digest(receipt_path)}
        if public_archive:
            tree = receipt.get("source_tree", "")
            if not isinstance(tree, str) or not re.fullmatch(r"[0-9a-f]{40}", tree):
                raise ValueError("unsupported source archive receipt tree")
            identity["source_tree"] = tree
        return identity
    try:
        checkout = Path(subprocess.check_output(
            ["git", "rev-parse", "--show-toplevel"], cwd=root, text=True,
            stderr=subprocess.PIPE).strip()).resolve()
    except (OSError, subprocess.CalledProcessError) as error:
        raise ValueError("source provenance requires an archive receipt or Git checkout") from error
    if checkout != root and not (root.name == "candace" and checkout == root.parent):
        raise ValueError("source directory is not the Git checkout or its candace export root")
    return {
        "git_commit": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=root, text=True).strip(),
        "git_dirty": bool(subprocess.check_output(["git", "status", "--porcelain"], cwd=root, text=True).strip()),
    }


def train(args) -> dict:
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    # Exclusive manifest creation rejects accidental reuse of an old run.
    with (output / "manifest.json").open("x") as manifest:
        seeds = {
            split: [seed + args.scenario_seed_offset for seed in values]
            for split, values in {
                "train": range(100, 108), "validation": range(200, 206), "held_out": range(300, 308)
            }.items()
        }
        config = {
            "schema_version": 1, "run_id": args.run_id or output.name,
            "started_at": datetime.now(timezone.utc).isoformat(),
            "algorithm": "cross_entropy_method", "optimizer_seed": args.seed,
            "scenario_seed_offset": args.scenario_seed_offset,
            "generations": args.generations, "population": args.population,
            "steps": args.steps, "seed_splits": seeds,
            "acceptance": ACCEPTANCE, "baseline_weights": BASELINE,
            "runtime_command": args.runtime_command,
            "evidence_kind": "local_cpu_highwayenv_through_go_runtime",
            "brain_kind": "numerical_search_no_llm_calls",
            "python_version": platform.python_version(),
            "versions": {name: importlib.metadata.version(name) for name in ("highway-env", "numpy", "protobuf")},
            "uv_lock_sha256": digest(Path(__file__).with_name("uv.lock")),
            "contract_descriptor_sha256": hashlib.sha256(pb.DESCRIPTOR.serialized_pb).hexdigest(),
            "source_sha256": {path.name: digest(path) for path in Path(__file__).parent.glob("*.py")},
            **source_provenance(ROOT.parents[1]),
            "external_spend_usd": 0,
        }
        binary = Path(args.runtime_command[0])
        if binary.is_file():
            config["runtime_binary_sha256"] = digest(binary)
        json.dump(config, manifest, sort_keys=True, indent=2)
        manifest.write("\n")
    scenarios = {split: [make_scenario(seed, args.steps) for seed in split_seeds] for split, split_seeds in seeds.items()}
    for split, values in scenarios.items():
        directory = output / "scenarios" / split
        directory.mkdir(parents=True)
        for scenario in values:
            write_message(directory / f"{scenario.name}.json", scenario)
    events = Events(output / "events.jsonl", config["run_id"])
    events.status("started", "Fixed objective and disjoint seed sets recorded", "manifest.json")
    events.metric("external_spend_usd", 0, 0)
    started = time.monotonic()
    try:
        with Runtime(args.runtime_command, output / "runtime.stderr.log") as runtime:
            experiment = Experiment(output, runtime, events, scenarios)
            try:
                experiment.canary()
                baseline, baseline_source = experiment.evaluate(BASELINE, "baseline")
                baseline["selection"] = "baseline_reference"
                experiment.append_candidate(baseline)
                best, best_source = baseline, baseline_source
                rng = np.random.default_rng(args.seed)
                mean = np.array([-600, -1200, 1400, 500], dtype=float)
                std = np.array([600, 1000, 900, 500], dtype=float)
                for generation in range(args.generations):
                    results = []
                    samples = np.clip(np.rint(rng.normal(mean, std, (args.population, 4))), -6000, 6000).astype(int)
                    for number, weights in enumerate(samples):
                        candidate_id = f"g{generation:02d}-c{number:03d}"
                        row, source = experiment.evaluate(weights, candidate_id)
                        if acceptable(row, best):
                            best, best_source = row, source
                            experiment.promotions += 1
                            row["selection"] = "promoted"
                            write_message(output / "best-controller.json", best_source)
                            events.status("promoted", f"{candidate_id}: validation loss {row['validation']['mean_loss']:.6f}", "best-controller.json")
                        else:
                            row["selection"] = "rejected_by_fixed_acceptance_rule"
                        experiment.append_candidate(row)
                        events.metric("best_validation_loss", best["validation"]["mean_loss"], experiment.candidate_count, best["id"], "validation")
                        events.metric("promotions", experiment.promotions, experiment.candidate_count)
                        results.append(row)
                    elite = sorted(results, key=lambda row: row["train"]["mean_loss"])[:max(2, args.population // 4)]
                    elite_weights = np.asarray([row["weights"] for row in elite], dtype=float)
                    mean = elite_weights.mean(axis=0)
                    std = np.maximum(elite_weights.std(axis=0), [50, 80, 80, 40])
                write_message(output / "best-controller.json", best_source)
                selection = {
                    "candidate_id": best["id"], "controller_hash": best["controller_hash"],
                    "weights": best["weights"], "validation": best["validation"],
                    "baseline_validation": baseline["validation"],
                    "promotions": experiment.promotions,
                    "held_out_evaluation_started": False,
                }
                dump(output / "selection.json", selection)
                # Nothing below this point changes selection, search state or acceptance.
                events.status("held_out", "Selection frozen; evaluating untouched test seeds", "selection.json")
                runtime.activate(baseline_source)
                baseline_test = experiment.evaluate_split("baseline", "held_out")
                if best["id"] == "baseline":
                    # Reuse the identical evaluation rather than overwriting its
                    # retained trajectory with a second session epoch.
                    best_test = baseline_test
                else:
                    runtime.activate(best_source)
                    best_test = experiment.evaluate_split(best["id"], "held_out")
                faults = list(fault_scenarios(400 + args.scenario_seed_offset, args.steps))
                directory = output / "scenarios" / "faults"
                directory.mkdir()
                for scenario in faults:
                    write_message(directory / f"{scenario.name}.json", scenario)
                fault_results = experiment.evaluate_split(best["id"], "faults", faults)
                receipt = {
                    "status": "completed", "evidence_kind": config["evidence_kind"],
                    "selected_candidate": best["id"], "controller_hash": best["controller_hash"],
                    "candidate_count": experiment.candidate_count,
                    "promotions": experiment.promotions,
                    "baseline_validation": baseline["validation"], "best_validation": best["validation"],
                    "baseline_held_out": baseline_test, "best_held_out": best_test,
                    "faults": fault_results, "held_out_used_for_selection": False,
                    "external_spend_usd": 0, "wall_seconds": time.monotonic() - started,
                    "limitations": [
                        "Restricted four-coefficient AST family; numerical CEM, not LLM training.",
                        "Single-vehicle HighwayEnv kinematics; no traffic, perception, tire slip or hardware proof.",
                        "Lane-departure metric checks vehicle centre, not footprint clearance.",
                        "Brain-freeze fixture stops proposals while the existing controller continues; no external model process is killed.",
                        "CARLA and Isaac projections are generated plans, not executed simulator evidence.",
                    ],
                }
                # A plain replay is convenient for viewers; every run also has gzip traces.
                replay_source = output / "episodes" / best["id"] / "held_out" / f"{scenarios['held_out'][0].name}.jsonl.gz"
                (output / "replay.jsonl").write_bytes(gzip.decompress(replay_source.read_bytes()))
                events.status("completed", f"Selected {best['id']}; held-out results recorded without reselection", "receipt.json")
                events.metric("external_spend_usd", 0, experiment.candidate_count)
                experiment.close()
                runtime.close()
                receipt["artifacts"] = {
                    str(path.relative_to(output)): digest(path)
                    for path in sorted(output.rglob("*"))
                    if path.is_file() and path.name != "receipt.json"
                }
                dump(output / "receipt.json", receipt)
                return receipt
            finally:
                experiment.close()
    except Exception as error:
        events.status("failed", f"{type(error).__name__}: {error}", "failure.json")
        dump(output / "failure.json", {"error": f"{type(error).__name__}: {error}", "external_spend_usd": 0})
        raise
    finally:
        events.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--runtime", required=True, help="Shell-style argument list, executed without a shell")
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--run-id")
    parser.add_argument("--seed", type=int, default=601)
    parser.add_argument("--scenario-seed-offset", type=int, default=10000,
                        help="Shift all disjoint scenario seed ranges; default reserves fresh seeds after the retained smoke")
    parser.add_argument("--generations", type=int, default=4)
    parser.add_argument("--population", type=int, default=12)
    parser.add_argument("--steps", type=int, default=120)
    args = parser.parse_args()
    if not 1 <= args.generations <= 100 or not 4 <= args.population <= 256:
        parser.error("generations must be [1,100] and population [4,256]")
    if not 12 <= args.steps <= 150:
        parser.error("steps must be [12,150] for this fixed scenario family")
    if not 0 <= args.scenario_seed_offset <= 2**32 - 1000:
        parser.error("scenario-seed-offset must be in [0,2**32-1000]")
    args.runtime_command = shlex.split(args.runtime)
    if not args.runtime_command:
        parser.error("runtime command must not be empty")
    receipt = train(args)
    print(json.dumps({key: value for key, value in receipt.items() if key != "artifacts"}, sort_keys=True, indent=2))


if __name__ == "__main__":
    main()
