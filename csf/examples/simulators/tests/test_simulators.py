from __future__ import annotations

from argparse import Namespace
import json
from pathlib import Path
import sys
import types

HERE = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(HERE))

import common
import carla_waypoint
import isaac_rigidbody
import pytest


class _Response:
    status = 202

    def __enter__(self):
        return self

    def __exit__(self, *_):
        return False

    def read(self):
        return b'{"run":{"cancellationRequested":true}}'


def test_progress_envelope_uses_owned_event(monkeypatch, tmp_path):
    observed = []
    monkeypatch.setattr(common.urllib.request, "urlopen", lambda request, timeout: (observed.append((request, timeout)) or _Response()))
    evidence = common.Evidence(tmp_path / "run", "run-1", "http://example.invalid/api/simulation/events")
    evidence.status("started", "test")
    evidence.close()
    body = json.loads(observed[0][0].data)
    assert body["runId"] == "run-1"
    assert body["events"][0]["status"]["phase"] == "started"
    assert observed[0][1] == 10


def test_progress_failure_is_retained_and_raised(monkeypatch, tmp_path):
    attempts = []
    def offline(request, timeout):
        attempts.append((request.data, timeout))
        raise OSError("offline")
    monkeypatch.setattr(common.urllib.request, "urlopen", offline)
    evidence = common.Evidence(tmp_path / "run", "run-1", "http://example.invalid/api/simulation/events")
    with pytest.raises(common.ProgressDeliveryError, match="offline"):
        evidence.status("started", "test")
    evidence.close()
    failure = json.loads((tmp_path / "run" / "progress-errors.jsonl").read_text())
    assert failure["events"][0]["status"]["phase"] == "started"
    assert len(attempts) == 3
    assert len({body for body, _timeout in attempts}) == 1
    assert {timeout for _body, timeout in attempts} == {10}
    assert len((tmp_path / "run" / "events.jsonl").read_text().splitlines()) == 1


def test_progress_timeout_then_success_does_not_duplicate_evidence(monkeypatch, tmp_path, capsys):
    attempts = []
    def flaky(request, timeout):
        attempts.append(request.data)
        if len(attempts) == 1:
            raise TimeoutError("busy")
        return _Response()
    monkeypatch.setattr(common.urllib.request, "urlopen", flaky)
    evidence = common.Evidence(tmp_path / "run", "run-1", "http://example.invalid/api/simulation/events")
    evidence.status("started", "test")
    evidence.close()
    assert len(attempts) == 2
    assert attempts[0] == attempts[1]
    assert len((tmp_path / "run" / "events.jsonl").read_text().splitlines()) == 1
    assert (tmp_path / "run" / "progress-errors.jsonl").read_text() == ""
    assert capsys.readouterr().out.count("CSF_EVENT ") == 1


def test_metric_batch_uses_one_request_and_retains_each_event_once(monkeypatch, tmp_path, capsys):
    observed = []
    monkeypatch.setattr(common.urllib.request, "urlopen",
                        lambda request, timeout: (observed.append(request.data) or _Response()))
    evidence = common.Evidence(tmp_path / "run", "run-1", "http://example.invalid/api/simulation/events")
    evidence.metrics((("simulation_steps_completed", 1), ("simulator_x_metres", 2.5)),
                     1, "test-simulator")
    evidence.close()
    envelope = json.loads(observed[0])
    assert len(observed) == 1
    assert [event["measurement"]["metric"] for event in envelope["events"]] == [
        "simulation_steps_completed", "simulator_x_metres",
    ]
    assert len((tmp_path / "run" / "events.jsonl").read_text().splitlines()) == 2
    assert capsys.readouterr().out.count("CSF_EVENT ") == 2


def test_cancellation_uses_sibling_inspect_endpoint(monkeypatch, tmp_path):
    observed = []
    monkeypatch.setattr(common.urllib.request, "urlopen", lambda request, timeout: (observed.append(request) or _Response()))
    evidence = common.Evidence(tmp_path / "run", "run-1", "http://service/api/simulation/events")
    assert evidence.cancellation_requested()
    evidence.close()
    assert observed[0].full_url == "http://service/api/simulation/inspect"
    assert json.loads(observed[0].data) == {"runId": "run-1"}


