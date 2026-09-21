"""Flush canonical metric declarations and observations for a live dashboard."""

from datetime import datetime, timezone
import math
from pathlib import Path

from contract import encode, pb


METRICS = {
    "episode_loss": ("score", "Fixed normalized tracking objective; lower is better, including lane departure penalty."),
    "lane_departures": ("count", "Episodes where the vehicle centre crossed the declared lane boundary."),
    "runtime_fallbacks": ("count", "Actions marked as fallback by the Go runtime in an episode."),
    "fault_contract_mismatches": ("count", "Observed fallback decisions differing from the injected fault expectation."),
    "episode_duration_seconds": ("seconds", "Observed wall time to run one simulated episode."),
    "candidate_train_loss": ("score", "Mean fixed objective over the predeclared training seeds."),
    "candidate_validation_loss": ("score", "Mean fixed objective over the separate validation seeds."),
    "best_validation_loss": ("score", "Validation score of the current selected candidate, never computed from held-out tests."),
    "candidates_evaluated": ("count", "Cumulative completed optimization candidates, excluding the invalid admission canary."),
    "promotions": ("count", "Cumulative promotions under the immutable validation acceptance rule."),
    "external_spend_usd": ("USD", "Actual externally billed resources used by this local CPU experiment; no paid APIs are called."),
}


class Events:
    def __init__(self, path: Path, run_id: str):
        self.file = path.open("x", buffering=1)
        self.run_id = run_id
        for name, (unit, description) in METRICS.items():
            self.emit(definition=pb.MetricDefinition(name=name, unit=unit, description=description))

    def emit(self, **payload) -> None:
        event = pb.ResearchEvent(
            schema_version=1,
            recorded_at=datetime.now(timezone.utc).isoformat().replace("+00:00", "Z"),
            **payload,
        )
        self.file.write(encode(event) + "\n")
        self.file.flush()

    def status(self, phase: str, message: str, evidence_path: str = "") -> None:
        self.emit(status=pb.RunStatus(
            run_id=self.run_id, phase=phase, message=message, evidence_path=evidence_path
        ))

    def metric(self, name: str, value: float, step: int, candidate: str = "", split: str = "") -> None:
        if name not in METRICS:
            raise ValueError(f"unregistered metric: {name}")
        if not math.isfinite(value):
            raise ValueError(f"non-finite metric: {name}")
        self.emit(measurement=pb.Measurement(
            metric=name, value=value, step=step, run_id=self.run_id,
            candidate_id=candidate, split=split,
        ))

    def close(self) -> None:
        self.file.close()
