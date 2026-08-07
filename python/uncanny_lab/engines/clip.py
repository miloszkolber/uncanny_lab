"""Local-only CLIP-guided optimization engines."""

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


def clip_parameters(parameters: dict[str, Any]) -> dict[str, Any]:
    prompt = parameters.get("prompt")
    if not isinstance(prompt, str) or not prompt.strip() or len(prompt) > 500:
        raise invalid("prompt must be a non-empty string up to 500 characters")
    clip_model = str(parameters.get("clip_model", "ViT-B-32"))
    if clip_model != "ViT-B-32":
        raise invalid("clip_model must be ViT-B-32")
    return {
        "prompt": prompt,
        "clip_checkpoint": local_file(parameters.get("clip_checkpoint"), "clip_checkpoint", model=True),
        "clip_model": clip_model,
        "width": integer(parameters.get("width"), "width", 32, 512, 256),
        "height": integer(parameters.get("height"), "height", 32, 512, 256),
        "iterations": integer(parameters.get("iterations"), "iterations", 1, 2000, 250),
        "learning_rate": number(parameters.get("learning_rate"), "learning_rate", 1e-5, 1.0, 0.001),
    }


def open_clip_model(parameters: dict[str, Any], runtime: Runtime) -> tuple[Any, Any]:
    try:
        import open_clip
    except ImportError as error:
        raise WorkerError("UNSUPPORTED_MODEL", "CLIP engines require open_clip_torch") from error
    try:
        model, _, _ = open_clip.create_model_and_transforms(parameters["clip_model"], pretrained=None, device=runtime.device)
        state = load_state_dict(parameters["clip_checkpoint"], runtime.device)
        missing, _ = model.load_state_dict({key.removeprefix("module."): value for key, value in state.items()}, strict=False)
        if len(missing) > len(model.state_dict()) // 3:
            raise RuntimeError("checkpoint is missing most CLIP weights")
        freeze(model)
        tokens = open_clip.tokenize([parameters["prompt"]]).to(runtime.device)
        with require_torch().no_grad():
            target = model.encode_text(tokens)
            target = target / target.norm(dim=-1, keepdim=True)
        return model.eval(), target
    except WorkerError:
        raise
    except Exception as error:
        raise WorkerError("MODEL_INVALID", f"could not load local CLIP checkpoint: {error}") from error


def freeze(model: Any) -> Any:
    model.eval()
    for parameter in model.parameters():
        parameter.requires_grad_(False)
        parameter.grad = None
    return model


def encode_image(clip: Any, image: Any) -> Any:
    library = require_torch()
    size = getattr(clip.visual, "image_size", 224)
    size = (size, size) if isinstance(size, int) else size
    value = library.nn.functional.interpolate(image, size=size, mode="bicubic", align_corners=False)
    mean = getattr(clip.visual, "image_mean", (0.48145466, 0.4578275, 0.40821073))
    std = getattr(clip.visual, "image_std", (0.26862954, 0.26130258, 0.27577711))
    mean_tensor = library.tensor(mean, device=value.device, dtype=value.dtype).view(1, 3, 1, 1)
    std_tensor = library.tensor(std, device=value.device, dtype=value.dtype).view(1, 3, 1, 1)
    encoded = clip.encode_image((value - mean_tensor) / std_tensor)
    return encoded / encoded.norm(dim=-1, keepdim=True)


def display_image(generated: Any, width: int, height: int) -> Any:
    library = require_torch()
    image = (generated + 1) / 2 if generated.detach().min().item() < 0 else generated
    image = image.clamp(0, 1)
    return library.nn.functional.interpolate(image, size=(height, width), mode="bicubic", align_corners=False).clamp(0, 1)


def vector_quantize(latent: Any, codebook: Any) -> Any:
    """Nearest-neighbor codebook lookup with a straight-through gradient."""
    library = require_torch()
    if latent.ndim != 4 or codebook.ndim != 2 or latent.shape[1] != codebook.shape[1]:
        raise WorkerError("MODEL_INVALID", "VQGAN latent channels must match the codebook width")
    flat = latent.permute(0, 2, 3, 1).reshape(-1, latent.shape[1])
    distances = flat.square().sum(1, keepdim=True) + codebook.square().sum(1).unsqueeze(0) - 2 * flat @ codebook.t()
    indices = distances.argmin(1)
    quantized = library.nn.functional.embedding(indices, codebook).view(latent.shape[0], latent.shape[2], latent.shape[3], latent.shape[1]).permute(0, 3, 1, 2)
    return latent + (quantized - latent).detach()