class _World:
    def __init__(self):
        self.steps = 0
        self.rendered = []
        self.block_on_render = False

    def step(self, render=False):
        self.rendered.append(render)
        self.steps += 1

    def render(self):
        self.rendered.append("readiness")

    def set_block_on_render(self, enabled):
        self.block_on_render = enabled


class _Cube:
    def __init__(self, world):
        self.world = world

    def get_world_pose(self):
        return ([self.world.steps / 60, 0.0, 0.5], [1.0, 0.0, 0.0, 0.0])

    def get_linear_velocity(self):
        return [1.0, 0.0, 0.0]


class _App:
    def __init__(self):
        self.closed = False

    def close(self):
        self.closed = True


class _Isaac:
    def __init__(self):
        self.app = _App()
        self.world = _World()

    def create(self, physics_dt, seed):
        assert physics_dt == 1 / 60
        assert seed == 7
        return self.app, self.world, _Cube(self.world), "6.0.0"


class _Pixels:
    def astype(self, kind):
        assert kind == "uint8"
        return self

    def tobytes(self):
        return bytes([20, 40, 60]) * (64 * 64)


class _Camera:
    def __init__(self, fail=False):
        self.destroyed = False
        self.fail = fail

    def destroy(self):
        self.destroyed = True


class _CapturingIsaac(_Isaac):
    def __init__(self, fail=False):
        super().__init__()
        self.camera = _Camera(fail)

    def create_camera(self, width, height):
        assert (width, height) == (64, 64)
        return self.camera

    def save_camera_rgb(self, _world, camera, path, width, height, _wall_deadline):
        assert _world.block_on_render is True
        if camera.fail:
            raise RuntimeError("RTX capture unavailable")
        common.write_rgb_png(path, width, height, _Pixels().tobytes())

    def destroy_camera(self, camera):
        camera.destroy()


class _CameraCleanupFailure(_CapturingIsaac):
    def destroy_camera(self, _camera):
        raise RuntimeError("camera cleanup failed")


def test_isaac_vendor_adapter_disables_process_exiting_fast_shutdown(monkeypatch):
    observed = []
    app = _App()

    botocore = types.ModuleType("botocore")
    botocore.__path__ = []
    botocore_exceptions = types.ModuleType("botocore.exceptions")
    botocore.exceptions = botocore_exceptions

    class Scene:
        def add_default_ground_plane(self):
            pass

        def add(self, cube):
            return cube

    class World:
        def __init__(self, **_kwargs):
            self.scene = Scene()
            self.stage = object()

        def reset(self):
            pass

    class Cube:
        def __init__(self, **_kwargs):
            pass

        def set_linear_velocity(self, _velocity):
            pass

    isaacsim = types.ModuleType("isaacsim")

    def simulation_app(config):
        assert sys.modules.get("botocore.exceptions") is botocore_exceptions
        observed.append(config)
        return app

    isaacsim.SimulationApp = simulation_app
    core = types.ModuleType("isaacsim.core")
    api = types.ModuleType("isaacsim.core.api")
    api.World = World
    objects = types.ModuleType("isaacsim.core.api.objects")
    objects.DynamicCuboid = Cube
    numpy = types.ModuleType("numpy")
    numpy.array = lambda value: value
    class DomeLight:
        @staticmethod
        def Define(_stage, _path):
            return types.SimpleNamespace(CreateIntensityAttr=lambda _value: None)
    pxr = types.ModuleType("pxr")
    pxr.UsdLux = types.SimpleNamespace(DomeLight=DomeLight)
    monkeypatch.setitem(sys.modules, "botocore", botocore)
    monkeypatch.setitem(sys.modules, "botocore.exceptions", botocore_exceptions)
    monkeypatch.setitem(sys.modules, "isaacsim", isaacsim)
    monkeypatch.setitem(sys.modules, "isaacsim.core", core)
    monkeypatch.setitem(sys.modules, "isaacsim.core.api", api)
    monkeypatch.setitem(sys.modules, "isaacsim.core.api.objects", objects)
    monkeypatch.setitem(sys.modules, "numpy", numpy)
    monkeypatch.setitem(sys.modules, "pxr", pxr)
    monkeypatch.setattr(isaac_rigidbody.importlib.metadata, "version", lambda _name: "6.0.0")

    returned_app, _world, _cube, version = isaac_rigidbody.IsaacAPI.create(1 / 60, 7)
    assert returned_app is app
    assert version == "6.0.0"
    assert observed == [{"headless": True, "fast_shutdown": False}]


