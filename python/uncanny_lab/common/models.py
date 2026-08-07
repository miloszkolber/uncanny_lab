"""Safe local model loading and reusable feature extraction."""

from __future__ import annotations

from pathlib import Path
import os
from typing import Any

from uncanny_lab.errors import WorkerError
from uncanny_lab.runtime.device import torch

VGG_MEAN = (0.485, 0.456, 0.406)
VGG_STD = (0.229, 0.224, 0.225)


def local_file(value: Any, name: str, *, model: bool = False) -> Path:
    if not isinstance(value, str) or not value:
        raise WorkerError("INVALID_PARAMETERS", f"{name} must be an absolute path")
    path = Path(value)
    root = Path(os.environ.get("UNCANNY_MODELS_ROOT" if model else "UNCANNY_DATA_ROOT", "/data/models" if model else "/data")).resolve()
    try:
        resolved = path.resolve(strict=True)
        resolved.relative_to(root)
    except (OSError, ValueError) as error:
        code = "MODEL_MISSING" if model else "INVALID_PARAMETERS"
        raise WorkerError(code, f"{name} must be an existing file under {root}") from error
    if not resolved.is_file():
        raise WorkerError("MODEL_MISSING" if model else "INVALID_PARAMETERS", f"{name} is not a file")
    return resolved


def require_torch() -> Any:
    if torch is None:
        raise WorkerError("UNSUPPORTED_MODEL", "PyTorch is not installed in this worker")
    return torch


def load_state_dict(path: Path, device: str) -> dict[str, Any]:
    library = require_torch()
    try:
        try:
            payload = library.load(path, map_location=device, weights_only=True)
        except TypeError:  # Older supported torch releases lack weights_only.
            payload = library.load(path, map_location=device)
    except FileNotFoundError as error:
        raise WorkerError("MODEL_MISSING", f"model checkpoint is missing: {path}") from error
    except Exception as error:
        raise WorkerError("MODEL_INVALID", f"could not load checkpoint {path.name}: {error}") from error
    if isinstance(payload, dict) and isinstance(payload.get("state_dict"), dict):
        payload = payload["state_dict"]
    if not isinstance(payload, dict) or not payload:
        raise WorkerError("MODEL_INVALID", "checkpoint must contain a non-empty tensor state_dict")
    if not all(isinstance(key, str) for key in payload):
        raise WorkerError("MODEL_INVALID", "checkpoint state_dict keys must be strings")
    return payload


class VGGFeatures(require_torch().nn.Module if torch is not None else object):
    """VGG19 feature stack compatible with torchvision's `features.*` weights."""

    def __init__(self) -> None:
        library = require_torch()
        super().__init__()
        layers: list[Any] = []
        channels = [3, 64, 64, "M", 128, 128, "M", 256, 256, 256, 256, "M", 512, 512, 512, 512, "M", 512, 512, 512, 512, "M"]
        previous = 3
        for channel in channels[1:]:
            if channel == "M":
                layers.append(library.nn.MaxPool2d(2, 2))
            else:
                layers.extend((library.nn.Conv2d(previous, channel, 3, padding=1), library.nn.ReLU(inplace=False)))
                previous = channel
        self.features = library.nn.Sequential(*layers)

    def forward_features(self, image: Any, requested: set[str]) -> dict[str, Any]:
        output: dict[str, Any] = {}
        value = normalize_vgg(image)
        for index, layer in enumerate(self.features):
            value = layer(value)
            name = f"features.{index}"
            if name in requested:
                output[name] = value
        return output


def normalize_vgg(image: Any) -> Any:
    library = require_torch()
    mean = library.tensor(VGG_MEAN, device=image.device, dtype=image.dtype).view(1, 3, 1, 1)
    std = library.tensor(VGG_STD, device=image.device, dtype=image.dtype).view(1, 3, 1, 1)
    return (image - mean) / std


def load_vgg(path: Path, device: str) -> VGGFeatures:
    library = require_torch()
    state = load_state_dict(path, device)
    model = VGGFeatures().to(device)
    # Accept checkpoints saved from either VGGFeatures or torchvision.models.vgg19.
    cleaned = {key.removeprefix("module."): value for key, value in state.items()}
    expected = model.state_dict()
    missing = set(expected).difference(cleaned)
    unexpected = {key for key in cleaned if key not in expected and not key.startswith("classifier.")}
    wrong_shapes = {key for key, value in expected.items() if key in cleaned and tuple(cleaned[key].shape) != tuple(value.shape)}
    if missing or unexpected or wrong_shapes:
        raise WorkerError("MODEL_INVALID", "checkpoint must contain the complete TorchVision VGG19 feature state_dict")
    model.load_state_dict({key: cleaned[key] for key in expected}, strict=True)
    model.eval()
    for parameter in model.parameters():
        parameter.requires_grad_(False)
    return model


def load_torchscript(path: Path, device: str) -> Any:
    library = require_torch()
    try:
        return library.jit.load(str(path), map_location=device).eval()
    except FileNotFoundError as error:
        raise WorkerError("MODEL_MISSING", f"generator is missing: {path}") from error
    except Exception as error:
        raise WorkerError("MODEL_INVALID", f"invalid TorchScript generator: {error}") from error
