from __future__ import annotations

import hashlib
import math
import time
from pathlib import Path
from typing import Any

from uncanny_lab.common.images import write_rgb_png
from uncanny_lab.common.progress import emit
from uncanny_lab.engines.base import Engine
from uncanny_lab.runtime.device import Runtime, torch


class TestPatternEngine(Engine):
    id = "test-pattern"
    version = "0.1.0"

    def validate(self, parameters: dict[str, Any]) -> dict[str, Any]:
        normalized = {
            "prompt": str(parameters.get("prompt", "legacy signal"))[:500],
            "width": _bounded_integer(parameters.get("width", 256), "width", 64, 1024),
            "height": _bounded_integer(parameters.get("height", 256), "height", 64, 1024),
            "iterations": _bounded_integer(parameters.get("iterations", 30), "iterations", 1, 5000),
        }
        return normalized

    def generate(self, job: dict[str, Any], parameters: dict[str, Any], runtime: Runtime, job_dir: Path) -> None:
        width, height, total = parameters["width"], parameters["height"], parameters["iterations"]
        preview = job.get("preview", {})
        preview_every = max(1, int(preview.get("every_steps", 5)))
        keep_previews = bool(preview.get("enabled", True))
        prompt_phase = int.from_bytes(hashlib.sha256(parameters["prompt"].encode()).digest()[:4], "little") / 2**32
        runtime.seed(int(job["seed"]))
        emit("started", device=runtime.device, fallback=runtime.fallback)

        pixels = b""
        for step in range(1, total + 1):
            pixels = self._frame(width, height, step, total, prompt_phase, runtime)
            emit("progress", step=step, total=total)
            if keep_previews and (step == 1 or step % preview_every == 0 or step == total):
                relative = f"previews/{step:06d}.png"
                write_rgb_png(job_dir / relative, width, height, pixels)
                emit("preview", step=step, path=relative)
            time.sleep(0.02)

        write_rgb_png(job_dir / "final.png", width, height, pixels)
        emit("completed", path="final.png", device=runtime.device)

    @staticmethod
    def _frame(width: int, height: int, step: int, total: int, phase: float, runtime: Runtime) -> bytes:
        progress = step / total
        if torch is not None:
            y = torch.linspace(-1.0, 1.0, height, device=runtime.device).view(height, 1)
            x = torch.linspace(-1.0, 1.0, width, device=runtime.device).view(1, width)
            with runtime.autocast():
                radial = torch.sqrt(x * x + y * y)
                angle = torch.atan2(y, x)
                red = (torch.sin((radial * (7 + progress * 19) - progress * 8 + phase * 6.28) * math.pi) + 1) * 127.5
                green = (torch.sin(angle * (2 + progress * 4) + radial * 12 - phase * 3) + 1) * 127.5
                blue = (torch.cos((x - y) * (4 + progress * 9) + phase * 9) + 1) * 127.5
                image = torch.stack((red.expand(height, width), green.expand(height, width), blue.expand(height, width)), dim=-1)
                pixels = bytes(image.clamp(0, 255).to(torch.uint8).cpu().contiguous().view(-1).tolist())
            runtime.synchronize()
            return pixels

        output = bytearray(width * height * 3)
        offset = 0
        for row in range(height):
            y = row / max(height - 1, 1) * 2 - 1
            for column in range(width):
                x = column / max(width - 1, 1) * 2 - 1
                radial, angle = math.hypot(x, y), math.atan2(y, x)
                output[offset] = int((math.sin((radial * (7 + progress * 19) - progress * 8 + phase * 6.28) * math.pi) + 1) * 127.5)
                output[offset + 1] = int((math.sin(angle * (2 + progress * 4) + radial * 12 - phase * 3) + 1) * 127.5)
                output[offset + 2] = int((math.cos((x - y) * (4 + progress * 9) + phase * 9) + 1) * 127.5)
                offset += 3
        return bytes(output)


def _bounded_integer(value: Any, name: str, minimum: int, maximum: int) -> int:
    if isinstance(value, bool):
        raise ValueError(f"{name} must be an integer")
    try:
        result = int(value)
    except (TypeError, ValueError) as error:
        raise ValueError(f"{name} must be an integer") from error
    if result < minimum or result > maximum:
        raise ValueError(f"{name} must be between {minimum} and {maximum}")
    return result
