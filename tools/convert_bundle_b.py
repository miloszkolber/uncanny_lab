#!/usr/bin/env python3
"""Create the portable Bundle B artifacts from locally downloaded checkpoints.

This tool is intentionally outside the runtime image. Run it in an ephemeral
environment with the conversion dependencies documented in the README.
"""

from __future__ import annotations

import argparse
import hashlib
import importlib
import importlib.metadata
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tempfile
from datetime import UTC, datetime
import types
from typing import Any, Callable
from uuid import uuid4

import torch
import yaml


CLIP_METADATA_KEYS = frozenset({"input_resolution", "context_length", "vocab_size"})
TAMING_COMMIT = "3ba01b241669f5ade541ce990f7650a3b8f65318"
BIGGAN_COMMIT = "1e18aed2dff75db51428f13b940c38b923eb4a3d"
SOURCE_HASHES = {
    "vgg19.pt": "dcbb9e9dad569fff7a846263a77324fc34978fea2bfb039c012d710e1776ae44",
    "ViT-B-32.pt": "40d365715913c9da98579312b702a82c18be219cc2a73407c4526f58eba950af",
    "vqgan-imagenet-f16-16384.ckpt": "845a68805098cb666420d5db93df53f3a3b6dd443e6dd85c05759c5b998cd663",
    "vqgan-imagenet-f16-16384.yaml": "00e2c6189926f1d89ecfef73e9598db77981c1982f0555fbade963ffd16143c7",
    "biggan-deep-256.bin": "5900ef4065047e3aa0d1b66197b7f2664dceb59b27080e414ad57e203c485bc5",
    "biggan-deep-256-config.json": "edd106f65ff28ee1638491a978cfa6bc50dfa0344aab70749a7b8eb08bbec677",
}
SOURCE_PROVENANCE = {
    "vgg19": {"url": "https://download.pytorch.org/models/vgg19-dcbb9e9d.pth", "license": "https://github.com/pytorch/vision/blob/main/LICENSE"},
    "clip": {"url": "https://openaipublic.azureedge.net/clip/models/40d365715913c9da98579312b702a82c18be219cc2a73407c4526f58eba950af/ViT-B-32.pt", "license": "https://github.com/openai/CLIP/blob/main/LICENSE"},
    "vqgan": {"url": "https://heibox.uni-heidelberg.de/d/a7530b09fed84f80a887/files/?p=%2Fckpts%2Flast.ckpt&dl=1", "license": "https://github.com/CompVis/taming-transformers/blob/master/License.txt"},
    "biggan": {"url": "https://s3.amazonaws.com/models.huggingface.co/biggan/biggan-deep-256-pytorch_model.bin", "license": "https://github.com/huggingface/pytorch-pretrained-BigGAN/blob/master/LICENSE"},
}


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def require_approved_source(path: Path) -> None:
    expected = SOURCE_HASHES.get(path.name)
    actual = sha256(path)
    if expected is None or actual != expected:
        raise ValueError(f"unapproved source hash for {path.name}: {actual}")


def git_output(source: Path, *args: str) -> str | None:
    try:
        return subprocess.check_output(["git", "-C", str(source), *args], text=True).strip()
    except (OSError, subprocess.CalledProcessError):
        return None


def git_commit(source: Path) -> str | None:
    return git_output(source, "rev-parse", "HEAD")


def require_pinned_clean_source(source: Path, expected_commit: str, label: str) -> dict[str, str]:
    if not source.is_dir():
        raise ValueError(f"{label} source checkout does not exist: {source}")
    commit = git_commit(source)
    status = git_output(source, "status", "--porcelain", "--untracked-files=all", "--ignored")
    tree = git_output(source, "rev-parse", "HEAD^{tree}")
    if commit != expected_commit:
        raise ValueError(f"{label} source must be pinned at {expected_commit}")
    if status is None or status:
        raise ValueError(f"{label} source checkout must be clean, including untracked files")
    if tree is None:
        raise ValueError(f"could not determine {label} source tree hash")
    return {"path": str(source.resolve()), "commit": commit, "tree": tree}


def safe_load(path: Path) -> Any:
    """Load tensor-only checkpoint data without allowing pickle execution."""
    try:
        return torch.load(path, map_location="cpu", weights_only=True)
    except TypeError as error:
        raise RuntimeError("PyTorch >= 2.0 with weights_only loading is required") from error