def test_isaac_authors_light_and_aims_camera_at_cube_travel_region(monkeypatch):
    authored = {}

    class Light:
        def CreateIntensityAttr(self, intensity):
            authored["intensity"] = intensity

    class DomeLight:
        @staticmethod
        def Define(stage, path):
            authored.update(stage=stage, path=path)
            return Light()

    pxr = types.ModuleType("pxr")
    pxr.UsdLux = types.SimpleNamespace(DomeLight=DomeLight)
    monkeypatch.setitem(sys.modules, "pxr", pxr)
    stage = object()
    isaac_rigidbody.IsaacAPI.create_light(stage)
    assert authored == {"stage": stage, "path": "/World/BrainSpineDomeLight", "intensity": 1000.0}

    camera_call = {}
    class Camera:
        def __init__(self, **kwargs):
            camera_call["constructor"] = kwargs
        def set_world_pose(self, **kwargs):
            camera_call["pose"] = kwargs
        def initialize(self, **kwargs):
            camera_call["initialize"] = kwargs

    numpy = types.ModuleType("numpy")
    numpy.array = lambda value: value
    rotations = types.ModuleType("isaacsim.core.utils.rotations")
    rotations.rot_matrix_to_quat = lambda matrix: camera_call.setdefault("rotation", matrix) or "quaternion"
    sensors = types.ModuleType("isaacsim.sensors.camera")
    sensors.Camera = Camera
    monkeypatch.setitem(sys.modules, "numpy", numpy)
    monkeypatch.setitem(sys.modules, "isaacsim.core.utils.rotations", rotations)
    monkeypatch.setitem(sys.modules, "isaacsim.sensors.camera", sensors)

    isaac_rigidbody.IsaacAPI.create_camera(640, 360)
    position = camera_call["pose"]["position"]
    target_vector = [0.5 - position[0], -position[1], 0.5 - position[2]]
    target_length = sum(value * value for value in target_vector) ** 0.5
    expected_forward = [value / target_length for value in target_vector]
    actual_forward = [camera_call["rotation"][row][0] for row in range(3)]
    assert abs(sum(a * b for a, b in zip(expected_forward, actual_forward)) - 1.0) < 1e-12
    assert camera_call["pose"]["camera_axes"] == "world"
    assert camera_call["constructor"]["resolution"] == (640, 360)
    assert camera_call["initialize"] == {"attach_rgb_annotator": True}


def test_isaac_rgb_readiness_renders_without_duplicating_physics(tmp_path):
    class Pixels:
        def astype(self, kind):
            assert kind == "uint8"
            return self
        def tobytes(self):
            return bytes([20, 40, 60]) * (64 * 64)

    class Camera:
        def __init__(self):
            self.results = [None, None, Pixels()]
        def get_rgb(self, device):
            assert device == "cpu"
            return self.results.pop(0)

    world = _World()
    world.step(render=True)
    output = tmp_path / "ready.png"
    isaac_rigidbody.IsaacAPI.save_camera_rgb(
        world, Camera(), output, 64, 64, isaac_rigidbody.time.monotonic() + 10,
    )
    assert world.steps == 1
    assert world.rendered == [True, "readiness", "readiness"]
    assert output.read_bytes().startswith(b"\x89PNG")


def test_isaac_rgb_readiness_exhaustion_is_bounded(tmp_path):
    class Camera:
        calls = 0
        def get_rgb(self, device):
            assert device == "cpu"
            self.calls += 1

    camera = Camera()
    world = _World()
    with pytest.raises(RuntimeError, match="8 render-only readiness attempts"):
        isaac_rigidbody.IsaacAPI.save_camera_rgb(
            world, camera, tmp_path / "missing.png", 64, 64,
            isaac_rigidbody.time.monotonic() + 10,
        )
    assert world.steps == 0
    assert world.rendered == ["readiness"] * 8
    assert camera.calls == 9


