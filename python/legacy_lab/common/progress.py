"""Stable newline-delimited JSON protocol used by the Go control plane."""

from __future__ import annotations

import json
import sys
from typing import Any


def emit(event: str, **fields: Any) -> None:
    payload = {"event": event, **fields}
    print(json.dumps(payload, separators=(",", ":")), flush=True)


def fail(code: str, message: str) -> None:
    emit("error", code=code, message=message)


def diagnostic(message: str) -> None:
    print(message, file=sys.stderr, flush=True)