def safe_load_vqgan(path: Path) -> Any:
    """Load the approved Lightning archive with a non-executable callback stub."""
    module_name = "pytorch_lightning.callbacks.model_checkpoint"
    lightning = types.ModuleType("pytorch_lightning")
    callbacks = types.ModuleType("pytorch_lightning.callbacks")
    model_checkpoint = types.ModuleType(module_name)
    ModelCheckpoint = type("ModelCheckpoint", (), {})
    ModelCheckpoint.__module__ = module_name
    model_checkpoint.ModelCheckpoint = ModelCheckpoint
    lightning.callbacks = callbacks
    callbacks.model_checkpoint = model_checkpoint
    inserted = {"pytorch_lightning": lightning, "pytorch_lightning.callbacks": callbacks, module_name: model_checkpoint}
    previous = {name: sys.modules.get(name) for name in inserted}
    sys.modules.update(inserted)
    try:
        with torch.serialization.safe_globals([ModelCheckpoint]):
            return safe_load(path)
    finally:
        for name, value in previous.items():
            if value is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = value


def require_tensor_state(payload: Any, label: str) -> dict[str, torch.Tensor]:
    if isinstance(payload, dict) and isinstance(payload.get("state_dict"), dict):
        payload = payload["state_dict"]
    if not isinstance(payload, dict) or not payload:
        raise ValueError(f"{label} must contain a non-empty state_dict")
    if not all(isinstance(key, str) and isinstance(value, torch.Tensor) for key, value in payload.items()):
        raise ValueError(f"{label} must contain only string tensor state_dict entries")
    return payload


def strip_clip_metadata(state: dict[str, torch.Tensor]) -> tuple[dict[str, torch.Tensor], list[str]]:
    removed = sorted(set(state).intersection(CLIP_METADATA_KEYS))
    cleaned = {key: value for key, value in state.items() if key not in CLIP_METADATA_KEYS}
    if not cleaned:
        raise ValueError("CLIP state_dict contains no model tensors")
    return cleaned, removed


def atomic_artifact(path: Path, writer: Callable[[Path], None], validator: Callable[[Path], dict[str, Any]]) -> dict[str, Any]:
    """Write and validate a sibling temporary file before replacing the artifact."""
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary_name = tempfile.mkstemp(prefix=f".{path.name}.", suffix=".tmp", dir=path.parent)
    os.close(descriptor)
    temporary = Path(temporary_name)
    try:
        writer(temporary)
        validation = validator(temporary)
        os.replace(temporary, path)
    finally:
        temporary.unlink(missing_ok=True)
    return {"path": str(path), "sha256": sha256(path), "bytes": path.stat().st_size, "validation": validation}


def validate_tensor_state(path: Path, expected_key: str, shape: tuple[int, ...]) -> dict[str, Any]:
    state = require_tensor_state(safe_load(path), path.name)
    if set(state) != {expected_key} or tuple(state[expected_key].shape) != shape:
        raise ValueError(f"{path.name} is not exactly {expected_key} with shape {shape}")
    return {"format": "weights_only_state_dict", "keys": [expected_key], "shape": list(shape)}


def import_from_source(root: Path, module: str) -> Any:
    root = root.resolve()
    if not root.is_dir():
        raise ValueError(f"source checkout does not exist: {root}")
    sys.path.insert(0, str(root))
    previous_dont_write_bytecode = sys.dont_write_bytecode
    sys.dont_write_bytecode = True
    try:
        return importlib.import_module(module)
    finally:
        sys.dont_write_bytecode = previous_dont_write_bytecode
        sys.path.pop(0)


def import_biggan_source(root: Path) -> tuple[Any, Any]:
    """Load the pinned model modules without importing optional download clients."""
    package_name = "_uncanny_biggan_source"
    package_root = root.resolve() / "pytorch_pretrained_biggan"
    package = types.ModuleType(package_name)
    package.__path__ = [str(package_root)]
    sys.modules[package_name] = package
    file_utils = types.ModuleType(f"{package_name}.file_utils")
    file_utils.cached_path = lambda path, cache_dir=None: path
    sys.modules[file_utils.__name__] = file_utils
    previous_dont_write_bytecode = sys.dont_write_bytecode
    sys.dont_write_bytecode = True
    try:
        config = importlib.import_module(f"{package_name}.config")
        model = importlib.import_module(f"{package_name}.model")
        return config, model
    finally:
        sys.dont_write_bytecode = previous_dont_write_bytecode
        sys.modules.pop(file_utils.__name__, None)