@pytest.mark.parametrize("worker", [carla_waypoint, isaac_rigidbody])
def test_capture_interval_defaults_from_worker_environment(worker, monkeypatch):
    monkeypatch.setenv("CSF_CAPTURE_EVERY", "20")
    monkeypatch.setattr(sys, "argv", [worker.__file__, "--run-id", "env-capture"])
    assert worker.parse_args().capture_every == 20


def test_isaac_success_exit_flushes_before_skipping_interpreter_finalizers(monkeypatch):
    calls = []

    class Stream:
        def __init__(self, name):
            self.name = name

        def flush(self):
            calls.append(self.name)

    monkeypatch.setattr(isaac_rigidbody.sys, "stdout", Stream("stdout"))
    monkeypatch.setattr(isaac_rigidbody.sys, "stderr", Stream("stderr"))
    monkeypatch.setattr(isaac_rigidbody.os, "_exit", lambda code: calls.append(("exit", code)))
    isaac_rigidbody.successful_process_exit()
    assert calls == ["stdout", "stderr", ("exit", 0)]


def test_isaac_main_does_not_force_success_exit_when_run_fails(monkeypatch):
    monkeypatch.setitem(sys.modules, "isaacsim", types.ModuleType("isaacsim"))
    monkeypatch.setattr(isaac_rigidbody, "parse_args", lambda: object())
    monkeypatch.setattr(isaac_rigidbody, "IsaacAPI", lambda: object())
    monkeypatch.setattr(
        isaac_rigidbody, "run",
        lambda _args, _api: (_ for _ in ()).throw(RuntimeError("worker failed")),
    )
    exits = []
    monkeypatch.setattr(isaac_rigidbody.os, "_exit", exits.append)
    with pytest.raises(RuntimeError, match="worker failed"):
        isaac_rigidbody.main()
    assert exits == []


def test_isaac_bounded_run_and_cleanup(tmp_path):
    api = _Isaac()
    output = tmp_path / "isaac"
    isaac_rigidbody.run(Namespace(
        output=output, run_id="isaac-1", progress_url=None, artifact_uri=None,
        steps=3, seed=7, max_wall_seconds=10,
    ), api)
    assert api.world.steps == 3
    assert api.app.closed
    assert json.loads((output / "manifest.json").read_text())["status"] == "completed"
    events = [json.loads(line) for line in (output / "events.jsonl").read_text().splitlines()]
    measurements = [event["measurement"] for event in events if "measurement" in event]
    completed = [item for item in measurements if item["metric"] == "simulation_steps_completed"]
    assert [int(item["value"]) for item in completed] == [1, 2, 3]
    assert len((output / "trace.jsonl").read_text().splitlines()) == 3


def test_isaac_camera_capture_is_bounded_indexed_and_cleaned(tmp_path):
    api = _CapturingIsaac()
    output = tmp_path / "isaac-camera"
    isaac_rigidbody.run(Namespace(
        output=output, run_id="isaac-camera", progress_url=None, artifact_uri=None,
        steps=5, seed=7, max_wall_seconds=10, capture_every=2,
        capture_width=64, capture_height=64,
    ), api)
    assert api.world.steps == 5
    assert api.world.rendered == [True] * 5
    assert api.camera.destroyed
    assert (output / "camera-step-000002.png").read_bytes().startswith(b"\x89PNG")
    frames = json.loads((output / "frames.json").read_text())
    assert [frame["step"] for frame in frames["frames"]] == [2, 4]
    assert [frame["simulation_seconds"] for frame in frames["frames"]] == [2 / 60, 4 / 60]
    assert all(len(frame["sha256"]) == 64 for frame in frames["frames"])
    assert frames["clock_definition"]["wall_recorded_at"].startswith("UTC wall clock")


def test_isaac_camera_error_still_destroys_camera_and_app(tmp_path):
    api = _CapturingIsaac(fail=True)
    output = tmp_path / "isaac-camera-failure"
    with pytest.raises(RuntimeError, match="RTX capture unavailable"):
        isaac_rigidbody.run(Namespace(
            output=output, run_id="isaac-camera-failure", progress_url=None, artifact_uri=None,
            steps=2, seed=7, max_wall_seconds=10, capture_every=1,
            capture_width=64, capture_height=64,
        ), api)
    assert api.camera.destroyed
    assert api.app.closed
    assert json.loads((output / "frames.json").read_text())["status"] == "failed"
    trace = [json.loads(line) for line in (output / "trace.jsonl").read_text().splitlines()]
    assert [row["step"] for row in trace] == [0]
    events = [json.loads(line) for line in (output / "events.jsonl").read_text().splitlines()]
    completed = [event["measurement"] for event in events
                 if event.get("measurement", {}).get("metric") == "simulation_steps_completed"]
    assert [measurement["value"] for measurement in completed] == [1.0]


