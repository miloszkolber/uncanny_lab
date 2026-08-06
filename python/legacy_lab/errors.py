"""Stable worker failures understood by the control plane."""

from __future__ import annotations


class WorkerError(Exception):
    VALID_CODES = {
        "MODEL_MISSING", "MODEL_INVALID", "INPUT_DECODE_FAILED", "OUT_OF_MEMORY",
        "UNSUPPORTED_PRECISION", "INVALID_PARAMETERS", "UNSUPPORTED_MODEL",
    }

    def __init__(self, code: str, message: str) -> None:
        if code not in self.VALID_CODES:
            raise ValueError(f"unsupported worker error code: {code}")
        super().__init__(message)
        self.code = code
        self.message = message


def invalid(message: str) -> WorkerError:
    return WorkerError("INVALID_PARAMETERS", message)
