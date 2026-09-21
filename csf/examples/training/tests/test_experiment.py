import importlib.util
import json
from pathlib import Path
import sys
import subprocess

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from contract import digest, pb
from driving import DrivingPlant, fault_scenarios, make_scenario, validate_scenario
from events import Events
from replay import compare_records
from train import acceptable, source_provenance

spec = importlib.util.spec_from_file_location(
    "scenario_project", Path(__file__).resolve().parents[2] / "scenarios" / "project.py"
)
projection = importlib.util.module_from_spec(spec)
spec.loader.exec_module(projection)


def test_extracted_training_uses_archive_identity_without_git(tmp_path, monkeypatch):
    receipt = tmp_path / "SOURCE_RECEIPT.json"
    receipt.write_text(json.dumps({"format_version": 1, "artifact_kind": "source_archive",
                                   "source_commit": "a" * 40}))
    monkeypatch.setenv("PATH", "")
    identity = source_provenance(tmp_path)
    assert identity["git_commit"] == json.loads(receipt.read_text())["source_commit"]
    assert identity["source_archive_receipt_sha256"] == digest(receipt)
    assert identity["git_dirty"] is None
    receipt.write_text(json.dumps({"format_version": 1, "artifact_kind": "source_archive",
                                   "source_commit": "not-a-commit"}))
    with pytest.raises(ValueError, match="source archive receipt"):
        source_provenance(tmp_path)


def test_checkout_training_preserves_git_revision_and_dirty_state(tmp_path):
    def git(*args):
        return subprocess.check_output(["git", "-C", str(tmp_path), *args], text=True).strip()
    git("init", "--quiet")
    git("-c", "user.name=Test", "-c", "user.email=test@example.invalid",
        "commit", "--quiet", "--allow-empty", "-m", "fixture")
    identity = source_provenance(tmp_path)
    assert identity == {"git_commit": git("rev-parse", "HEAD"), "git_dirty": False}
    (tmp_path / "changed.txt").write_text("uncommitted input")
    assert source_provenance(tmp_path) == {**identity, "git_dirty": True}


def test_public_archive_identity_includes_selected_tree_without_git(tmp_path, monkeypatch):
    marker = tmp_path / ".candace-source.json"
    marker.write_text(json.dumps({"format_version": 1, "artifact_kind": "source_archive",
                                 "source_revision": "a" * 40, "source_tree": "b" * 40}))
    monkeypatch.setenv("PATH", "")
    assert source_provenance(tmp_path) == {
        "git_commit": "a" * 40, "git_dirty": None, "source_tree": "b" * 40,
        "source_archive_receipt_sha256": digest(marker),
    }
    marker.write_text(json.dumps({"format_version": 1, "artifact_kind": "source_archive",
                                 "source_revision": "a" * 40, "source_tree": "unknown"}))
    with pytest.raises(ValueError, match="source archive receipt tree"):
        source_provenance(tmp_path)


def test_selective_archive_identity_is_found_above_candace_module(tmp_path, monkeypatch):
    module = tmp_path / "candace"
    module.mkdir()
    receipt = tmp_path / "SOURCE_RECEIPT.json"
    receipt.write_text(json.dumps({"format_version": 1, "artifact_kind": "source_archive",
                                  "source_commit": "c" * 40}))
    monkeypatch.setenv("PATH", "")
    assert source_provenance(module) == {
        "git_commit": "c" * 40, "git_dirty": None,
        "source_archive_receipt_sha256": digest(receipt),
    }


def test_missing_provenance_cannot_be_reported_as_success(tmp_path, monkeypatch):
    monkeypatch.setenv("PATH", "")
    with pytest.raises(ValueError, match="archive receipt or Git checkout"):
        source_provenance(tmp_path)


def test_unrelated_enclosing_checkout_is_not_archive_provenance(tmp_path):
    subprocess.run(["git", "init", "--quiet", str(tmp_path)], check=True)
    extracted = tmp_path / "downloaded-release"
    extracted.mkdir()
    with pytest.raises(ValueError, match="not the Git checkout"):
        source_provenance(extracted)


