#!/usr/bin/env python3
"""Run a bounded Isaac Sim 6.0 procedural rigid-body episode."""

from __future__ import annotations

import argparse
import importlib.metadata
import math
import os
from pathlib import Path
import sys
import time

VERSION = "6.0"
FIXED_DT = 1.0 / 60.0


def run(args, api) -> None:
    from common import ArtifactUploadError, Evidence, FrameEvidence, announce_terminal, best_effort_close, best_effort_failure, best_effort_failure_upload, best_effort_mark_manifest_failed, report_artifact_failure, sha256, upload_artifacts, write_json
    try:
        evidence = Evidence(args.output, args.run_id, args.progress_url)
    except Exception as error:
        best_effort_failure(args.output, args.run_id, args.progress_url, str(error), "")
        raise
    app = None
    camera = None
    started = time.monotonic()
    terminal_phase = "completed"
    terminal_message = f"completed {args.steps} fixed physics steps; no autonomy policy was evaluated"
    steps_completed = 0
    frames = FrameEvidence(args.output, "NVIDIA Isaac Sim 6.0", getattr(args, "capture_every", 0),
                           getattr(args, "capture_width", 640), getattr(args, "capture_height", 360))
    try:
        evidence.definitions(("simulation_steps_completed", "simulator_x_metres", "simulator_y_metres", "simulator_z_metres", "simulator_speed_mps"))
        evidence.status("started", "launching Isaac Sim 6.0 headless")
        app, world, cube, runtime_version = api.create(FIXED_DT, args.seed)
        if frames.enabled:
            camera = api.create_camera(frames.width, frames.height)
            # Replicator annotator buffers arrive asynchronously by default.
            # Block each render so the bounded readiness attempts below count
            # completed frames rather than submissions to the renderer.
            world.set_block_on_render(True)
        for step in range(args.steps):
            if evidence.cancellation_requested():
                terminal_phase = "cancelled"
                terminal_message = f"cancelled after {step} fixed physics steps"
                break
            if time.monotonic() - started > args.max_wall_seconds:
                raise TimeoutError(f"Isaac run exceeded {args.max_wall_seconds} wall seconds")
            world.step(render=frames.enabled)
            completed_step = step + 1
            position, orientation = cube.get_world_pose()
            velocity = cube.get_linear_velocity()
            values = [float(value) for value in position]
            speed = math.sqrt(sum(float(value) ** 2 for value in velocity))
            evidence.step({"step": step, "simulation_seconds": (step + 1) * FIXED_DT,
                           "position": values, "orientation_wxyz": [float(value) for value in orientation],
                           "linear_velocity": [float(value) for value in velocity], "speed_mps": speed})
            evidence.metrics((
                ("simulation_steps_completed", step + 1),
                ("simulator_x_metres", values[0]),
                ("simulator_y_metres", values[1]),
                ("simulator_z_metres", values[2]),
                ("simulator_speed_mps", speed),
            ), step + 1, "isaac-sim-6.0")
            steps_completed = step + 1
            if frames.enabled and completed_step % frames.capture_every == 0:
                path = frames.path(completed_step)
                api.save_camera_rgb(
                    world, camera, path, frames.width, frames.height,
                    started + args.max_wall_seconds,
                )
                frames.record(completed_step, completed_step * FIXED_DT, path)
        frames.write(terminal_phase)
        write_json(args.output / "manifest.json", {
            "format": "brain-spine-simulator-run-v1", "status": terminal_phase, "run_id": args.run_id,
            "simulator": {"name": "NVIDIA Isaac Sim", "expected_version": VERSION,
                          "runtime_distribution_version": runtime_version},
            "configuration": {"steps": args.steps, "seed": args.seed, "physics_dt": FIXED_DT,
                              "max_wall_seconds": args.max_wall_seconds,
                              "initial_linear_velocity_mps": [1.0, 0.0, 0.0],
                              "capture_every": frames.capture_every, "capture_width": frames.width,
                              "capture_height": frames.height},
            "steps_completed": steps_completed,
            "scripts": {"isaac_rigidbody.py": sha256(Path(__file__)),
                        "common.py": sha256(Path(__file__).with_name("common.py"))},
            "elapsed_wall_seconds": time.monotonic() - started,
            "scope": "procedural rigid body on a ground plane; no robot asset, learning, or physical-safety claim",
        })
    except Exception as error:
        cleanup_errors = []
        if camera is not None:
            try:
                api.destroy_camera(camera)
            except Exception as cleanup_error:
                cleanup_errors.append(f"camera destroy: {cleanup_error}")
            camera = None
        if app is not None:
            try:
                app.close()
            except Exception as cleanup_error:
                cleanup_errors.append(f"app close: {cleanup_error}")
            app = None
        try:
            frames.write("failed", str(error))
        except OSError:
            pass
        best_effort_failure(args.output, args.run_id, args.progress_url,
                            f"{error}" + (f"; cleanup error: {'; '.join(cleanup_errors)}" if cleanup_errors else ""),
                            "trace.jsonl")
        best_effort_close(evidence)
        best_effort_failure_upload(args.output, args.artifact_uri)
        raise
    cleanup_errors = []
    if camera is not None:
        try:
            api.destroy_camera(camera)
        except Exception as error:
            cleanup_errors.append(f"camera destroy: {error}")
        camera = None
    if app is not None:
        try:
            app.close()
        except Exception as error:
            cleanup_errors.append(f"app close: {error}")
        app = None
    if cleanup_errors:
        error = RuntimeError("; ".join(cleanup_errors))
        try:
            frames.write("failed", str(error))
        except OSError:
            pass
        best_effort_mark_manifest_failed(args.output, error)
        best_effort_failure(args.output, args.run_id, args.progress_url,
                            f"Isaac cleanup failed: {error}", "trace.jsonl")
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