def test_isaac_camera_cleanup_failure_still_closes_app(tmp_path):
    api = _CameraCleanupFailure()
    output = tmp_path / "isaac-camera-cleanup-failure"
    with pytest.raises(RuntimeError, match="camera cleanup failed"):
        isaac_rigidbody.run(Namespace(
            output=output, run_id="isaac-camera-cleanup-failure", progress_url=None,
            artifact_uri=None, steps=1, seed=7, max_wall_seconds=10,
            capture_every=1, capture_width=64, capture_height=64,
        ), api)
    assert api.app.closed
    assert json.loads((output / "frames.json").read_text())["status"] == "failed"


def test_carla_camera_frame_matching_and_cleanup():
    import queue

    image = types.SimpleNamespace(frame=17)
    frames = queue.Queue()
    frames.put(types.SimpleNamespace(frame=16))
    frames.put(image)
    assert carla_waypoint.matching_camera_frame(frames, 17, 0.1) is image

    calls = []
    camera = types.SimpleNamespace(
        stop=lambda: calls.append("camera-stop"),
        destroy=lambda: calls.append("camera-destroy"),
    )
    vehicle = types.SimpleNamespace(destroy=lambda: calls.append("vehicle-destroy"))
    world = types.SimpleNamespace(apply_settings=lambda settings: calls.append(("restore", settings)))
    assert carla_waypoint.cleanup(world, "original", vehicle, camera) == []
    assert calls == ["camera-stop", "camera-destroy", "vehicle-destroy", ("restore", "original")]


def test_carla_world_readiness_uses_startup_timeout_then_restores_rpc_timeout():
    calls = []
    world = object()

    class Client:
        def set_timeout(self, seconds):
            calls.append(("timeout", seconds))

        def get_world(self):
            calls.append(("get_world",))
            return world

    assert carla_waypoint.get_ready_world(Client(), 120, 10) is world
    assert calls == [("timeout", 120), ("get_world",), ("timeout", 10)]


def test_carla_world_readiness_restores_rpc_timeout_after_startup_failure():
    calls = []

    class Client:
        def set_timeout(self, seconds):
            calls.append(("timeout", seconds))

        def get_world(self):
            calls.append(("get_world",))
            raise RuntimeError("world still loading")

    with pytest.raises(RuntimeError, match="world still loading"):
        carla_waypoint.get_ready_world(Client(), 120, 10)
    assert calls == [("timeout", 120), ("get_world",), ("timeout", 10)]


def test_carla_camera_cleanup_error_does_not_skip_owned_actors():
    calls = []

    def stop_failure():
        calls.append("camera-stop")
        raise RuntimeError("stop failed")

    camera = types.SimpleNamespace(
        stop=stop_failure,
        destroy=lambda: calls.append("camera-destroy"),
    )
    vehicle = types.SimpleNamespace(destroy=lambda: calls.append("vehicle-destroy"))
    world = types.SimpleNamespace(apply_settings=lambda _settings: calls.append("restore"))
    assert carla_waypoint.cleanup(world, object(), vehicle, camera) == ["camera stop: stop failed"]
    assert calls == ["camera-stop", "camera-destroy", "vehicle-destroy", "restore"]


def test_isaac_cooperative_cancellation_before_step(monkeypatch, tmp_path):
    monkeypatch.setattr(common.urllib.request, "urlopen", lambda *_args, **_kwargs: _Response())
    api = _Isaac()
    output = tmp_path / "cancelled"
    isaac_rigidbody.run(Namespace(
        output=output, run_id="isaac-cancel", progress_url="http://service/api/simulation/events",
        artifact_uri=None, steps=3, seed=7, max_wall_seconds=10,
    ), api)
    assert api.world.steps == 0
    assert api.app.closed
    assert _phases(output) == ["started", "cancelled"]
    manifest = json.loads((output / "manifest.json").read_text())
    assert manifest["status"] == "cancelled"
    assert manifest["steps_completed"] == 0


