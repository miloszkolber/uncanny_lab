"""Feature-optimization engines backed by a local VGG19 checkpoint."""

from __future__ import annotations

from functools import partial
from pathlib import Path
from typing import Any

from uncanny_lab.common.images import load_image, save_tensor_png
from uncanny_lab.common.models import load_vgg, local_file, require_torch
from uncanny_lab.common.progress import PreviewWriter, emit
from uncanny_lab.errors import WorkerError, invalid
from uncanny_lab.engines.base import Engine
from uncanny_lab.runtime.device import Runtime

STYLE_LAYERS = ("features.1", "features.6", "features.11", "features.20", "features.29")
CONTENT_LAYER = "features.22"


def integer(value: Any, name: str, minimum: int, maximum: int, default: int) -> int:
    value = default if value is None else value
    if isinstance(value, bool):
        raise invalid(f"{name} must be an integer")
    try:
        result = int(value)
    except (TypeError, ValueError) as error:
        raise invalid(f"{name} must be an integer") from error
    if not minimum <= result <= maximum:
        raise invalid(f"{name} must be between {minimum} and {maximum}")
    return result


def number(value: Any, name: str, minimum: float, maximum: float, default: float) -> float:
    value = default if value is None else value
    try:
        result = float(value)
    except (TypeError, ValueError) as error:
        raise invalid(f"{name} must be a number") from error
    if not minimum <= result <= maximum:
        raise invalid(f"{name} must be between {minimum} and {maximum}")
    return result


def image_parameters(parameters: dict[str, Any], *, style: bool = False) -> dict[str, Any]:
    source = local_file(parameters.get("source_image", parameters.get("source")), "source_image")
    output = {
        "source_image": source,
        "classifier_path": local_file(parameters.get("classifier_path", parameters.get("model_path")), "classifier_path", model=True),
        "width": integer(parameters.get("width"), "width", 32, 1024, 256),
        "height": integer(parameters.get("height"), "height", 32, 1024, 256),
        "iterations": integer(parameters.get("iterations"), "iterations", 1, 2000, 250),
        "learning_rate": number(parameters.get("learning_rate"), "learning_rate", 0.00001, 1.0, 0.03),
    }
    if output["width"] * output["height"] > 1_048_576:
        raise invalid("image area must not exceed 1048576 pixels")
    if style:
        output["style_image"] = local_file(parameters.get("style_image", parameters.get("style")), "style_image")
        output["content_weight"] = number(parameters.get("content_weight"), "content_weight", 0, 1e6, 1.0)
        output["style_weight"] = number(parameters.get("style_weight"), "style_weight", 0, 1e8, 1e4)
    return output


def gram(value: Any) -> Any:
    batches, channels, height, width = value.shape
    features = value.reshape(batches, channels, height * width)
    return features @ features.transpose(1, 2) / (channels * height * width)


class FeatureEngine(Engine):
    def _events(self, image: Any, step: int, total: int, job: dict[str, Any], job_dir: Path, writer: PreviewWriter) -> None:
        emit("progress", step=step, total=total)
        preview = job.get("preview", {})
        every = max(1, int(preview.get("every_steps", 5)))
        if bool(preview.get("enabled", True)) and (step == 1 or step % every == 0 or step == total):
            relative = f"previews/{step:06d}.png"
            writer.submit(job_dir / relative, step, partial(save_tensor_png, job_dir / relative, image))


class NeuralStyleEngine(FeatureEngine):
    id, version = "neural-style", "1.0.0"

    def validate(self, parameters: dict[str, Any]) -> dict[str, Any]:
        return image_parameters(parameters, style=True)

    def generate(self, job: dict[str, Any], parameters: dict[str, Any], runtime: Runtime, job_dir: Path) -> None:
        library = require_torch()
        runtime.seed(integer(job.get("seed"), "seed", 0, 2**63 - 1, 0))
        emit("model-loading", model=parameters["classifier_path"].name)
        model = load_vgg(parameters["classifier_path"], runtime.device)
        source = load_image(parameters["source_image"], parameters["width"], parameters["height"], runtime.device)
        style = load_image(parameters["style_image"], parameters["width"], parameters["height"], runtime.device)
        wanted = set(STYLE_LAYERS) | {CONTENT_LAYER}
        with library.no_grad():
            content = model.forward_features(source, wanted)[CONTENT_LAYER]
            style_targets = {name: gram(value) for name, value in model.forward_features(style, wanted).items() if name in STYLE_LAYERS}
        result = source.clone().requires_grad_(True)
        optimizer = library.optim.Adam([result], lr=parameters["learning_rate"])
        writer = PreviewWriter()
        writer.start()
        emit("started", device=runtime.device, fallback=runtime.fallback)
        try:
            for step in range(1, parameters["iterations"] + 1):
                optimizer.zero_grad(set_to_none=True)
                features = model.forward_features(result, wanted)
                content_loss = library.nn.functional.mse_loss(features[CONTENT_LAYER], content)
                style_loss = sum(library.nn.functional.mse_loss(gram(features[name]), style_targets[name]) for name in STYLE_LAYERS)
                loss = parameters["content_weight"] * content_loss + parameters["style_weight"] * style_loss
                loss.backward()
                optimizer.step()
                with library.no_grad(): result.clamp_(0, 1)
                self._events(result, step, parameters["iterations"], job, job_dir, writer)
        finally:
            writer.stop()
        runtime.synchronize()
        save_tensor_png(job_dir / "final.png", result)
        emit("completed", path="final.png", device=runtime.device)


