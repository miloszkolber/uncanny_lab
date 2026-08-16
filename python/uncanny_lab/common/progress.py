"""Stable newline-delimited JSON protocol used by the Go control plane."""

from __future__ import annotations

import json
import sys
import threading
from pathlib import Path
from typing import Any, Callable


def emit(event: str, **fields: Any) -> None:
    payload = {"event": event, **fields}
    print(json.dumps(payload, separators=(",", ":")), flush=True)


def fail(code: str, message: str) -> None:
    emit("error", code=code, message=message)


def diagnostic(message: str) -> None:
    print(message, file=sys.stderr, flush=True)


class PreviewWriter:
    """Writes preview PNGs off the optimization critical path.

    A single daemon thread encodes and stores preview frames and emits the
    matching preview events, so PNG encoding does not stall the optimizer.
    The optimizer thread only waits when the pending backlog exceeds
    max_pending, which also bounds memory. stop() drains the backlog before
    the final "completed" event, preserving event ordering for the control
    plane (which validates that preview artifacts exist when their events
    arrive).
    """

    def __init__(self, max_pending: int = 16) -> None:
        self._max_pending = max_pending
        self._queue: list[tuple[Path, int, Callable[[], None]]] = []
        self._condition = threading.Condition()
        self._closed = False
        self._thread: threading.Thread | None = None

    def start(self) -> None:
        self._thread = threading.Thread(target=self._run, name="preview-writer", daemon=True)
        self._thread.start()

    def submit(self, path: Path, step: int, task: Callable[[], None]) -> None:
        with self._condition:
            while len(self._queue) >= self._max_pending and not self._closed:
                self._condition.wait(timeout=0.1)
            if self._closed:
                return
            self._queue.append((path, step, task))
            self._condition.notify()

    def stop(self) -> None:
        with self._condition:
            self._closed = True
            self._condition.notify()
        if self._thread is not None:
            self._thread.join(timeout=60)
            self._thread = None

    def _run(self) -> None:
        while True:
            with self._condition:
                while not self._queue and not self._closed:
                    self._condition.wait()
                if not self._queue:
                    return
                path, step, task = self._queue.pop(0)
            try:
                task()
            except Exception as error:  # A failed preview must not kill the run.
                diagnostic(f"preview write failed for {path.name}: {error}")
                continue
            emit("preview", step=step, path=f"previews/{path.name}")