class IsaacAPI:
    @staticmethod
    def create(physics_dt: float, seed: int):
        del seed  # The procedural fixture contains no stochastic operation.
        # Load the worker-pinned complete package before Kit prepends extension
        # bundles. Isaac Sim 6.0's SimReady bundle contains an incomplete
        # botocore namespace that otherwise prevents Replicator camera startup.
        import botocore.exceptions  # noqa: F401
        from isaacsim import SimulationApp
        app = SimulationApp({"headless": True, "fast_shutdown": False})
        try:
            import numpy as np
            from isaacsim.core.api import World
            from isaacsim.core.api.objects import DynamicCuboid
            world = World(physics_dt=physics_dt, rendering_dt=physics_dt, stage_units_in_meters=1.0)
            world.scene.add_default_ground_plane()
            IsaacAPI.create_light(world.stage)
            cube = world.scene.add(DynamicCuboid(
                prim_path="/World/BrainSpineCube", name="brain_spine_cube",
                position=np.array([0.0, 0.0, 1.0]), scale=np.array([0.5, 0.5, 0.5]),
                color=np.array([0.2, 0.5, 0.9]), mass=1.0,
            ))
            world.reset()
            cube.set_linear_velocity(np.array([1.0, 0.0, 0.0]))
            try:
                runtime_version = importlib.metadata.version("isaacsim")
            except importlib.metadata.PackageNotFoundError:
                runtime_version = "6.0 (distribution metadata unavailable)"
            if not runtime_version.startswith(VERSION):
                raise RuntimeError(f"Isaac Sim version mismatch: {runtime_version}, expected {VERSION}.x")
            return app, world, cube, runtime_version
        except Exception:
            app.close()
            raise

    @staticmethod
    def create_light(stage) -> None:
        from pxr import UsdLux
        light = UsdLux.DomeLight.Define(stage, "/World/BrainSpineDomeLight")
        light.CreateIntensityAttr(1000.0)

    @staticmethod
    def create_camera(width: int, height: int):
        import numpy as np
        from isaacsim.core.utils.rotations import rot_matrix_to_quat
        from isaacsim.sensors.camera import Camera
        position = [-3.0, -3.0, 2.0]
        target = [0.5, 0.0, 0.5]
        forward = [target[index] - position[index] for index in range(3)]
        length = math.sqrt(sum(value * value for value in forward))
        forward = [value / length for value in forward]
        world_up = [0.0, 0.0, 1.0]
        # With camera_axes="world", Isaac cameras use +X forward and +Z up.
        left = [
            world_up[1] * forward[2] - world_up[2] * forward[1],
            world_up[2] * forward[0] - world_up[0] * forward[2],
            world_up[0] * forward[1] - world_up[1] * forward[0],
        ]
        length = math.sqrt(sum(value * value for value in left))
        left = [value / length for value in left]
        up = [
            forward[1] * left[2] - forward[2] * left[1],
            forward[2] * left[0] - forward[0] * left[2],
            forward[0] * left[1] - forward[1] * left[0],
        ]
        rotation = np.array([[forward[row], left[row], up[row]] for row in range(3)])
        camera = Camera(
            prim_path="/World/BrainSpineCamera",
            resolution=(width, height),
            annotator_device="cpu",
        )
        camera.set_world_pose(
            position=np.array(position),
            orientation=rot_matrix_to_quat(rotation),
            camera_axes="world",
        )
        camera.initialize(attach_rgb_annotator=True)
        return camera

    @staticmethod
    def save_camera_rgb(world, camera, path: Path, width: int, height: int,
                        wall_deadline: float) -> None:
        from common import write_rgb_png
        rgb = camera.get_rgb(device="cpu")
        # Isaac's RGB annotator can remain empty for several frames after it is
        # attached. World.render() refreshes rendering without advancing the
        # physics simulation or its fixed-step clock.
        for _ in range(8):
            if rgb is not None:
                break
            if time.monotonic() > wall_deadline:
                raise TimeoutError("Isaac run exceeded its wall-time bound while waiting for RGB data")
            world.render()
            rgb = camera.get_rgb(device="cpu")
        if rgb is None:
            raise RuntimeError("Isaac RGB camera returned no frame after 8 render-only readiness attempts")
        pixels = rgb.astype("uint8").tobytes()
        write_rgb_png(path, width, height, pixels)

    @staticmethod
    def destroy_camera(camera) -> None:
        camera.destroy()


