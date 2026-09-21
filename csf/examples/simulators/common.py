"""Shared bounded evidence and progress reporting for simulator workers."""

from __future__ import annotations

from datetime import datetime, timezone
import hashlib
import json
import math
from pathlib import Path
import struct
import sys
import urllib.error
import urllib.request
import zlib

from google.protobuf import json_format

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT.parent / "tools" / "codegen" / "generated" / "python"))
from candace.brainspine.v1 import brainspine_pb2 as pb


METRICS = {
    "simulation_steps_completed": ("count", "Absolute number of simulator physics steps completed by this run."),
    "simulator_x_metres": ("metres", "Observed simulator world-frame x position."),
    "simulator_y_metres": ("metres", "Observed simulator world-frame y position."),
    "simulator_z_metres": ("metres", "Observed simulator world-frame z position."),
    "simulator_speed_mps": ("metres/second", "Observed linear speed magnitude."),
    "simulator_tracking_error_metres": ("metres", "Distance from the current CARLA map waypoint."),
}
PROGRESS_ATTEMPTS = 3
PROGRESS_TIMEOUT_SECONDS = 10


def encode(message) -> str:
    value = json_format.MessageToDict(message, preserving_proto_field_name=True)
    return json.dumps(value, sort_keys=True, separators=(",", ":"))