def load_codebook(path: Path, device: str) -> Any:
    state = load_state_dict(path, device)
    preferred = ("embedding.weight", "quantize.embedding.weight", "codebook.weight")
    value = next((state[key] for key in preferred if key in state), None)
    if value is None:
        tensors = [item for item in state.values() if hasattr(item, "ndim") and item.ndim == 2]
        value = tensors[0] if len(tensors) == 1 else None
    if value is None or value.ndim != 2 or value.shape[0] < 2 or value.shape[1] < 1:
        raise WorkerError("MODEL_INVALID", "VQGAN codebook must contain one [codes, channels] embedding tensor")
    return value.to(device=device, dtype=require_torch().float32).detach()


def progress(job: dict[str, Any], job_dir: Path, image: Any, step: int, total: int) -> None:
    emit("progress", step=step, total=total)
    preview = job.get("preview", {})
    every = max(1, int(preview.get("every_steps", 5)))
    if bool(preview.get("enabled", True)) and (step == 1 or step % every == 0 or step == total):
        relative = f"previews/{step:06d}.png"
        save_tensor_png(job_dir / relative, image)
        emit("preview", step=step, path=relative)


class DeepDazeEngine(Engine):
    id, version = "deep-daze", "1.1.0"

    def validate(self, parameters: dict[str, Any]) -> dict[str, Any]:
        return clip_parameters(parameters)

    def generate(self, job: dict[str, Any], parameters: dict[str, Any], runtime: Runtime, job_dir: Path) -> None:
        library = require_torch()
        runtime.seed(integer(job.get("seed"), "seed", 0, 2**63 - 1, 0))
        emit("model-loading", model=parameters["clip_checkpoint"].name)
        clip, target = open_clip_model(parameters, runtime)

        class SineLayer(library.nn.Module):
            def __init__(self, source: int, target_size: int, omega: float, first: bool = False) -> None:
                super().__init__()
                self.linear, self.omega = library.nn.Linear(source, target_size), omega
                bound = 1 / source if first else (6 / source) ** 0.5 / omega
                with library.no_grad():
                    self.linear.weight.uniform_(-bound, bound)

            def forward(self, value: Any) -> Any:
                return library.sin(self.omega * self.linear(value))

        network = library.nn.Sequential(SineLayer(2, 128, 30, True), SineLayer(128, 128, 30), library.nn.Linear(128, 3), library.nn.Sigmoid()).to(runtime.device)
        yy, xx = library.meshgrid(library.linspace(-1, 1, parameters["height"], device=runtime.device), library.linspace(-1, 1, parameters["width"], device=runtime.device), indexing="ij")
        coords = library.stack((xx, yy), -1).reshape(-1, 2)
        optimizer = library.optim.Adam(network.parameters(), lr=parameters["learning_rate"])
        emit("started", device=runtime.device, fallback=runtime.fallback)
        for step in range(1, parameters["iterations"] + 1):
            optimizer.zero_grad(set_to_none=True)
            image = network(coords).reshape(parameters["height"], parameters["width"], 3).permute(2, 0, 1).unsqueeze(0)
            (-(encode_image(clip, image) * target).sum()).backward()
            optimizer.step()
            with library.no_grad():
                image = network(coords).reshape(parameters["height"], parameters["width"], 3).permute(2, 0, 1).unsqueeze(0)
            progress(job, job_dir, image, step, parameters["iterations"])
        save_tensor_png(job_dir / "final.png", image)
        emit("completed", path="final.png", device=runtime.device)


