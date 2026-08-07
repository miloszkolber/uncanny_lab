"""Local-only CLIP adapters. They never fetch weights or execute pickle code."""
from __future__ import annotations

from pathlib import Path
from typing import Any

from uncanny_lab.common.images import save_tensor_png
from uncanny_lab.common.models import load_state_dict, load_torchscript, local_file, require_torch
from uncanny_lab.common.progress import emit
from uncanny_lab.engines.base import Engine
from uncanny_lab.engines.vision import integer, number
from uncanny_lab.errors import WorkerError, invalid
from uncanny_lab.runtime.device import Runtime


def clip_parameters(parameters: dict[str, Any], *, generator: bool = False) -> dict[str, Any]:
    prompt = parameters.get("prompt")
    if not isinstance(prompt, str) or not prompt.strip() or len(prompt) > 500: raise invalid("prompt must be a non-empty string up to 500 characters")
    result = {"prompt": prompt, "clip_checkpoint": local_file(parameters.get("clip_checkpoint"), "clip_checkpoint", model=True), "width": integer(parameters.get("width"), "width", 32, 512, 256), "height": integer(parameters.get("height"), "height", 32, 512, 256), "iterations": integer(parameters.get("iterations"), "iterations", 1, 2000, 250), "learning_rate": number(parameters.get("learning_rate"), "learning_rate", 1e-5, 1.0, 0.05), "clip_model": str(parameters.get("clip_model", "ViT-B-32"))}
    if generator:
        result["generator_path"] = local_file(parameters.get("generator_path"), "generator_path", model=True)
        result["latent_channels"] = integer(parameters.get("latent_channels"), "latent_channels", 1, 4096, 256)
    return result


def open_clip_model(parameters: dict[str, Any], runtime: Runtime) -> tuple[Any, Any]:
    try:
        import open_clip
    except ImportError as error:
        raise WorkerError("UNSUPPORTED_MODEL", "deep-daze and CLIP adapters require optional open_clip_torch") from error
    try:
        # Build the packaged architecture without a pretrained tag, then load only local tensors.
        model, _, preprocess = open_clip.create_model_and_transforms(parameters["clip_model"], pretrained=None, device=runtime.device)
        state = load_state_dict(parameters["clip_checkpoint"], runtime.device)
        missing, _ = model.load_state_dict({key.removeprefix("module."): value for key, value in state.items()}, strict=False)
        if len(missing) > len(model.state_dict()) // 3:
            raise RuntimeError("checkpoint is missing most CLIP weights")
        tokens = open_clip.tokenize([parameters["prompt"]]).to(runtime.device)
        with require_torch().no_grad(): target = model.encode_text(tokens); target = target / target.norm(dim=-1, keepdim=True)
        return model.eval(), target
    except Exception as error:
        raise WorkerError("MODEL_INVALID", f"could not load local CLIP checkpoint: {error}") from error


class DeepDazeEngine(Engine):
    id, version = "deep-daze", "1.0.0"
    def validate(self, parameters: dict[str, Any]) -> dict[str, Any]: return clip_parameters(parameters)
    def generate(self, job: dict[str, Any], parameters: dict[str, Any], runtime: Runtime, job_dir: Path) -> None:
        library = require_torch(); runtime.seed(integer(job.get("seed"), "seed", 0, 2**63 - 1, 0))
        emit("model-loading", model=parameters["clip_checkpoint"].name); clip, target = open_clip_model(parameters, runtime)
        class SineLayer(library.nn.Module):
            def __init__(self, source: int, target_size: int, omega: float, first: bool = False) -> None:
                super().__init__(); self.linear = library.nn.Linear(source, target_size); self.omega = omega
                bound = 1 / source if first else (6 / source) ** 0.5 / omega
                with library.no_grad(): self.linear.weight.uniform_(-bound, bound)
            def forward(self, value: Any) -> Any: return library.sin(self.omega * self.linear(value))
        class Siren(library.nn.Module):
            def __init__(self) -> None:
                super().__init__(); self.layers = library.nn.Sequential(SineLayer(2, 128, 30, True), SineLayer(128, 128, 30), library.nn.Linear(128, 3), library.nn.Sigmoid())
            def forward(self, coords: Any) -> Any: return self.layers(coords)
        network = Siren().to(runtime.device)
        yy, xx = library.meshgrid(library.linspace(-1, 1, parameters["height"], device=runtime.device), library.linspace(-1, 1, parameters["width"], device=runtime.device), indexing="ij")
        coords = library.stack((xx, yy), -1).reshape(-1, 2); optimizer = library.optim.Adam(network.parameters(), lr=parameters["learning_rate"])
        emit("started", device=runtime.device, fallback=runtime.fallback)
        for step in range(1, parameters["iterations"] + 1):
            optimizer.zero_grad(set_to_none=True); image = network(coords).reshape(parameters["height"], parameters["width"], 3).permute(2, 0, 1).unsqueeze(0)
            # CLIP preprocessing varies by model. Its transform is intentionally not applied to tensors, so use its visual input size.
            size = getattr(clip.visual, "image_size", 224); size = (size, size) if isinstance(size, int) else size
            encoded = encode_image(clip, image, size); encoded = encoded / encoded.norm(dim=-1, keepdim=True)
            (-(encoded * target).sum()).backward(); optimizer.step()
            with library.no_grad(): image = network(coords).reshape(parameters["height"], parameters["width"], 3).permute(2, 0, 1).unsqueeze(0)
            emit("progress", step=step, total=parameters["iterations"])
            preview = job.get("preview", {}); every = max(1, int(preview.get("every_steps", 5)))
            if bool(preview.get("enabled", True)) and (step == 1 or step % every == 0 or step == parameters["iterations"]):
                relative = f"previews/{step:06d}.png"; save_tensor_png(job_dir / relative, image); emit("preview", step=step, path=relative)
        save_tensor_png(job_dir / "final.png", image); emit("completed", path="final.png", device=runtime.device)


