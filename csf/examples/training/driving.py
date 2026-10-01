"""HighwayEnv plant and the explicit physical conversion for numeric profile v1."""

from __future__ import annotations

import math

import numpy as np
from highway_env.road.lane import CircularLane, StraightLane
from highway_env.road.road import Road, RoadNetwork
from highway_env.vehicle.kinematics import Vehicle

from contract import pb, to_dict

LOSS_WEIGHTS = {"lateral": 1.0, "heading": 0.25, "speed": 0.5, "steering": 0.005}
DEPARTURE_PENALTY = 50.0


def validate_scenario(scenario) -> None:
    if scenario.schema_version != 1 or not scenario.name:
        raise ValueError("scenario requires schema_version=1 and a name")
    if not 1 <= scenario.steps <= 10_000:
        raise ValueError("scenario steps must be in [1,10000]")
    if not 1 <= scenario.tick_milliseconds <= 1000:
        raise ValueError("tick_milliseconds must be in [1,1000]")
    values = (
        scenario.target_speed_mps,
        scenario.lane_half_width_metres,
        scenario.initial_lateral_metres,
        scenario.initial_heading_radians,
        scenario.road_curvature_per_metre,
    )
    if not all(math.isfinite(value) for value in values):
        raise ValueError("scenario physical values must be finite")
    if not 4 <= scenario.target_speed_mps <= 20:
        raise ValueError("target speed must be in [4,20] m/s")
    if not 1.5 <= scenario.lane_half_width_metres <= 5:
        raise ValueError("lane half width must be in [1.5,5] metres")
    if abs(scenario.initial_lateral_metres) >= scenario.lane_half_width_metres:
        raise ValueError("initial vehicle centre is outside the lane")
    if abs(scenario.initial_heading_radians) > 0.3:
        raise ValueError("initial heading magnitude exceeds 0.3 radians")
    if abs(scenario.road_curvature_per_metre) > 0.01:
        raise ValueError("curvature magnitude exceeds 0.01 per metre")
    # CircularLane.local_coordinates wraps at pi. Restrict this fixture instead
    # of silently interpreting a multi-lap run as a short trajectory.
    angular_travel_bound = (
        scenario.steps * scenario.tick_milliseconds / 1000
        * Vehicle.MAX_SPEED * abs(scenario.road_curvature_per_metre)
    )
    if angular_travel_bound >= math.pi:
        raise ValueError("circular fixture exceeds its unambiguous arc bound")
    for fault in scenario.faults:
        if fault.kind not in (
            pb.FAULT_KIND_FREEZE_BRAIN,
            pb.FAULT_KIND_STALE_OBSERVATION,
            pb.FAULT_KIND_WRONG_EPOCH,
        ):
            raise ValueError("unsupported fault kind")
        if fault.duration_ticks == 0 or fault.start_tick + fault.duration_ticks > scenario.steps:
            raise ValueError("fault interval must lie within the episode")
        if fault.kind == pb.FAULT_KIND_STALE_OBSERVATION and fault.start_tick < 1:
            raise ValueError("stale-observation fault needs a previous observation")


def make_scenario(seed: int, steps: int = 120):
    rng = np.random.default_rng(seed)
    scenario = pb.Scenario(
        schema_version=1,
        name=f"lane-tracking-{seed}",
        seed=seed,
        steps=steps,
        tick_milliseconds=100,
        target_speed_mps=float(rng.uniform(8, 14)),
        lane_half_width_metres=2.0,
        initial_lateral_metres=float(rng.uniform(-1.2, 1.2)),
        initial_heading_radians=float(rng.uniform(-0.08, 0.08)),
        road_curvature_per_metre=float(rng.choice([-0.005, 0.0, 0.005])),
    )
    validate_scenario(scenario)
    return scenario


