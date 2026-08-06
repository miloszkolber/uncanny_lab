from __future__ import annotations

from abc import ABC, abstractmethod
from pathlib import Path
from typing import Any

from legacy_lab.runtime.device import Runtime


class Engine(ABC):
    id: str
    version: str

    @abstractmethod
    def validate(self, parameters: dict[str, Any]) -> dict[str, Any]:
        """Return normalized parameters or raise ValueError."""

    @abstractmethod
    def generate(self, job: dict[str, Any], parameters: dict[str, Any], runtime: Runtime, job_dir: Path) -> None:
        """Generate artifacts and emit worker events."""