def _phases(output):
    events = [json.loads(line) for line in (output / "events.jsonl").read_text().splitlines()]
    return [event["status"]["phase"] for event in events if "status" in event]


class _VendorFailure:
    def create(self, _physics_dt, _seed):
        raise RuntimeError("vendor unavailable")


def test_vendor_failure_emits_failed_without_completed(tmp_path):
    output = tmp_path / "vendor-failure"
    with pytest.raises(RuntimeError, match="vendor unavailable"):
        isaac_rigidbody.run(Namespace(
            output=output, run_id="failure-1", progress_url=None, artifact_uri=None,
            steps=3, seed=7, max_wall_seconds=10,
        ), _VendorFailure())
    assert _phases(output) == ["started", "failed"]


def test_vendor_failure_posts_failed_to_local_progress(monkeypatch, tmp_path):
    observed = []
    monkeypatch.setattr(common.urllib.request, "urlopen", lambda request, timeout: (observed.append(request) or _Response()))
    output = tmp_path / "vendor-progress-failure"
    with pytest.raises(RuntimeError, match="vendor unavailable"):
        isaac_rigidbody.run(Namespace(
            output=output, run_id="failure-http", progress_url="http://service/api/simulation/events",
            artifact_uri=None, steps=3, seed=7, max_wall_seconds=10,
        ), _VendorFailure())
    terminal = json.loads(observed[-1].data)
    assert terminal["events"][0]["status"]["phase"] == "failed"
    assert terminal["events"][0]["status"]["run_id"] == "failure-http"


class _CloseFailureApp(_App):
    def close(self):
        raise RuntimeError("close failed")


class _CleanupFailure(_Isaac):
    def __init__(self):
        super().__init__()
        self.app = _CloseFailureApp()


def test_cleanup_failure_never_emits_completed(tmp_path):
    output = tmp_path / "cleanup-failure"
    with pytest.raises(RuntimeError, match="close failed"):
        isaac_rigidbody.run(Namespace(
            output=output, run_id="failure-2", progress_url=None, artifact_uri=None,
            steps=1, seed=7, max_wall_seconds=10,
        ), _CleanupFailure())
    assert _phases(output) == ["started", "failed"]
    assert json.loads((output / "manifest.json").read_text())["status"] == "failed"


class _FailingS3:
    def upload_file(self, *_args):
        raise RuntimeError("S3 unavailable")


class _RecordingS3:
    def __init__(self):
        self.uploads = []

    def upload_file(self, path, bucket, key):
        self.uploads.append((Path(path).name, bucket, key, Path(path).read_bytes()))


def test_s3_failure_writes_terminal_failure_receipt(tmp_path):
    output = tmp_path / "artifacts"
    output.mkdir()
    (output / "events.jsonl").write_text("finalized\n")
    with pytest.raises(common.ArtifactUploadError, match="S3 unavailable"):
        common.upload_artifacts(output, "s3://bucket/prefix/run-1", client=_FailingS3())
    receipt = json.loads((output / "upload-receipt.json").read_text())
    assert receipt["status"] == "failed"
    assert receipt["error"] == "S3 unavailable"


def test_s3_client_setup_failure_uses_artifact_failure_path(monkeypatch, tmp_path):
    output = tmp_path / "client-setup-failure"
    output.mkdir()
    (output / "events.jsonl").write_text("finalized\n")
    boto3 = types.ModuleType("boto3")

    def fail_client(_service):
        raise RuntimeError("credentials configuration unavailable")

    boto3.client = fail_client
    monkeypatch.setitem(sys.modules, "boto3", boto3)
    with pytest.raises(common.ArtifactUploadError, match="credentials configuration unavailable"):
        common.upload_artifacts(output, "s3://bucket/prefix/run-1")
    receipt = json.loads((output / "upload-receipt.json").read_text())
    assert receipt == {
        "artifact_uri": "s3://bucket/prefix/run-1",
        "error": "credentials configuration unavailable",
        "objects": [],
        "status": "failed",
    }