def sha256(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_json(path: Path, value: object) -> None:
    path.write_text(json.dumps(value, sort_keys=True, indent=2, allow_nan=False) + "\n")


def write_rgb_png(path: Path, width: int, height: int, pixels: bytes) -> None:
    """Encode tightly packed vendor RGB bytes without another runtime dependency."""
    if len(pixels) != width * height * 3:
        raise ValueError(f"RGB payload has {len(pixels)} bytes; expected {width * height * 3}")

    def chunk(kind: bytes, payload: bytes) -> bytes:
        body = kind + payload
        return struct.pack(">I", len(payload)) + body + struct.pack(">I", zlib.crc32(body) & 0xFFFFFFFF)

    rows = b"".join(
        b"\x00" + pixels[offset:offset + width * 3]
        for offset in range(0, len(pixels), width * 3)
    )
    path.write_bytes(
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
        + chunk(b"IDAT", zlib.compress(rows))
        + chunk(b"IEND", b"")
    )


class FrameEvidence:
    """Retain an honest index for frames produced by a simulator camera."""

    def __init__(self, output: Path, simulator: str, capture_every: int, width: int, height: int):
        self.output = output
        self.simulator = simulator
        self.capture_every = capture_every
        self.width = width
        self.height = height
        self.frames: list[dict] = []

    @property
    def enabled(self) -> bool:
        return self.capture_every > 0

    def path(self, step: int) -> Path:
        return self.output / f"camera-step-{step:06d}.png"

    def record(self, step: int, simulation_seconds: float, path: Path, **vendor_clock) -> None:
        self.frames.append({
            "step": step,
            "simulation_seconds": simulation_seconds,
            "wall_recorded_at": datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
            "width": self.width,
            "height": self.height,
            "path": path.relative_to(self.output).as_posix(),
            "sha256": sha256(path),
            **{key: value for key, value in vendor_clock.items() if value is not None},
        })

    def write(self, status: str, error: str = "") -> None:
        if not self.enabled:
            return
        value = {
            "format": "brain-spine-simulator-frames-v1",
            "status": status,
            "simulator": self.simulator,
            "capture_every_steps": self.capture_every,
            "dimensions": {"width": self.width, "height": self.height},
            "clock_definition": {
                "simulation_seconds": "fixed-step simulator time after the recorded physics step",
                "wall_recorded_at": "UTC wall clock after the vendor frame was written",
            },
            "frames": self.frames,
        }
        if error:
            value["error"] = error
        write_json(self.output / "frames.json", value)


def mark_manifest_failed(output: Path, error: Exception) -> None:
    path = output / "manifest.json"
    if not path.exists():
        return
    value = json.loads(path.read_text())
    value["status"] = "failed"
    value["error"] = str(error)
    write_json(path, value)


def best_effort_mark_manifest_failed(output: Path, error: Exception) -> None:
    try:
        mark_manifest_failed(output, error)
    except OSError as write_error:
        print(f"failed to update manifest: {write_error}", file=sys.stderr, flush=True)


class ProgressDeliveryError(RuntimeError):
    """The optional live progress endpoint did not accept an event."""


class ArtifactUploadError(RuntimeError):
    """One or more finalized artifacts were not uploaded."""


def _post_events(progress_url: str, run_id: str, events: list[dict]) -> None:
    """Post one immutable envelope with bounded retries."""
    data = json.dumps({"runId": run_id, "events": events}, separators=(",", ":")).encode()
    last_error = None
    for _attempt in range(PROGRESS_ATTEMPTS):
        request = urllib.request.Request(
            progress_url, data=data, headers={"Content-Type": "application/json"}, method="POST"
        )
        try:
            with urllib.request.urlopen(request, timeout=PROGRESS_TIMEOUT_SECONDS) as response:
                if response.status < 200 or response.status >= 300:
                    raise ProgressDeliveryError(f"progress endpoint returned HTTP {response.status}")
            return
        except (OSError, ProgressDeliveryError, urllib.error.HTTPError,
                urllib.error.URLError) as error:
            last_error = error
    raise ProgressDeliveryError(str(last_error)) from last_error


class Evidence:
    def __init__(self, output: Path, run_id: str, progress_url: str | None):
        output.mkdir(parents=True, exist_ok=False)
        self.output = output
        self.run_id = run_id
        self.progress_url = progress_url
        self.events = (output / "events.jsonl").open("x", buffering=1)
        self.trace = (output / "trace.jsonl").open("x", buffering=1)
        self.failures = (output / "progress-errors.jsonl").open("x", buffering=1)

    def _events(self, payloads: list[dict], *, forward: bool = True,
                announce: bool = True) -> None:
        bodies = []
        for payload in payloads:
            event = pb.ResearchEvent(
                schema_version=1,
                recorded_at=datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
                **payload,
            )
            body = encode(event)
            bodies.append(body)
            self.events.write(body + "\n")
            if announce:
                print("CSF_EVENT " + body, flush=True)
        if forward and self.progress_url:
            try:
                _post_events(self.progress_url, self.run_id, [json.loads(body) for body in bodies])
            except ProgressDeliveryError as error:
                failure = {"error": str(error), "events": [json.loads(body) for body in bodies]}
                self.failures.write(json.dumps(failure, sort_keys=True) + "\n")
                print(f"progress delivery failed: {error}", file=sys.stderr, flush=True)
                raise

    def _event(self, *, forward: bool = True, announce: bool = True, **payload) -> None:
        self._events([payload], forward=forward, announce=announce)

    def definitions(self, names: tuple[str, ...]) -> None:
        self._events([
            {"definition": pb.MetricDefinition(name=name, unit=METRICS[name][0], description=METRICS[name][1])}
            for name in names
        ])

    def status(self, phase: str, message: str, evidence_path: str = "", *,
               forward: bool = True, announce: bool = True) -> None:
        self._event(
            forward=forward,
            announce=announce,
            status=pb.RunStatus(
                run_id=self.run_id, phase=phase, message=message, evidence_path=evidence_path
            ),
        )

    def metric(self, name: str, value: float, step: int, simulator: str) -> None:
        self.metrics(((name, value),), step, simulator)

    def metrics(self, values, step: int, simulator: str) -> None:
        payloads = []
        for name, value in values:
            if name not in METRICS or not math.isfinite(value):
                raise ValueError(f"invalid metric {name}: {value}")
            payloads.append({"measurement": pb.Measurement(
                metric=name, value=value, step=step, run_id=self.run_id,
                candidate_id=simulator, split="native",
            )})
        self._events(payloads)

    def cancellation_requested(self) -> bool:
        if not self.progress_url:
            return False
        inspect_url = self.progress_url.rsplit("/", 1)[0] + "/inspect"
        body = json.dumps({"runId": self.run_id}, separators=(",", ":")).encode()
        request = urllib.request.Request(
            inspect_url, data=body, headers={"Content-Type": "application/json"}, method="POST"
        )
        try:
            with urllib.request.urlopen(request, timeout=3) as response:
                payload = json.loads(response.read())
        except (OSError, ValueError, urllib.error.HTTPError, urllib.error.URLError) as error:
            failure = {"error": f"cancellation inspection failed: {error}", "run_id": self.run_id}
            self.failures.write(json.dumps(failure, sort_keys=True) + "\n")
            print(failure["error"], file=sys.stderr, flush=True)
            raise ProgressDeliveryError(failure["error"]) from error
        run = payload.get("run", payload)
        return bool(run.get("cancellationRequested", run.get("cancellation_requested", False)))

    def step(self, value: dict) -> None:
        self.trace.write(json.dumps(value, sort_keys=True, allow_nan=False) + "\n")

    def close(self) -> None:
        self.events.close()
        self.trace.close()
        self.failures.close()


def upload_artifacts(output: Path, artifact_uri: str | None, *, client=None) -> list[dict]:
    """Upload retained files when explicitly configured with an S3 URI."""
    if not artifact_uri:
        return []
    uploaded = []
    receipt_path = output / "upload-receipt.json"
    try:
        if not artifact_uri.startswith("s3://"):
            raise ValueError("artifact-uri must use s3://")
        location = artifact_uri[5:]
        bucket, separator, prefix = location.partition("/")
        if not bucket or not separator or not prefix:
            raise ValueError("artifact-uri must include bucket and prefix")
        if client is None:
            import boto3
            client = boto3.client("s3")
        paths = sorted(item for item in output.iterdir() if item.is_file() and item != receipt_path)
        for path in paths:
            key = f"{prefix.rstrip('/')}/{path.name}"
            client.upload_file(str(path), bucket, key)
            uploaded.append({"path": path.name, "sha256": sha256(path), "uri": f"s3://{bucket}/{key}"})
        receipt = {"artifact_uri": artifact_uri, "status": "completed", "objects": uploaded}
        write_json(receipt_path, receipt)
        receipt_key = f"{prefix.rstrip('/')}/{receipt_path.name}"
        client.upload_file(str(receipt_path), bucket, receipt_key)
    except Exception as error:
        try:
            write_json(receipt_path, {
                "artifact_uri": artifact_uri, "status": "failed",
                "error": str(error), "objects": uploaded,
            })
        except OSError as receipt_error:
            print(f"failed to retain artifact failure receipt: {receipt_error}", file=sys.stderr, flush=True)
        raise ArtifactUploadError(str(error)) from error
    return uploaded


def report_artifact_failure(output: Path, run_id: str, progress_url: str | None, error: Exception) -> None:
    """Append and forward the authoritative failed terminal state after upload."""
    best_effort_failure(
        output, run_id, progress_url, f"artifact upload failed: {error}", "upload-receipt.json"
    )


def best_effort_failure(output: Path, run_id: str, progress_url: str | None,
                        message: str, evidence_path: str) -> None:
    """Report failure without masking the simulator or cleanup exception."""
    event = pb.ResearchEvent(
        schema_version=1,
        recorded_at=datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
        status=pb.RunStatus(
            run_id=run_id, phase="failed", message=message, evidence_path=evidence_path,
        ),
    )
    body = encode(event)
    try:
        with (output / "events.jsonl").open("a") as events:
            events.write(body + "\n")
    except OSError as write_error:
        print(f"failed to retain failure event: {write_error}", file=sys.stderr, flush=True)
    print("CSF_EVENT " + body, flush=True)
    if not progress_url:
        return
    try:
        _post_events(progress_url, run_id, [json.loads(body)])
    except ProgressDeliveryError as delivery_error:
        try:
            with (output / "progress-errors.jsonl").open("a") as failures:
                failures.write(json.dumps({"error": str(delivery_error), "event": json.loads(body)}, sort_keys=True) + "\n")
        except OSError:
            pass
        print(f"failed to forward artifact failure: {delivery_error}", file=sys.stderr, flush=True)


def best_effort_close(evidence: Evidence) -> None:
    try:
        evidence.close()
    except OSError as error:
        print(f"failed to close evidence files: {error}", file=sys.stderr, flush=True)


def best_effort_failure_upload(output: Path, artifact_uri: str | None) -> None:
    try:
        upload_artifacts(output, artifact_uri)
    except Exception as error:
        print(f"failed to upload partial failure evidence: {error}", file=sys.stderr, flush=True)


def announce_terminal(output: Path, run_id: str, progress_url: str | None) -> None:
    """Announce the retained terminal event only after artifacts succeeded."""
    body = (output / "events.jsonl").read_text().splitlines()[-1]
    event = json.loads(body)
    if event.get("status", {}).get("run_id") != run_id:
        raise RuntimeError("retained terminal event does not match run id")
    print("CSF_EVENT " + body, flush=True)
    if not progress_url:
        return
    _post_events(progress_url, run_id, [event])
