"""Use the generated protobuf owner; never maintain a second set of wire DTOs."""

from __future__ import annotations

import hashlib
import json
from pathlib import Path
import sys

from google.protobuf import json_format

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT.parent / "tools" / "codegen" / "generated" / "python"))
try:
    from candace.brainspine.v1 import brainspine_pb2 as pb
except ImportError as error:
    raise ImportError(
        "Generate candace/csf/tools/codegen/generated/python from "
        "candace/proto/candace/brainspine/v1/brainspine.proto before this experiment."
    ) from error


def to_dict(message):
    return json_format.MessageToDict(message, preserving_proto_field_name=True)


def encode(message) -> str:
    return json.dumps(to_dict(message), sort_keys=True, separators=(",", ":"))


def read_message(path: Path, message):
    return json_format.Parse(path.read_text(), message, ignore_unknown_fields=False)


def write_message(path: Path, message) -> None:
    path.write_text(json.dumps(to_dict(message), sort_keys=True, indent=2) + "\n")


def digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()