class VQGANClipEngine(Engine):
    id, version = "vqgan-clip", "1.1.0"

    def validate(self, parameters: dict[str, Any]) -> dict[str, Any]:
        result = clip_parameters(parameters)
        result.update({
            "decoder_path": local_file(parameters.get("decoder_path"), "decoder_path", model=True),
            "codebook_path": local_file(parameters.get("codebook_path"), "codebook_path", model=True),
            "latent_width": integer(parameters.get("latent_width"), "latent_width", 1, 128, 16),
            "latent_height": integer(parameters.get("latent_height"), "latent_height", 1, 128, 16),
            "commitment_weight": number(parameters.get("commitment_weight"), "commitment_weight", 0, 10, 0.1),
        })
        if result["latent_width"] * result["latent_height"] > 4096:
            raise invalid("VQGAN latent area must not exceed 4096 positions")
        return result

    def generate(self, job: dict[str, Any], parameters: dict[str, Any], runtime: Runtime, job_dir: Path) -> None:
        library = require_torch()
        runtime.seed(integer(job.get("seed"), "seed", 0, 2**63 - 1, 0))
        emit("model-loading", model=parameters["decoder_path"].name)
        decoder = freeze(load_torchscript(parameters["decoder_path"], runtime.device))
        codebook = load_codebook(parameters["codebook_path"], runtime.device)
        clip, target = open_clip_model(parameters, runtime)
        latent = library.randn((1, codebook.shape[1], parameters["latent_height"], parameters["latent_width"]), device=runtime.device, requires_grad=True)
        try:
            probe = decoder(vector_quantize(latent, codebook))
        except Exception as error:
            raise WorkerError("UNSUPPORTED_MODEL", "VQGAN decoder must accept a quantized BCHW embedding grid") from error
        if probe.ndim != 4 or probe.shape[1] != 3:
            raise WorkerError("UNSUPPORTED_MODEL", "VQGAN decoder must return a BCHW RGB image")
        optimizer = library.optim.Adam([latent], lr=parameters["learning_rate"])
        emit("started", device=runtime.device, fallback=runtime.fallback)
        for step in range(1, parameters["iterations"] + 1):
            optimizer.zero_grad(set_to_none=True)
            quantized = vector_quantize(latent, codebook)
            image = display_image(decoder(quantized), parameters["width"], parameters["height"])
            loss = -(encode_image(clip, image) * target).sum() + parameters["commitment_weight"] * library.nn.functional.mse_loss(latent, quantized.detach())
            loss.backward()
            optimizer.step()
            with library.no_grad():
                image = display_image(decoder(vector_quantize(latent, codebook)), parameters["width"], parameters["height"])
            progress(job, job_dir, image, step, parameters["iterations"])
        save_tensor_png(job_dir / "final.png", image)
        emit("completed", path="final.png", device=runtime.device)


class BigSleepEngine(Engine):
    id, version = "big-sleep", "1.1.0"

    def validate(self, parameters: dict[str, Any]) -> dict[str, Any]:
        result = clip_parameters(parameters)
        result.update({
            "generator_path": local_file(parameters.get("generator_path"), "generator_path", model=True),
            "latent_channels": integer(parameters.get("latent_channels"), "latent_channels", 1, 4096, 128),
            "class_count": integer(parameters.get("class_count"), "class_count", 2, 10000, 1000),
            "class_entropy_weight": number(parameters.get("class_entropy_weight"), "class_entropy_weight", 0, 10, 0.01),
        })
        return result

    def generate(self, job: dict[str, Any], parameters: dict[str, Any], runtime: Runtime, job_dir: Path) -> None:
        library = require_torch()
        runtime.seed(integer(job.get("seed"), "seed", 0, 2**63 - 1, 0))
        emit("model-loading", model=parameters["generator_path"].name)
        generator = freeze(load_torchscript(parameters["generator_path"], runtime.device))
        clip, target = open_clip_model(parameters, runtime)
        latent = library.randn((1, parameters["latent_channels"]), device=runtime.device, requires_grad=True)
        class_logits = library.zeros((1, parameters["class_count"]), device=runtime.device, requires_grad=True)
        try:
            probe = generator(latent, class_logits.softmax(-1))
        except Exception as error:
            raise WorkerError("UNSUPPORTED_MODEL", "BigGAN generator must accept latent and class-probability tensors") from error
        if probe.ndim != 4 or probe.shape[1] != 3:
            raise WorkerError("UNSUPPORTED_MODEL", "BigGAN generator must return a BCHW RGB image")
        optimizer = library.optim.Adam([latent, class_logits], lr=parameters["learning_rate"])
        emit("started", device=runtime.device, fallback=runtime.fallback)
        for step in range(1, parameters["iterations"] + 1):
            optimizer.zero_grad(set_to_none=True)
            classes = class_logits.softmax(-1)
            image = display_image(generator(latent, classes), parameters["width"], parameters["height"])
            entropy = -(classes * classes.clamp_min(1e-8).log()).sum()
            loss = -(encode_image(clip, image) * target).sum() + parameters["class_entropy_weight"] * entropy
            loss.backward()
            optimizer.step()
            with library.no_grad():
                image = display_image(generator(latent, class_logits.softmax(-1)), parameters["width"], parameters["height"])
            progress(job, job_dir, image, step, parameters["iterations"])
        save_tensor_png(job_dir / "final.png", image)
        emit("completed", path="final.png", device=runtime.device)
