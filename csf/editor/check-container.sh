#!/usr/bin/env bash
# Disposable Python consumer environment; invoked by both CI repositories.
set -Eeuo pipefail
editor_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
python3 -m venv /tmp/venv
/tmp/venv/bin/python -m pip install -r "$editor_root/requirements-test.txt"
export CSF_HIGHLIGHT_PYTHON=/tmp/venv/bin/python
bash "$editor_root/check.sh"