@pytest.mark.parametrize("curvature", [-0.005, 0, 0.005])
def test_physical_frame_and_fixed_point_features(curvature):
    scenario = make_scenario(100)
    scenario.initial_lateral_metres = 0.4
    scenario.initial_heading_radians = 0.05
    scenario.road_curvature_per_metre = curvature
    plant = DrivingPlant(scenario)
    observation = plant.observe(7, 3)
    assert list(observation.features) == [200, 100, round(scenario.target_speed_mps * 40), round(curvature * 20000)]
    assert observation.sequence == 8
    assert observation.epoch == 3
    old_speed = plant.vehicle.speed
    plant.step(pb.Action(steering=0, acceleration=1000))
    assert plant.vehicle.speed == pytest.approx(old_speed + 0.3)


def test_semantically_unsupported_scenario_is_rejected():
    scenario = make_scenario(100)
    scenario.target_speed_mps = float("nan")
    with pytest.raises(ValueError, match="finite"):
        validate_scenario(scenario)
    scenario = make_scenario(100)
    scenario.faults.add(kind=999, start_tick=5, duration_ticks=1)
    with pytest.raises(ValueError, match="unsupported fault"):
        validate_scenario(scenario)


@pytest.mark.parametrize("backend", ["carla", "isaac"])
def test_projection_preserves_intent_and_refuses_semantic_loss(backend):
    scenario = make_scenario(100)
    scenario.road_curvature_per_metre = 0
    plan = projection.project(scenario, backend)
    assert plan["status"] == "unexecuted_plan"
    assert plan["clock"]["tick_milliseconds"] == scenario.tick_milliseconds
    assert plan["initial_state"]["y_metres"] == scenario.initial_lateral_metres
    assert plan["execution_blockers"]
    scenario.road_curvature_per_metre = 0.005
    with pytest.raises(ValueError, match="straight"):
        projection.project(scenario, backend)
    scenario.road_curvature_per_metre = 0
    scenario.faults.add(kind=pb.FAULT_KIND_WRONG_EPOCH, start_tick=3, duration_ticks=2)
    with pytest.raises(ValueError, match="fault injection"):
        projection.project(scenario, backend)


def test_promotion_cannot_buy_better_loss_with_lane_departures():
    incumbent = {"validation": {"mean_loss": 10.0, "lane_departures": 0, "fault_mismatches": 0}}
    candidate = {"validation": {"mean_loss": 1.0, "lane_departures": 1, "fault_mismatches": 0}}
    assert not acceptable(candidate, incumbent)
    candidate["validation"]["lane_departures"] = 0
    assert acceptable(candidate, incumbent)
    candidate["validation"]["fault_mismatches"] = 1
    assert not acceptable(candidate, incumbent)


def test_fault_suite_is_valid_and_retains_each_case():
    scenarios = list(fault_scenarios(400))
    assert len({scenario.faults[0].kind for scenario in scenarios}) == 3
    for scenario in scenarios:
        validate_scenario(scenario)


def test_dashboard_events_declare_metrics_and_reject_nonfinite_values(tmp_path):
    path = tmp_path / "events.jsonl"
    events = Events(path, "test")
    events.metric("episode_loss", 1.2, 1, "candidate", "validation")
    with pytest.raises(ValueError, match="non-finite"):
        events.metric("episode_loss", float("nan"), 2)
    events.close()
    records = [json.loads(line) for line in path.read_text().splitlines()]
    declarations = {row["definition"]["name"] for row in records[:-1]}
    assert records[-1]["measurement"]["metric"] in declarations
    assert records[-1]["measurement"]["run_id"] == "test"


def test_replay_detects_physical_divergence():
    expected = [{
        "tick": 0, "state_before": {"x": 0.0}, "state_after": {"x": 1.0},
        "action": {}, "observation": {"features": ["0"]},
    }]
    actual = json.loads(json.dumps(expected))
    compare_records(actual, expected)
    actual[0]["state_after"]["x"] += 0.01
    with pytest.raises(ValueError, match="physical trajectory"):
        compare_records(actual, expected)