def parse_args():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run-id", default=os.environ.get("CSF_RUN_ID"), required="CSF_RUN_ID" not in os.environ)
    parser.add_argument("--steps", type=int, default=int(os.environ.get("CSF_STEPS", "120")))
    parser.add_argument("--output", type=Path, default=Path(os.environ.get("CSF_OUTPUT", "/tmp/csf-run")))
    parser.add_argument("--progress-url")
    parser.add_argument("--artifact-uri", default=os.environ.get("CSF_ARTIFACT_URI"))
    parser.add_argument("--seed", type=int, default=601)
    parser.add_argument("--max-wall-seconds", type=float, default=900.0)
    parser.add_argument("--capture-every", type=int, default=int(os.environ.get("CSF_CAPTURE_EVERY", "0")),
                        help="save one vendor RGB frame every N completed steps; 0 disables capture")
    parser.add_argument("--capture-width", type=int, default=640)
    parser.add_argument("--capture-height", type=int, default=360)
    args = parser.parse_args()
    if args.steps < 1 or args.steps > 10000 or args.max_wall_seconds <= 0:
        parser.error("steps must be 1..10000 and max-wall-seconds must be positive")
    if args.capture_every < 0 or not (64 <= args.capture_width <= 1920) or not (64 <= args.capture_height <= 1080):
        parser.error("capture-every must be nonnegative and capture dimensions must be width 64..1920, height 64..1080")
    return args


def successful_process_exit() -> None:
    """Exit after explicit Kit, evidence, upload, and progress cleanup succeeded.

    Graceful Kit shutdown unloads native extensions but leaves CPython running;
    the pinned Isaac 6.0 image can segfault when interpreter finalizers reenter
    those unloaded modules. All worker-owned files and network work are explicit
    and complete before this point, so skip only CPython's finalization phase.
    """
    sys.stdout.flush()
    sys.stderr.flush()
    os._exit(0)


def main() -> None:
    args = parse_args()
    try:
        import isaacsim  # noqa: F401 - validates the vendor runtime before protobuf support.
    except ImportError as error:
        raise SystemExit("Isaac Sim 6.0 Python runtime is required; run with /isaac-sim/python.sh") from error
    run(args, IsaacAPI())
    successful_process_exit()


if __name__ == "__main__":
    main()
