#!/usr/bin/env python3
"""Run a bounded CARLA 0.9.16 waypoint-following vehicle episode."""

from __future__ import annotations

import argparse
import math
from pathlib import Path
import queue
import random
import os
import sys
import time

VERSION = "0.9.16"
FIXED_DT = 0.05


def run(args, carla) -> None:
    from common import ArtifactUploadError, Evidence, FrameEvidence, announce_terminal, best_effort_close, best_effort_failure, best_effort_failure_upload, best_effort_mark_manifest_failed, report_artifact_failure, sha256, upload_artifacts, write_json
    try:
        evidence = Evidence(args.output, args.run_id, args.progress_url)
    except Exception as error:
        best_effort_failure(args.output, args.run_id, args.progress_url, str(error), "")
        raise
    vehicle = None
    camera = None
    camera_frames = None
    world = None
    original_settings = None
    started = time.monotonic()
    terminal_phase = "completed"
    terminal_message = f"completed {args.steps} fixed steps"
    steps_completed = 0
    frames = FrameEvidence(args.output, "CARLA 0.9.16", getattr(args, "capture_every", 0),
                           getattr(args, "capture_width", 640), getattr(args, "capture_height", 360))
    try:
        evidence.definitions(("simulation_steps_completed", "simulator_x_metres", "simulator_y_metres", "simulator_speed_mps", "simulator_tracking_error_metres"))
        evidence.status("started", "connecting to CARLA 0.9.16")
        client = carla.Client(args.host, args.port)
        client.set_timeout(args.timeout_seconds)
        if client.get_client_version() != VERSION or client.get_server_version() != VERSION:
            raise RuntimeError(
                f"CARLA version mismatch: client={client.get_client_version()} server={client.get_server_version()} expected={VERSION}"
            )
        world = get_ready_world(client, args.startup_timeout_seconds, args.timeout_seconds)
        original_settings = world.get_settings()
        settings = world.get_settings()
        settings.synchronous_mode = True
        settings.fixed_delta_seconds = FIXED_DT
        settings.substepping = True
        settings.max_substep_delta_time = 0.01
        settings.max_substeps = 5
        world.apply_settings(settings)

        rng = random.Random(args.seed)
        points = world.get_map().get_spawn_points()
        if not points:
            raise RuntimeError("CARLA map has no spawn points")
        spawn = points[rng.randrange(len(points))]
        blueprints = sorted(world.get_blueprint_library().filter("vehicle.*"), key=lambda item: item.id)
        if not blueprints:
            raise RuntimeError("CARLA installation has no vehicle blueprint")
        blueprint = next((item for item in blueprints if item.id == "vehicle.tesla.model3"), blueprints[0])
        if blueprint.has_attribute("role_name"):
            blueprint.set_attribute("role_name", "brain-spine-example")
        vehicle = world.try_spawn_actor(blueprint, spawn)
        if vehicle is None:
            raise RuntimeError("selected deterministic spawn point is occupied")
        if frames.enabled:
            camera_blueprint = world.get_blueprint_library().find("sensor.camera.rgb")
            camera_blueprint.set_attribute("image_size_x", str(frames.width))
            camera_blueprint.set_attribute("image_size_y", str(frames.height))
            camera_blueprint.set_attribute("sensor_tick", "0.0")
            camera = world.spawn_actor(
                camera_blueprint,
                carla.Transform(carla.Location(x=-5.5, z=2.8), carla.Rotation(pitch=-12.0)),
                attach_to=vehicle,
            )
            camera_frames = queue.Queue(maxsize=2)

            def retain_latest(image):
                if camera_frames.full():
                    try:
                        camera_frames.get_nowait()
                    except queue.Empty:
                        pass
                camera_frames.put_nowait(image)

            camera.listen(retain_latest)
        world.tick(args.timeout_seconds)

        for step in range(args.steps):
            if time.monotonic() - started > args.max_wall_seconds:
                raise TimeoutError(f"CARLA run exceeded {args.max_wall_seconds} wall seconds")
            if evidence.cancellation_requested():
                terminal_phase = "cancelled"
                terminal_message = f"cancelled after {step} fixed steps"
                break
            location = vehicle.get_location()
            waypoint = world.get_map().get_waypoint(location, project_to_road=True, lane_type=carla.LaneType.Driving)
            if waypoint is None:
                raise RuntimeError(f"vehicle is off the driving map at step {step}")
            choices = waypoint.next(5.0)
            if not choices:
                raise RuntimeError(f"waypoint route ended at step {step}")
            target = sorted(choices, key=lambda point: point.id)[0].transform.location
            transform = vehicle.get_transform()
            yaw = math.radians(transform.rotation.yaw)
            desired = math.atan2(target.y - location.y, target.x - location.x)
            heading_error = math.atan2(math.sin(desired - yaw), math.cos(desired - yaw))
            velocity = vehicle.get_velocity()
            speed = math.sqrt(velocity.x ** 2 + velocity.y ** 2 + velocity.z ** 2)
            steer = max(-1.0, min(1.0, 1.5 * heading_error))
            throttle = max(0.0, min(0.6, 0.35 + 0.15 * (args.target_speed_mps - speed)))
            brake = max(0.0, min(1.0, 0.2 * (speed - args.target_speed_mps)))
            vehicle.apply_control(carla.VehicleControl(throttle=throttle, steer=steer, brake=brake))
            frame = world.tick(args.timeout_seconds)
            completed_step = step + 1
            if frames.enabled and completed_step % frames.capture_every == 0:
                image = matching_camera_frame(camera_frames, frame, args.timeout_seconds)
                image_path = frames.path(completed_step)
                image.save_to_disk(str(image_path))
                frames.record(completed_step, completed_step * FIXED_DT, image_path,
                              vendor_frame=int(image.frame), vendor_timestamp_seconds=float(image.timestamp))
            location = vehicle.get_location()
            velocity = vehicle.get_velocity()
            speed = math.sqrt(velocity.x ** 2 + velocity.y ** 2 + velocity.z ** 2)
            error = math.sqrt((location.x - target.x) ** 2 + (location.y - target.y) ** 2)
            record = {"step": step, "frame": frame, "simulation_seconds": (step + 1) * FIXED_DT,
                      "position": [location.x, location.y, location.z], "speed_mps": speed,
                      "target": [target.x, target.y, target.z], "tracking_error_metres": error,
                      "control": {"throttle": throttle, "steer": steer, "brake": brake}}
            evidence.step(record)
            evidence.metrics((
                ("simulation_steps_completed", step + 1),
                ("simulator_x_metres", location.x),
                ("simulator_y_metres", location.y),
                ("simulator_speed_mps", speed),
                ("simulator_tracking_error_metres", error),
            ), step + 1, "carla-0.9.16")
            steps_completed = step + 1

        frames.write(terminal_phase)
        write_json(args.output / "manifest.json", {
            "format": "brain-spine-simulator-run-v1", "status": terminal_phase, "run_id": args.run_id,
            "simulator": {"name": "CARLA", "expected_version": VERSION,
                          "client_version": client.get_client_version(), "server_version": client.get_server_version()},
            "configuration": {"steps": args.steps, "seed": args.seed, "fixed_delta_seconds": FIXED_DT,
                              "target_speed_mps": args.target_speed_mps, "host": args.host, "port": args.port,
                              "max_wall_seconds": args.max_wall_seconds,
                              "capture_every": frames.capture_every, "capture_width": frames.width,
                              "capture_height": frames.height},
            "steps_completed": steps_completed,
            "scripts": {"carla_waypoint.py": sha256(Path(__file__)),
                        "common.py": sha256(Path(__file__).with_name("common.py"))},
            "elapsed_wall_seconds": time.monotonic() - started,
            "scope": "single vehicle, map waypoints, no perception, learning, traffic, or physical-safety claim",
        })
    except Exception as error:
        try:
            frames.write("failed", str(error))
        except OSError:
            pass
        cleanup_errors = cleanup(world, original_settings, vehicle, camera)
        world = original_settings = vehicle = camera = None
        message = str(error)
        if cleanup_errors:
            message += f"; cleanup error: {'; '.join(cleanup_errors)}"
        best_effort_failure(args.output, args.run_id, args.progress_url, message, "trace.jsonl")
        best_effort_close(evidence)
        best_effort_failure_upload(args.output, args.artifact_uri)
        raise
    cleanup_errors = cleanup(world, original_settings, vehicle, camera)
    if cleanup_errors:
        error = RuntimeError("; ".join(cleanup_errors))
        try:
            frames.write("failed", str(error))
        except OSError:
            pass
        best_effort_mark_manifest_failed(args.output, error)
        best_effort_failure(args.output, args.run_id, args.progress_url,
                            f"CARLA cleanup failed: {error}", "trace.jsonl")
        best_effort_close(evidence)
        best_effort_failure_upload(args.output, args.artifact_uri)
        raise error
    evidence.status(terminal_phase, terminal_message, "manifest.json", forward=False, announce=False)
    evidence.close()
    try:
        upload_artifacts(args.output, args.artifact_uri)
    except ArtifactUploadError as error:
        best_effort_mark_manifest_failed(args.output, error)
        report_artifact_failure(args.output, args.run_id, args.progress_url, error)
        raise
    announce_terminal(args.output, args.run_id, args.progress_url)


