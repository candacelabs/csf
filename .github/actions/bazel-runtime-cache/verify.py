"""Validate a restored Bazel dependency cache completion receipt."""

import json
import os
from pathlib import Path
import re


def main() -> None:
    matched = os.environ.get("MATCHED_KEY", "")
    stage = os.environ["EXPECTED_STAGE"]
    graph = os.environ["EXPECTED_GRAPH"]
    receipt_path = Path(os.environ["GITHUB_WORKSPACE"]) / ".git/csf-bazel-runtime/completed.json"
    valid = False
    try:
        prefix = f"csf-bazel-runtime-v2-{os.environ['RUNNER_OS']}-{os.environ['RUNNER_ARCH']}-{stage}-{graph}-"
        if not re.fullmatch(re.escape(prefix) + r"[0-9]+-[1-9][0-9]*", matched):
            raise ValueError("cache key identity mismatch")
        receipt = json.loads(receipt_path.read_text())
        valid = receipt == {"stage": stage, "graph": graph, "cache_key": matched}
    except (KeyError, OSError, ValueError, json.JSONDecodeError):
        valid = False
    if valid:
        print("valid=true")
    elif os.environ.get("VALIDATION_MODE") == "restore":
        raise SystemExit("restored Bazel cache is not a completed matching graph")
    else:
        print("valid=false")
    with open(os.environ["GITHUB_OUTPUT"], "a", encoding="utf-8") as output:
        output.write(f"valid={'true' if valid else 'false'}\n")


if __name__ == "__main__":
    main()