class DeepDreamEngine(FeatureEngine):
    id, version = "deepdream", "1.0.0"

    def validate(self, parameters: dict[str, Any]) -> dict[str, Any]:
        result = image_parameters(parameters)
        result["layer"] = str(parameters.get("layer", "features.20"))
        result["octaves"] = integer(parameters.get("octaves"), "octaves", 1, 5, 3)
        result["octave_scale"] = number(parameters.get("octave_scale"), "octave_scale", 1.1, 2.0, 1.4)
        if result["width"] * result["height"] * result["octave_scale"] ** (2 * (result["octaves"] - 1)) > 1_048_576:
            raise invalid("octaves would exceed the maximum image area")
        return result

    def generate(self, job: dict[str, Any], parameters: dict[str, Any], runtime: Runtime, job_dir: Path) -> None:
        library = require_torch()
        emit("model-loading", model=parameters["classifier_path"].name)
        model = load_vgg(parameters["classifier_path"], runtime.device)
        if parameters["layer"] not in {f"features.{index}" for index in range(len(model.features))}:
            raise WorkerError("INVALID_PARAMETERS", f"unknown VGG feature layer: {parameters['layer']}")
        image = load_image(parameters["source_image"], parameters["width"], parameters["height"], runtime.device)
        writer = PreviewWriter()
        writer.start()
        emit("started", device=runtime.device, fallback=runtime.fallback)
        total, global_step = parameters["iterations"] * parameters["octaves"], 0
        try:
            for octave in range(parameters["octaves"]):
                if octave:
                    scale = parameters["octave_scale"]
                    image = library.nn.functional.interpolate(image, scale_factor=scale, mode="bilinear", align_corners=False)
                image = image.detach().requires_grad_(True)
                optimizer = library.optim.Adam([image], lr=parameters["learning_rate"])
                for _ in range(parameters["iterations"]):
                    global_step += 1
                    optimizer.zero_grad(set_to_none=True)
                    activation = model.forward_features(image, {parameters["layer"]})[parameters["layer"]]
                    (-activation.mean()).backward()
                    optimizer.step()
                    with library.no_grad(): image.clamp_(0, 1)
                    self._events(image, global_step, total, job, job_dir, writer)
        finally:
            writer.stop()
        image = library.nn.functional.interpolate(image, size=(parameters["height"], parameters["width"]), mode="bilinear", align_corners=False)
        save_tensor_png(job_dir / "final.png", image)
        emit("completed", path="final.png", device=runtime.device)


class ActivationMaxEngine(FeatureEngine):
    id, version = "activation-max", "1.0.0"

    def validate(self, parameters: dict[str, Any]) -> dict[str, Any]:
        init = str(parameters.get("init", "noise"))
        if init not in {"noise", "source"}: raise invalid("init must be noise or source")
        result = {
            "classifier_path": local_file(parameters.get("classifier_path", parameters.get("model_path")), "classifier_path", model=True),
            "width": integer(parameters.get("width"), "width", 32, 1024, 256),
            "height": integer(parameters.get("height"), "height", 32, 1024, 256),
            "iterations": integer(parameters.get("iterations"), "iterations", 1, 2000, 250),
            "learning_rate": number(parameters.get("learning_rate"), "learning_rate", 0.00001, 1.0, 0.03),
        }
        if result["width"] * result["height"] > 1_048_576: raise invalid("image area must not exceed 1048576 pixels")
        result["layer"] = str(parameters.get("layer", "features.20"))
        result["channel"] = integer(parameters.get("channel"), "channel", 0, 4095, 0)
        result["init"] = init
        if init == "source": result["source_image"] = local_file(parameters.get("source_image", parameters.get("source")), "source_image")
        return result

    def generate(self, job: dict[str, Any], parameters: dict[str, Any], runtime: Runtime, job_dir: Path) -> None:
        library = require_torch()
        emit("model-loading", model=parameters["classifier_path"].name)
        model = load_vgg(parameters["classifier_path"], runtime.device)
        if parameters["layer"] not in {f"features.{index}" for index in range(len(model.features))}: raise invalid(f"unknown VGG feature layer: {parameters['layer']}")
        runtime.seed(integer(job.get("seed"), "seed", 0, 2**63 - 1, 0))
        image = load_image(parameters["source_image"], parameters["width"], parameters["height"], runtime.device) if parameters["init"] == "source" else library.rand((1, 3, parameters["height"], parameters["width"]), device=runtime.device)
        image.requires_grad_(True)
        optimizer = library.optim.Adam([image], lr=parameters["learning_rate"])
        writer = PreviewWriter()
        writer.start()
        emit("started", device=runtime.device, fallback=runtime.fallback)
        try:
            for step in range(1, parameters["iterations"] + 1):
                optimizer.zero_grad(set_to_none=True)
                activation = model.forward_features(image, {parameters["layer"]})[parameters["layer"]]
                if parameters["channel"] >= activation.shape[1]: raise invalid("channel is not present in the selected layer")
                loss = -activation[:, parameters["channel"]].mean() + 1e-4 * image.square().mean()
                loss.backward(); optimizer.step()
                with library.no_grad(): image.clamp_(0, 1)
                self._events(image, step, parameters["iterations"], job, job_dir, writer)
        finally:
            writer.stop()
        save_tensor_png(job_dir / "final.png", image)
        emit("completed", path="final.png", device=runtime.device)