def test_s3_client_setup_failure_marks_worker_failed(monkeypatch, tmp_path):
    boto3 = types.ModuleType("boto3")
    boto3.client = lambda _service: (_ for _ in ()).throw(RuntimeError("client setup failed"))
    monkeypatch.setitem(sys.modules, "boto3", boto3)
    output = tmp_path / "worker-client-setup-failure"
    with pytest.raises(common.ArtifactUploadError, match="client setup failed"):
        isaac_rigidbody.run(Namespace(
            output=output, run_id="upload-setup-failure", progress_url=None,
            artifact_uri="s3://bucket/prefix/run-1", steps=1, seed=7, max_wall_seconds=10,
        ), _Isaac())
    assert _phases(output)[-1] == "failed"
    assert json.loads((output / "manifest.json").read_text())["status"] == "failed"


def test_s3_uploads_terminal_receipt_after_finalized_files(tmp_path):
    output = tmp_path / "artifacts-complete"
    output.mkdir()
    (output / "events.jsonl").write_text("finalized\n")
    client = _RecordingS3()
    common.upload_artifacts(output, "s3://bucket/prefix/run-1", client=client)
    assert [upload[0] for upload in client.uploads] == ["events.jsonl", "upload-receipt.json"]
    receipt = json.loads(client.uploads[-1][3])
    assert receipt["status"] == "completed"
    assert receipt["objects"][0]["sha256"] == common.sha256(output / "events.jsonl")


def test_artifact_failure_replaces_local_terminal_state(tmp_path):
    output = tmp_path / "artifact-terminal"
    output.mkdir()
    (output / "events.jsonl").write_text('{"status":{"phase":"completed"}}\n')
    (output / "progress-errors.jsonl").write_text("")
    common.report_artifact_failure(output, "run-1", None, RuntimeError("upload stopped"))
    events = [json.loads(line) for line in (output / "events.jsonl").read_text().splitlines()]
    assert events[-1]["status"]["phase"] == "failed"
    assert events[-1]["status"]["run_id"] == "run-1"
    assert "upload stopped" in events[-1]["status"]["message"]


def test_failure_posts_even_when_events_append_is_enospc(monkeypatch, tmp_path, capsys):
    output = tmp_path / "enospc"
    output.mkdir()
    observed = []
    original_open = Path.open

    def fail_event_append(path, mode="r", *args, **kwargs):
        if path == output / "events.jsonl" and mode == "a":
            raise OSError(28, "No space left on device")
        return original_open(path, mode, *args, **kwargs)

    monkeypatch.setattr(Path, "open", fail_event_append)
    monkeypatch.setattr(common.urllib.request, "urlopen", lambda request, timeout: (observed.append(request) or _Response()))
    common.best_effort_failure(
        output, "enospc-run", "http://service/api/simulation/events", "disk full", "trace.jsonl"
    )
    assert "CSF_EVENT" in capsys.readouterr().out
    payload = json.loads(observed[-1].data)
    assert payload["events"][0]["status"]["phase"] == "failed"
    assert payload["events"][0]["status"]["message"] == "disk full"


def test_vendor_exception_survives_close_and_partial_upload_enospc(monkeypatch, tmp_path):
    monkeypatch.setattr(common.Evidence, "close", lambda _self: (_ for _ in ()).throw(OSError(28, "close ENOSPC")))
    monkeypatch.setattr(common, "upload_artifacts", lambda *_args, **_kwargs: (_ for _ in ()).throw(OSError(28, "upload ENOSPC")))
    with pytest.raises(RuntimeError, match="vendor unavailable"):
        isaac_rigidbody.run(Namespace(
            output=tmp_path / "preserve-original", run_id="original-error", progress_url=None,
            artifact_uri="s3://bucket/prefix", steps=1, seed=7, max_wall_seconds=10,
        ), _VendorFailure())


def test_completion_is_announced_only_after_artifacts_finish(tmp_path, capsys):
    output = tmp_path / "ordered-terminal"
    evidence = common.Evidence(output, "run-1", None)
    evidence.status("completed", "done", "manifest.json", forward=False, announce=False)
    evidence.close()
    assert "CSF_EVENT" not in capsys.readouterr().out
    client = _RecordingS3()
    common.upload_artifacts(output, "s3://bucket/prefix/run-1", client=client)
    assert client.uploads[-1][0] == "upload-receipt.json"
    common.announce_terminal(output, "run-1", None)
    announced = capsys.readouterr().out
    assert '"phase":"completed"' in announced