class VQGANDecoder(torch.nn.Module):
    def __init__(self, post_quant_conv: torch.nn.Module, decoder: torch.nn.Module) -> None:
        super().__init__()
        self.post_quant_conv = post_quant_conv
        self.decoder = decoder

    def forward(self, quantized: torch.Tensor) -> torch.Tensor:
        return self.decoder(self.post_quant_conv(quantized))


class BigGANWrapper(torch.nn.Module):
    def __init__(self, model: torch.nn.Module) -> None:
        super().__init__()
        self.model = model

    def forward(self, z: torch.Tensor, classes: torch.Tensor) -> torch.Tensor:
        return self.model(z, classes, 1.0)


def validate_decoder(module: torch.jit.ScriptModule, device: str = "cpu") -> dict[str, Any]:
    results: list[dict[str, Any]] = []
    for shape in ((1, 256, 16, 16), (2, 256, 8, 8), (1, 256, 8, 12)):
        value = torch.randn(shape, device=device, requires_grad=True)
        output = module(value)
        expected = (shape[0], 3, shape[2] * 16, shape[3] * 16)
        if tuple(output.shape) != expected:
            raise ValueError(f"decoder output {tuple(output.shape)} does not match {expected}")
        output.mean().backward()
        if value.grad is None or not torch.isfinite(value.grad).all():
            raise ValueError("decoder does not preserve finite input gradients")
        results.append({"input": list(shape), "output": list(output.shape), "input_gradient": True})
    return {"interface": "quantized BCHW [N,256,H,W] -> RGB BCHW [N,3,16H,16W]", "cases": results}


def validate_biggan(module: torch.jit.ScriptModule, device: str = "cpu") -> dict[str, Any]:
    results: list[dict[str, Any]] = []
    for batch, kind in ((1, "one_hot"), (2, "soft")):
        z = torch.randn((batch, 128), device=device, requires_grad=True)
        classes = torch.zeros((batch, 1000), device=device, requires_grad=True)
        if kind == "one_hot":
            with torch.no_grad():
                classes[:, 0] = 1
        else:
            with torch.no_grad():
                classes.copy_(torch.softmax(torch.randn_like(classes), dim=-1))
        output = module(z, classes)
        if tuple(output.shape) != (batch, 3, 256, 256):
            raise ValueError(f"BigGAN output has invalid shape {tuple(output.shape)}")
        output.mean().backward()
        if z.grad is None or classes.grad is None or not torch.isfinite(z.grad).all() or not torch.isfinite(classes.grad).all():
            raise ValueError("BigGAN does not preserve finite gradients to z and classes")
        results.append({"batch": batch, "classes": kind, "output": list(output.shape), "z_gradient": True, "classes_gradient": True})
    return {"interface": "z [N,128], classes [N,1000] -> RGB BCHW [N,3,256,256], truncation=1.0", "cases": results}


def convert_clip(source: Path, output: Path) -> dict[str, Any]:
    require_approved_source(source)
    scripted = torch.jit.load(str(source), map_location="cpu")
    state, removed = strip_clip_metadata(require_tensor_state(scripted.state_dict(), "OpenAI CLIP JIT"))
    import open_clip
    model = open_clip.create_model("ViT-B-32", pretrained=None, force_quick_gelu=True, device="cpu")
    model.load_state_dict(state, strict=True)

    def validate(path: Path) -> dict[str, Any]:
        loaded = require_tensor_state(safe_load(path), "converted CLIP")
        check = open_clip.create_model("ViT-B-32", pretrained=None, force_quick_gelu=True, device="cpu")
        check.load_state_dict(loaded, strict=True)
        return {
            "architecture": "ViT-B-32",
            "interface": "OpenCLIP ViT-B-32 state_dict with original OpenAI QuickGELU activations",
            "force_quick_gelu": True,
            "strict_state_dict": True,
            "metadata_removed": removed,
        }

    artifact = atomic_artifact(output, lambda temporary: torch.save(state, temporary), validate)
    return {"source": describe(source), "artifact": artifact}