def fault_scenarios(seed: int, steps: int = 120):
    if steps < 12:
        raise ValueError("fault suite requires at least 12 steps")
    for kind in (
        pb.FAULT_KIND_FREEZE_BRAIN,
        pb.FAULT_KIND_STALE_OBSERVATION,
        pb.FAULT_KIND_WRONG_EPOCH,
    ):
        scenario = make_scenario(seed, steps)
        scenario.name = f"fault-{pb.FaultKind.Name(kind).lower()}-{seed}"
        scenario.faults.add(kind=kind, start_tick=steps // 3, duration_ticks=4)
        yield scenario


def fixed_point(value: float) -> int:
    """Nearest integer, ties-to-even; distinct from SCALE's truncation rule."""
    return int(np.clip(round(value * 1000), -10000, 10000))


class DrivingPlant:
    def __init__(self, scenario):
        validate_scenario(scenario)
        self.scenario = scenario
        curvature = scenario.road_curvature_per_metre
        if curvature == 0:
            self.lane = StraightLane([0, 0], [10_000, 0], width=2 * scenario.lane_half_width_metres)
        else:
            direction = 1 if curvature > 0 else -1
            radius = abs(1 / curvature)
            phase = -direction * math.pi / 2
            self.lane = CircularLane(
                [0, direction * radius], radius, phase,
                phase + direction * math.pi,
                clockwise=direction == 1, width=2 * scenario.lane_half_width_metres,
            )
        network = RoadNetwork()
        network.add_lane("start", "end", self.lane)
        self.road = Road(network=network, np_random=np.random.RandomState(scenario.seed % (2**32)))
        self.vehicle = Vehicle(
            self.road,
            self.lane.position(0, scenario.initial_lateral_metres),
            self.lane.heading_at(0) + scenario.initial_heading_radians,
            speed=scenario.target_speed_mps * 0.6,
        )
        self.road.vehicles.append(self.vehicle)

    def state(self) -> dict:
        longitudinal, lateral = self.lane.local_coordinates(self.vehicle.position)
        heading = self.vehicle.heading - self.lane.heading_at(longitudinal)
        heading = (heading + math.pi) % (2 * math.pi) - math.pi
        return {
            "x_metres": float(self.vehicle.position[0]),
            "y_metres": float(self.vehicle.position[1]),
            "longitudinal_metres": float(longitudinal),
            "lateral_metres": float(lateral),
            "heading_radians": float(self.vehicle.heading),
            "heading_error_radians": float(heading),
            "speed_mps": float(self.vehicle.speed),
        }

    def observe(self, tick: int, epoch: int):
        state = self.state()
        scenario = self.scenario
        return pb.Observation(
            features=[
                fixed_point(state["lateral_metres"] / scenario.lane_half_width_metres),
                fixed_point(state["heading_error_radians"] / 0.5),
                fixed_point((scenario.target_speed_mps - state["speed_mps"]) / 10),
                fixed_point(scenario.road_curvature_per_metre / 0.05),
            ],
            sequence=tick + 1, epoch=epoch, tick=tick,
        )

    def step(self, action) -> None:
        self.vehicle.act({
            "steering": action.steering / 1000 * 0.5,
            "acceleration": action.acceleration / 1000 * 3,
        })
        self.road.step(self.scenario.tick_milliseconds / 1000)


def run_episode(runtime, scenario, on_step=None) -> tuple[dict, list[dict]]:
    """Runtime reset changes the epoch; candidates never change mid-episode."""
    runtime.reset()
    plant = DrivingPlant(scenario)
    records = []
    loss_sum = 0.0
    departed = False
    fallback_count = 0
    fault_mismatches = 0
    peak_lateral = 0.0
    for tick in range(scenario.steps):
        observation = plant.observe(tick, runtime.epoch)
        active_faults = [
            fault for fault in scenario.faults
            if fault.start_tick <= tick < fault.start_tick + fault.duration_ticks
        ]
        expected_fallback = False
        for fault in active_faults:
            if fault.kind == pb.FAULT_KIND_STALE_OBSERVATION:
                observation.sequence = fault.start_tick
                observation.tick = fault.start_tick - 1
                expected_fallback = True
            elif fault.kind == pb.FAULT_KIND_WRONG_EPOCH:
                observation.epoch = runtime.epoch + 1
                expected_fallback = True
            # FREEZE_BRAIN means proposals stop. The activated controller must
            # continue without brain calls; this harness makes no calls inside
            # any episode, so the case checks that separation explicitly.
        action = runtime.step(observation, tick)
        fallback_count += int(action.fallback)
        fault_mismatches += int(action.fallback != expected_fallback)
        before = plant.state()
        plant.step(action)
        after = plant.state()
        lateral = after["lateral_metres"] / scenario.lane_half_width_metres
        heading = after["heading_error_radians"] / 0.5
        speed_error = (scenario.target_speed_mps - after["speed_mps"]) / 10
        loss_sum += (
            LOSS_WEIGHTS["lateral"] * lateral**2
            + LOSS_WEIGHTS["heading"] * heading**2
            + LOSS_WEIGHTS["speed"] * speed_error**2
            + LOSS_WEIGHTS["steering"] * (action.steering / 1000)**2
        )
        peak_lateral = max(peak_lateral, abs(after["lateral_metres"]))
        departed = abs(after["lateral_metres"]) > scenario.lane_half_width_metres
        record = {
            "tick": tick,
            "time_seconds": tick * scenario.tick_milliseconds / 1000,
            "state_before": before,
            "observation": to_dict(observation),
            "action": to_dict(action),
            "state_after": after,
            "faults": [pb.FaultKind.Name(f.kind) for f in active_faults],
            "expected_fallback": expected_fallback,
            "lane_departure": departed,
        }
        records.append(record)
        if on_step is not None:
            on_step(record)
        if departed:
            break
    summary = {
        "scenario": scenario.name,
        "seed": scenario.seed,
        "steps": len(records),
        "requested_steps": scenario.steps,
        "loss": loss_sum / len(records) + DEPARTURE_PENALTY * int(departed),
        "lane_departure": departed,
        "max_abs_lateral_metres": peak_lateral,
        "final_speed_mps": plant.vehicle.speed,
        "fallback_count": fallback_count,
        "fault_mismatches": fault_mismatches,
        "epoch": runtime.epoch,
        "fault_ticks_observed": sum(bool(row["faults"]) for row in records),
        "fault_ticks_requested": sum(
            any(f.start_tick <= tick < f.start_tick + f.duration_ticks for f in scenario.faults)
            for tick in range(scenario.steps)
        ),
    }
    return summary, records
