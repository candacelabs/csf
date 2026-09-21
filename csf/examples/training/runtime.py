"""Bounded JSONL transport to the actual Go admission and execution runtime."""

from __future__ import annotations

import os
from pathlib import Path
import selectors
import subprocess
import time

from google.protobuf import json_format

from contract import encode, pb


class Runtime:
    def __init__(self, command: list[str], stderr_path: Path, timeout: float = 10.0):
        self.timeout = timeout
        self.stderr = stderr_path.open("ab")
        self.process = subprocess.Popen(
            command, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=self.stderr
        )
        self.selector = selectors.DefaultSelector()
        self.selector.register(self.process.stdout, selectors.EVENT_READ)
        self.buffer = b""
        self.epoch = 0

    def request(self, request):
        payload = (encode(request) + "\n").encode()
        if len(payload) > 1_048_576:
            raise ValueError("request exceeds the one MiB transport limit")
        self.process.stdin.write(payload)
        self.process.stdin.flush()
        deadline = time.monotonic() + self.timeout
        while b"\n" not in self.buffer:
            remaining = deadline - time.monotonic()
            if remaining <= 0 or not self.selector.select(remaining):
                raise TimeoutError("runtime response deadline exceeded")
            chunk = os.read(self.process.stdout.fileno(), 65536)
            if not chunk:
                raise RuntimeError(f"runtime exited with status {self.process.poll()}")
            self.buffer += chunk
            if len(self.buffer) > 1_048_576:
                raise RuntimeError("runtime response exceeds one MiB")
        line, self.buffer = self.buffer.split(b"\n", 1)
        response = json_format.Parse(
            line.decode(), pb.RuntimeResponse(), ignore_unknown_fields=False
        )
        if response.error:
            raise ValueError(response.error)
        return response

    def compile(self, controller):
        response = self.request(
            pb.RuntimeRequest(kind=pb.REQUEST_KIND_COMPILE, controller=controller)
        )
        if not response.HasField("program"):
            raise ValueError("compile returned no program")
        return response.program

    def activate(self, controller) -> int:
        next_epoch = self.epoch + 1
        response = self.request(
            pb.RuntimeRequest(
                kind=pb.REQUEST_KIND_ACTIVATE, controller=controller, epoch=next_epoch
            )
        )
        if response.epoch != next_epoch:
            raise ValueError("activation returned an unexpected epoch")
        self.epoch = next_epoch
        return self.epoch

    def reset(self) -> None:
        next_epoch = self.epoch + 1
        response = self.request(
            pb.RuntimeRequest(kind=pb.REQUEST_KIND_RESET, epoch=next_epoch)
        )
        if response.epoch != next_epoch:
            raise ValueError("reset returned an unexpected epoch")
        self.epoch = next_epoch

    def step(self, observation, tick: int):
        response = self.request(
            pb.RuntimeRequest(
                kind=pb.REQUEST_KIND_STEP, observation=observation, tick=tick
            )
        )
        if not response.HasField("action"):
            raise ValueError("step returned no action")
        if response.action.epoch != self.epoch:
            raise ValueError("action epoch differs from the active controller")
        if not (-1000 <= response.action.steering <= 1000):
            raise ValueError("runtime steering exceeds numeric profile")
        if not (-1000 <= response.action.acceleration <= 1000):
            raise ValueError("runtime acceleration exceeds numeric profile")
        return response.action

    def close(self) -> None:
        self.selector.close()
        self.process.stdin.close()
        try:
            self.process.wait(timeout=2)
        except subprocess.TimeoutExpired:
            self.process.terminate()
            try:
                self.process.wait(timeout=2)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait()
        self.process.stdout.close()
        self.stderr.close()

    def __enter__(self):
        return self

    def __exit__(self, *_):
        self.close()
