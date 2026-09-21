#!/usr/bin/env python3
"""Own the vendor simulator process for one containerized worker run."""

from __future__ import annotations

import glob
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import time

HERE = Path(__file__).resolve().parent


def wait_for_port(process: subprocess.Popen, host: str, port: int, timeout: float) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if process.poll() is not None:
            raise RuntimeError(f"CARLA server exited before readiness with code {process.returncode}")
        try:
            with socket.create_connection((host, port), timeout=1):
                return
        except OSError:
            time.sleep(0.25)
    raise TimeoutError(f"CARLA server did not listen on {host}:{port} within {timeout} seconds")


def run_carla() -> int:
    for distribution in glob.glob("/workspace/PythonAPI/carla/dist/carla-0.9.16*.egg"):
        sys.path.insert(0, distribution)
    server = subprocess.Popen(
        ["/workspace/CarlaUE4.sh", "-RenderOffScreen", "-nosound", "-quality-level=Low", "-carla-rpc-port=2000"],
        start_new_session=True,
    )
    try:
        wait_for_port(server, "127.0.0.1", 2000, float(os.environ.get("CSF_SERVER_TIMEOUT", "120")))
        from carla_waypoint import main
        sys.argv = [sys.argv[0], *sys.argv[2:]]
        main()
        return 0
    finally:
        if server.poll() is None:
            os.killpg(server.pid, signal.SIGTERM)
            try:
                server.wait(timeout=15)
            except subprocess.TimeoutExpired:
                os.killpg(server.pid, signal.SIGKILL)
                server.wait(timeout=5)


def run_isaac() -> None:
    os.execv("/isaac-sim/python.sh", ["/isaac-sim/python.sh", str(HERE / "isaac_rigidbody.py"), *sys.argv[2:]])


if __name__ == "__main__":
    if len(sys.argv) < 2 or sys.argv[1] not in ("carla", "isaac"):
        raise SystemExit("usage: container_entrypoint.py carla|isaac [worker flags]")
    raise SystemExit(run_carla() if sys.argv[1] == "carla" else run_isaac())
