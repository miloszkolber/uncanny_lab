"""Device, precision, synchronization, and seed handling for every engine."""

from __future__ import annotations

import contextlib
import platform
import random
from dataclasses import dataclass
from typing import Any, ContextManager

from uncanny_lab.errors import WorkerError


try:
    import torch
except ImportError:  # The UI and protocol can be developed without the large runtime.
    torch = None  # type: ignore[assignment]


@dataclass(frozen=True)
class Runtime:
    requested_device: str
    device: str
    precision: str
    fallback: bool

    @classmethod
    def create(cls, requested_device: str, precision: str = "fp32") -> "Runtime":
        if requested_device not in {"xpu", "cpu"}:
            raise WorkerError("INVALID_PARAMETERS", "device must be xpu or cpu")
        if precision not in {"fp32", "fp16"}:
            raise WorkerError("UNSUPPORTED_PRECISION", "precision must be fp32 or fp16")
        xpu_available = bool(torch is not None and hasattr(torch, "xpu") and torch.xpu.is_available())
        selected = "xpu" if requested_device == "xpu" and xpu_available else "cpu"
        if precision == "fp16" and selected == "cpu":
            raise WorkerError("UNSUPPORTED_PRECISION", "fp16 is supported only on XPU")
        return cls(requested_device=requested_device, device=selected, precision=precision, fallback=selected != requested_device)

    def seed(self, value: int) -> None:
        random.seed(value)
        if torch is not None:
            torch.manual_seed(value)
            if self.device == "xpu":
                torch.xpu.manual_seed_all(value)

    def autocast(self) -> ContextManager[Any]:
        if torch is None or self.precision == "fp32":
            return contextlib.nullcontext()
        return torch.autocast(device_type=self.device, dtype=torch.float16)

    def synchronize(self) -> None:
        if torch is not None and self.device == "xpu":
            torch.xpu.synchronize()

    def report(self) -> dict[str, Any]:
        xpu_available = bool(torch is not None and hasattr(torch, "xpu") and torch.xpu.is_available())
        device_name = None
        if xpu_available:
            try:
                device_name = torch.xpu.get_device_name(0)
            except Exception:
                device_name = "Intel XPU"
        return {
            "available": torch is not None,
            "python_version": platform.python_version(),
            "torch_version": getattr(torch, "__version__", None),
            "xpu_available": xpu_available,
            "requested_device": self.requested_device,
            "device": self.device,
            "device_name": device_name or ("CPU" if self.device == "cpu" else None),
            "fallback": self.fallback,
            "precision": self.precision,
        }
