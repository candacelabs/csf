"""Generate explicit, unexecuted CARLA/Isaac plans from the owned Scenario proto."""

import argparse
import hashlib
import json
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "training"))
from contract import digest, pb, read_message, to_dict
from driving import validate_scenario


def project(scenario, backend: str) -> dict:
    validate_scenario(scenario)
    if backend not in ("carla", "isaac"):
        raise ValueError(f"unsupported backend: {backend}")
    if scenario.faults:
        raise ValueError("simulator plan profile v1 does not implement fault injection; use the CPU harness")
    if scenario.road_curvature_per_metre != 0:
        raise ValueError("simulator plan profile v1 supports straight reference paths only")
    plan = {
        "format": "brain-spine-simulator-plan-v1",
        "status": "unexecuted_plan",
        "backend": backend,
        "source_scenario": to_dict(scenario),
        "contract_descriptor_sha256": hashlib.sha256(pb.DESCRIPTOR.serialized_pb).hexdigest(),
        "clock": {
            "mode": "single_owner_fixed_step",
            "tick_milliseconds": scenario.tick_milliseconds,
            "steps": scenario.steps,
            "command_deadlines_use_monotonic_runtime_clock_on_hardware": True,
        },
        "units": {"distance": "metre", "time": "second", "angle": "radian"},
        "canonical_frame": "right-handed: x forward, y left, z up",
        "initial_state": {
            "x_metres": 0,
            "y_metres": scenario.initial_lateral_metres,
            "yaw_radians": scenario.initial_heading_radians,
            "speed_mps": 0.6 * scenario.target_speed_mps,
        },
        "reference_path": {
            "kind": "straight", "start_metres": [0, 0, 0], "direction": [1, 0, 0],
            "half_width_metres": scenario.lane_half_width_metres,
        },
        "controller_io": {
            "numeric_profile": 1,
            "transport": "generated RuntimeRequest/RuntimeResponse JSONL",
            "controller_replacement": "between episodes; strictly increasing epoch",
            "feature_order": ["lateral/half_lane_width", "heading_error/0.5rad", "speed_error/10mps", "curvature/0.05_per_metre"],
            "feature_quantization": "multiply1000, nearest ties-to-even, clip[-10000,10000]",
            "steering_conversion": "action.steering/1000 * 0.5 radians",
            "acceleration_conversion": "action.acceleration/1000 * 3 metres/second^2",
        },
        "observations_required": ["pose", "linear_velocity", "simulation_tick", "monotonic_sequence", "active_epoch"],
        "oracles": ["normalized feature/units conformance", "command bounds", "stale/epoch enforcement in spine", "centre-lane departure", "tracking objective"],
        "reproducibility": {
            "seed": str(scenario.seed),
            "record_before_execution": ["simulator_version", "physics_version", "asset_sha256", "adapter_sha256", "hardware", "realized_initial_state"],
            "cross_backend_identical_physics_claimed": False,
        },
        "execution_blockers": [
            "Attach a pinned simulator and a vehicle fixture with a known actuator mapping.",
            "Calibrate steering/acceleration realization and record tolerance bounds.",
            "Run adapter conformance and preserve the observed trace before claiming execution.",
        ],
    }
    if backend == "carla":
        plan["backend_profile"] = {
            "reference_version": "0.9.16",
            "frame_projection": {"x": "x", "y": "-y", "z": "z", "yaw_degrees": "-yaw_radians*180/pi"},
            "settings": {"synchronous_mode": True, "fixed_delta_seconds": scenario.tick_milliseconds / 1000},
            "requirements": [
                "Exactly one client ticks the world; synchronize Traffic Manager if present.",
                "Physics substep duration times maximum substeps must cover fixed_delta_seconds.",
                "Choose a straight lane with matching width, or author a matching OpenDRIVE fixture; reject mismatches.",
                "VehicleControl.steer is not a physical radian value; supply a measured vehicle-specific mapping.",
            ],
        }
    else:
        plan["backend_profile"] = {
            "reference_version": "Isaac Sim 6.0 direct interface",
            "frame_projection": "Author the stage as canonical right-handed Z-up, metresPerUnit=1; verify asset transforms.",
            "settings": {"physics_dt": scenario.tick_milliseconds / 1000, "metres_per_unit": 1.0, "up_axis": "Z"},
            "requirements": [
                "Use one fixed-step physics owner and stamp observations with simulation tick.",
                "Provide a pinned Ackermann vehicle USD, joint names and actuator calibration.",
                "Create a flat lane fixture with the declared width; lane boundaries are reference geometry, not collision claims.",
                "Joint targets and applied acceleration differ; record the controller/drive model used.",
            ],
        }
    return plan


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--scenario", required=True, type=Path)
    parser.add_argument("--backend", required=True, choices=("carla", "isaac"))
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    scenario = read_message(args.scenario, pb.Scenario())
    plan = project(scenario, args.backend)
    plan["source_scenario_sha256"] = digest(args.scenario)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open("x") as result:
        json.dump(plan, result, sort_keys=True, indent=2, allow_nan=False)
        result.write("\n")
    print(f"Wrote unexecuted {args.backend} plan: {args.output}")


if __name__ == "__main__":
    main()