def convert_vqgan(source: Path, config_path: Path, output_codebook: Path, output_decoder: Path, taming_source: Path) -> dict[str, Any]:
    require_approved_source(source)
    require_approved_source(config_path)
    source_tree = require_pinned_clean_source(taming_source, TAMING_COMMIT, "taming-transformers")
    config = yaml.safe_load(config_path.read_text(encoding="utf-8"))["model"]["params"]
    ddconfig = config["ddconfig"]
    Decoder = import_from_source(taming_source, "taming.modules.diffusionmodules.model").Decoder
    state = require_tensor_state(safe_load_vqgan(source), "VQGAN checkpoint")
    codebook_key = "quantize.embedding.weight"
    codebook = state.get(codebook_key)
    if codebook is None or tuple(codebook.shape) != (16384, 256):
        raise ValueError("VQGAN checkpoint must have quantize.embedding.weight [16384,256]")
    decoder = Decoder(**ddconfig).eval()
    post_quant_conv = torch.nn.Conv2d(256, 256, 1).eval()
    decoder_state = {key.removeprefix("decoder."): value for key, value in state.items() if key.startswith("decoder.")}
    post_state = {key.removeprefix("post_quant_conv."): value for key, value in state.items() if key.startswith("post_quant_conv.")}
    decoder.load_state_dict(decoder_state, strict=True)
    post_quant_conv.load_state_dict(post_state, strict=True)
    wrapped = VQGANDecoder(post_quant_conv, decoder).eval()

    codebook_artifact = atomic_artifact(output_codebook, lambda temporary: torch.save({"embedding.weight": codebook}, temporary), lambda path: validate_tensor_state(path, "embedding.weight", (16384, 256)))

    def write_decoder(temporary: Path) -> None:
        traced = torch.jit.trace(wrapped, torch.randn((1, 256, 16, 16)), check_trace=False)
        traced.save(str(temporary))

    decoder_artifact = atomic_artifact(output_decoder, write_decoder, lambda path: validate_decoder(torch.jit.load(str(path), map_location="cpu")))
    return {"source": describe(source), "config": describe(config_path), "source_tree": source_tree, "codebook": codebook_artifact, "decoder": decoder_artifact, "export": "trace (pinned decoder mutates last_z_shape, preventing scripting)"}


def convert_biggan(weights: Path, config_path: Path, output: Path, biggan_source: Path) -> dict[str, Any]:
    require_approved_source(weights)
    require_approved_source(config_path)
    source_tree = require_pinned_clean_source(biggan_source, BIGGAN_COMMIT, "pytorch-pretrained-BigGAN")
    config_module, model_module = import_biggan_source(biggan_source)
    config = config_module.BigGANConfig.from_json_file(str(config_path))
    model = model_module.BigGAN(config).eval()
    model.load_state_dict(require_tensor_state(safe_load(weights), "BigGAN checkpoint"), strict=True)
    wrapped = BigGANWrapper(model).eval()

    def write(temporary: Path) -> None:
        traced = torch.jit.trace(wrapped, (torch.randn((1, 128)), torch.nn.functional.one_hot(torch.tensor([0]), 1000).float()), check_trace=False)
        traced.save(str(temporary))

    artifact = atomic_artifact(output, write, lambda path: validate_biggan(torch.jit.load(str(path), map_location="cpu")))
    return {"weights": describe(weights), "config": describe(config_path), "source_tree": source_tree, "artifact": artifact, "export": "trace (legacy model uses Python truncation/statistics control flow)"}


def describe(path: Path) -> dict[str, Any]:
    return {"path": str(path), "sha256": sha256(path), "bytes": path.stat().st_size}


def validate_vgg(path: Path) -> dict[str, Any]:
    require_approved_source(path)
    state = require_tensor_state(safe_load(path), "VGG19 checkpoint")
    feature_weights = [key for key in state if key.startswith("features.") and key.endswith(".weight")]
    if len(feature_weights) != 16:
        raise ValueError(f"VGG19 checkpoint has {len(feature_weights)} feature weights, expected 16")
    return {"format": "TorchVision VGG19 state_dict", "feature_weights": len(feature_weights)}


def copy_vgg(source: Path, output: Path) -> dict[str, Any]:
    validation = validate_vgg(source)
    artifact = atomic_artifact(output, lambda temporary: shutil.copyfile(source, temporary), lambda path: validate_vgg_artifact(path))
    return {"source": describe(source), "artifact": artifact, "validation": validation}