class TorchScriptClipEngine(Engine):
    """Portable generator adapter. Unsupported latent interfaces fail explicitly."""
    def validate(self, parameters: dict[str, Any]) -> dict[str, Any]: return clip_parameters(parameters, generator=True)
    def generate(self, job: dict[str, Any], parameters: dict[str, Any], runtime: Runtime, job_dir: Path) -> None:
        library = require_torch(); runtime.seed(integer(job.get("seed"), "seed", 0, 2**63 - 1, 0)); emit("model-loading", model=parameters["generator_path"].name)
        generator = load_torchscript(parameters["generator_path"], runtime.device); clip, target = open_clip_model(parameters, runtime)
        latent = library.randn((1, parameters["latent_channels"]), device=runtime.device, requires_grad=True)
        try:
            probe = generator(latent)
        except Exception as error:
            raise WorkerError("UNSUPPORTED_MODEL", "TorchScript generator must accept a [1, latent_channels] tensor") from error
        if not hasattr(probe, "ndim") or probe.ndim != 4 or probe.shape[1] != 3: raise WorkerError("UNSUPPORTED_MODEL", "TorchScript generator must return a BCHW RGB image")
        optimizer = library.optim.Adam([latent], lr=parameters["learning_rate"]); emit("started", device=runtime.device, fallback=runtime.fallback)
        for step in range(1, parameters["iterations"] + 1):
            optimizer.zero_grad(set_to_none=True); generated = generator(latent); image = generated.sigmoid() if generated.detach().min().item() < 0 or generated.detach().max().item() > 1 else generated
            image = library.nn.functional.interpolate(image, size=(parameters["height"], parameters["width"]), mode="bicubic", align_corners=False)
            size = getattr(clip.visual, "image_size", 224); size = (size, size) if isinstance(size, int) else size
            encoded = encode_image(clip, image, size); encoded = encoded / encoded.norm(dim=-1, keepdim=True)
            (-(encoded * target).sum()).backward(); optimizer.step()
            with library.no_grad():
                generated = generator(latent); image = generated.sigmoid() if generated.min().item() < 0 or generated.max().item() > 1 else generated
                image = library.nn.functional.interpolate(image, size=(parameters["height"], parameters["width"]), mode="bicubic", align_corners=False)
            emit("progress", step=step, total=parameters["iterations"])
            preview = job.get("preview", {}); every = max(1, int(preview.get("every_steps", 5)))
            if bool(preview.get("enabled", True)) and (step == 1 or step % every == 0 or step == parameters["iterations"]):
                relative = f"previews/{step:06d}.png"; save_tensor_png(job_dir / relative, image); emit("preview", step=step, path=relative)
        save_tensor_png(job_dir / "final.png", image); emit("completed", path="final.png", device=runtime.device)


class VQGANClipEngine(TorchScriptClipEngine):
    id, version = "vqgan-clip", "1.0.0"


class BigSleepEngine(TorchScriptClipEngine):
    id, version = "big-sleep", "1.0.0"


def encode_image(clip: Any, image: Any, size: Any) -> Any:
    library = require_torch()
    value = library.nn.functional.interpolate(image, size=size, mode="bicubic", align_corners=False)
    mean = getattr(clip.visual, "image_mean", (0.48145466, 0.4578275, 0.40821073))
    std = getattr(clip.visual, "image_std", (0.26862954, 0.26130258, 0.27577711))
    mean_tensor = library.tensor(mean, device=value.device, dtype=value.dtype).view(1, 3, 1, 1)
    std_tensor = library.tensor(std, device=value.device, dtype=value.dtype).view(1, 3, 1, 1)
    return clip.encode_image((value - mean_tensor) / std_tensor)
