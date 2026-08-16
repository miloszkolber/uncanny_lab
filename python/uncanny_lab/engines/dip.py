"""Small, checkpoint-free Deep Image Prior reconstruction engine."""
from __future__ import annotations

from functools import partial
from pathlib import Path
from typing import Any

from uncanny_lab.common.images import load_image, save_tensor_png
from uncanny_lab.common.models import local_file, require_torch
from uncanny_lab.common.progress import PreviewWriter, emit
from uncanny_lab.engines.base import Engine
from uncanny_lab.engines.vision import integer, number
from uncanny_lab.errors import invalid
from uncanny_lab.runtime.device import Runtime


def build_network(library: Any, device: str) -> Any:
    return library.nn.Sequential(library.nn.Conv2d(32, 64, 3, padding=1), library.nn.ReLU(), library.nn.Conv2d(64, 64, 3, padding=1), library.nn.ReLU(), library.nn.Conv2d(64, 32, 3, padding=1), library.nn.ReLU(), library.nn.Conv2d(32, 3, 1), library.nn.Sigmoid()).to(device)


class DeepImagePriorEngine(Engine):
    id, version = "deep-image-prior", "1.0.0"

    def validate(self, parameters: dict[str, Any]) -> dict[str, Any]:
        result = {"source_image": local_file(parameters.get("source_image", parameters.get("source")), "source_image"), "width": integer(parameters.get("width"), "width", 32, 512, 256), "height": integer(parameters.get("height"), "height", 32, 512, 256), "iterations": integer(parameters.get("iterations"), "iterations", 1, 3000, 500), "learning_rate": number(parameters.get("learning_rate"), "learning_rate", 1e-5, 0.1, 0.01), "noise_std": number(parameters.get("noise_std"), "noise_std", 0, 0.2, 0.03)}
        if result["width"] * result["height"] > 262144: raise invalid("DIP image area must not exceed 262144 pixels")
        return result

    def generate(self, job: dict[str, Any], parameters: dict[str, Any], runtime: Runtime, job_dir: Path) -> None:
        library = require_torch(); runtime.seed(integer(job.get("seed"), "seed", 0, 2**63 - 1, 0))
        target = load_image(parameters["source_image"], parameters["width"], parameters["height"], runtime.device)
        net = build_network(library, runtime.device)
        noise = library.rand((1, 32, parameters["height"], parameters["width"]), device=runtime.device)
        optimizer = library.optim.Adam(net.parameters(), lr=parameters["learning_rate"])
        writer = PreviewWriter()
        writer.start()
        emit("started", device=runtime.device, fallback=runtime.fallback)
        output = target
        try:
            for step in range(1, parameters["iterations"] + 1):
                optimizer.zero_grad(set_to_none=True)
                output = net(noise + library.randn_like(noise) * parameters["noise_std"])
                library.nn.functional.mse_loss(output, target).backward(); optimizer.step()
                with library.no_grad(): output = net(noise)
                emit("progress", step=step, total=parameters["iterations"])
                preview = job.get("preview", {}); every = max(1, int(preview.get("every_steps", 5)))
                if bool(preview.get("enabled", True)) and (step == 1 or step % every == 0 or step == parameters["iterations"]):
                    relative = f"previews/{step:06d}.png"; writer.submit(job_dir / relative, step, partial(save_tensor_png, job_dir / relative, output))
        finally:
            writer.stop()
        with library.no_grad(): output = net(noise)
        save_tensor_png(job_dir / "final.png", output); emit("completed", path="final.png", device=runtime.device)