def validate_vgg_artifact(path: Path) -> dict[str, Any]:
    state = require_tensor_state(safe_load(path), "VGG19 artifact")
    feature_weights = [key for key in state if key.startswith("features.") and key.endswith(".weight")]
    if len(feature_weights) != 16:
        raise ValueError(f"VGG19 artifact has {len(feature_weights)} feature weights, expected 16")
    return {"format": "TorchVision VGG19 state_dict", "feature_weights": len(feature_weights)}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sources", type=Path, default=Path("/data/model-sources"))
    parser.add_argument("--models", type=Path, default=Path("/data/models"))
    parser.add_argument("--vgg-source", type=Path, default=Path("/data/models/classifiers/vgg19.pt"))
    parser.add_argument("--taming-source", type=Path, required=True)
    parser.add_argument("--biggan-source", type=Path, required=True)
    return parser.parse_args()


def stable_paths(value: Any, stage: Path, models: Path) -> Any:
    """Replace staging paths in the report with runtime-stable bundle-b paths."""
    if isinstance(value, dict):
        return {key: stable_paths(item, stage, models) for key, item in value.items()}
    if isinstance(value, list):
        return [stable_paths(item, stage, models) for item in value]
    if isinstance(value, str):
        try:
            return str(models / "bundle-b" / Path(value).relative_to(stage))
        except ValueError:
            return value
    return value


def atomic_symlink(models: Path, version: Path) -> Path:
    """Atomically point the stable runtime path at one immutable bundle version."""
    target = models / "bundle-b"
    temporary = models / f".bundle-b-{uuid4().hex}.tmp"
    os.symlink(str(Path("bundles") / version.name), temporary)
    try:
        os.replace(temporary, target)
    finally:
        temporary.unlink(missing_ok=True)
    return target


def publish_bundle(models: Path, build: Callable[[Path], dict[str, Any]]) -> dict[str, Any]:
    """Build all files in one staging directory, then publish through one symlink swap."""
    models.mkdir(parents=True, exist_ok=True)
    bundles = models / "bundles"
    bundles.mkdir(parents=True, exist_ok=True)
    token = uuid4().hex
    stage = bundles / f".bundle-b-{token}.staging"
    version = bundles / f"bundle-b-{datetime.now(UTC).strftime('%Y%m%dT%H%M%SZ')}-{token[:12]}"
    stage.mkdir(mode=0o750)
    try:
        report = build(stage)
        stable = models / "bundle-b"
        report["publication"] = {"version": str(version), "stable_path": str(stable), "provenance": str(stable / "provenance/bundle-b-conversion-report.json")}
        provenance = stage / "provenance" / "bundle-b-conversion-report.json"
        payload = json.dumps(stable_paths(report, stage, models), indent=2, sort_keys=True) + "\n"
        atomic_artifact(provenance, lambda temporary: temporary.write_text(payload, encoding="utf-8"), lambda path: {"json": isinstance(json.loads(path.read_text(encoding="utf-8")), dict)})
        os.replace(stage, version)
        atomic_symlink(models, version)
        return stable_paths(report, stage, models)
    except Exception:
        shutil.rmtree(stage, ignore_errors=True)
        raise


def main() -> None:
    args = parse_args()
    sources, models = args.sources, args.models
    app_root = Path(__file__).resolve().parents[1]

    def build(stage: Path) -> dict[str, Any]:
        return {
            "format": "uncanny-lab-bundle-b-conversion-v2",
            "tool": {
                "script_sha256": sha256(Path(__file__).resolve()),
                "app_git_revision": git_commit(app_root),
                "environment": {"python": sys.version.split()[0], "platform": platform.platform(), "executable": sys.executable, "working_directory": str(Path.cwd())},
                "versions": {
                    "torch": torch.__version__,
                    "open_clip_torch": importlib.metadata.version("open_clip_torch"),
                    "PyYAML": importlib.metadata.version("PyYAML"),
                    "numpy": importlib.metadata.version("numpy"),
                },
            },
            "sources": SOURCE_PROVENANCE,
            "vgg19": copy_vgg(args.vgg_source, stage / "classifiers/vgg19.pt"),
            "clip": convert_clip(sources / "ViT-B-32.pt", stage / "clip/vit-b-32.pt"),
            "vqgan": convert_vqgan(sources / "vqgan-imagenet-f16-16384.ckpt", sources / "vqgan-imagenet-f16-16384.yaml", stage / "vqgan/codebook.pt", stage / "vqgan/decoder.pt", args.taming_source),
            "biggan": convert_biggan(sources / "biggan-deep-256.bin", sources / "biggan-deep-256-config.json", stage / "biggan/generator.pt", args.biggan_source),
        }

    report = publish_bundle(models, build)
    print(json.dumps(report, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