def matching_camera_frame(camera_frames, expected_frame: int, timeout_seconds: float):
    deadline = time.monotonic() + timeout_seconds
    while True:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise TimeoutError(f"CARLA RGB camera did not produce frame {expected_frame}")
        image = camera_frames.get(timeout=remaining)
        if image.frame == expected_frame:
            return image
        if image.frame > expected_frame:
            raise RuntimeError(f"CARLA RGB camera skipped frame {expected_frame}; received {image.frame}")


def get_ready_world(client, startup_timeout_seconds: float, timeout_seconds: float):
    """Wait once for Unreal's world load, then restore bounded steady-state RPCs."""
    client.set_timeout(startup_timeout_seconds)
    try:
        return client.get_world()
    finally:
        client.set_timeout(timeout_seconds)


def cleanup(world, original_settings, vehicle, camera=None) -> list[str]:
    errors = []
    if camera is not None:
        try:
            camera.stop()
        except Exception as error:
            errors.append(f"camera stop: {error}")
        try:
            camera.destroy()
        except Exception as error:
            errors.append(f"camera destroy: {error}")
    if vehicle is not None:
        try:
            vehicle.destroy()
        except Exception as error:
            errors.append(f"vehicle destroy: {error}")
    if world is not None and original_settings is not None:
        try:
            world.apply_settings(original_settings)
        except Exception as error:
            errors.append(f"world settings restore: {error}")
    return errors


def parse_args():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run-id", default=os.environ.get("CSF_RUN_ID"), required="CSF_RUN_ID" not in os.environ)
    parser.add_argument("--steps", type=int, default=int(os.environ.get("CSF_STEPS", "100")))
    parser.add_argument("--output", type=Path, default=Path(os.environ.get("CSF_OUTPUT", "/tmp/csf-run")))
    parser.add_argument("--progress-url")
    parser.add_argument("--artifact-uri", default=os.environ.get("CSF_ARTIFACT_URI"))
    parser.add_argument("--seed", type=int, default=601)
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=2000)
    parser.add_argument("--timeout-seconds", type=float, default=10.0)
    parser.add_argument("--startup-timeout-seconds", type=float, default=120.0,
                        help="bounded timeout for the initial CARLA world load")
    parser.add_argument("--target-speed-mps", type=float, default=8.0)
    parser.add_argument("--max-wall-seconds", type=float, default=900.0)
    parser.add_argument("--capture-every", type=int, default=int(os.environ.get("CSF_CAPTURE_EVERY", "0")),
                        help="save one vendor RGB frame every N completed steps; 0 disables capture")
    parser.add_argument("--capture-width", type=int, default=640)
    parser.add_argument("--capture-height", type=int, default=360)
    args = parser.parse_args()
    if (args.steps < 1 or args.steps > 10000 or args.timeout_seconds <= 0
            or args.startup_timeout_seconds <= 0 or args.max_wall_seconds <= 0
            or args.startup_timeout_seconds > args.max_wall_seconds):
        parser.error("steps must be 1..10000; timeouts must be positive; startup timeout must not exceed max wall seconds")
    if args.capture_every < 0 or not (64 <= args.capture_width <= 1920) or not (64 <= args.capture_height <= 1080):
        parser.error("capture-every must be nonnegative and capture dimensions must be width 64..1920, height 64..1080")
    return args


def main() -> None:
    args = parse_args()
    try:
        import carla
    except ImportError as error:
        raise SystemExit("CARLA 0.9.16 Python API is required; run with its bundled PythonAPI") from error
    run(args, carla)


if __name__ == "__main__":
    main()
