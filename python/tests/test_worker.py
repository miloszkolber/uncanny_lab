from __future__ import annotations

import tempfile
import unittest
import types
from unittest.mock import patch
from argparse import Namespace
from contextlib import redirect_stdout
from io import StringIO
from pathlib import Path

import yaml

from uncanny_lab.common.images import write_rgb_png
from uncanny_lab.engines.test_pattern import TestPatternEngine
from uncanny_lab.engines.dip import DeepImagePriorEngine, build_network
from uncanny_lab.engines.clip import BigSleepEngine, freeze, load_codebook, open_clip_model, validate_biggan_output, validate_vqgan_output, vector_quantize
from uncanny_lab.engines import ENGINES
from uncanny_lab.errors import WorkerError
from uncanny_lab.runner import run
from uncanny_lab.runtime.device import Runtime, torch
from uncanny_lab.common.models import VGGFeatures, load_vgg, normalize_vgg


class WorkerTests(unittest.TestCase):
    def test_manifest_versions_match_worker_implementations(self) -> None:
        manifests = Path(__file__).resolve().parents[2] / "manifests" / "engines"
        declared = {}
        for path in manifests.glob("*.yaml"):
            manifest = yaml.safe_load(path.read_text(encoding="utf-8"))
            declared[manifest["id"]] = manifest["version"]
        self.assertEqual(set(declared), set(ENGINES))
        self.assertEqual(declared, {engine_id: engine.version for engine_id, engine in ENGINES.items()})

    def test_validates_parameter_bounds(self) -> None:
        engine = TestPatternEngine()
        with self.assertRaisesRegex(ValueError, "width must be between"):
            engine.validate({"width": 32})

    def test_cpu_runtime_is_available_without_xpu(self) -> None:
        runtime = Runtime.create("cpu")
        self.assertEqual(runtime.device, "cpu")
        self.assertFalse(runtime.fallback)

    def test_writes_png_atomically(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "image.png"
            write_rgb_png(path, 1, 1, b"\xff\x00\x00")
            self.assertEqual(path.read_bytes()[:8], b"\x89PNG\r\n\x1a\n")

    def test_rejects_paths_outside_data_root(self) -> None:
        with self.assertRaises(WorkerError) as raised:
            DeepImagePriorEngine().validate({"source_image": "/tmp/input.png"})
        self.assertEqual(raised.exception.code, "INVALID_PARAMETERS")

    def test_runner_emits_typed_error(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            job = Path(directory) / "job.json"
            job.write_text('{"parameters":{"width":32}}', encoding="utf-8")
            output = StringIO()
            with redirect_stdout(output):
                status = run(Namespace(self_test=False, engine="deep-image-prior", job=job, device="cpu"))
        self.assertEqual(status, 1)
        self.assertIn('"code":"INVALID_PARAMETERS"', output.getvalue())

    @unittest.skipIf(__import__("importlib").util.find_spec("torch") is None, "torch is not installed")
    def test_dip_tiny_generation_emits_ndjson(self) -> None:
        # The generated PNG is a valid source image and keeps this test checkpoint-free.
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / "source.png"; write_rgb_png(source, 32, 32, bytes([128, 64, 32]) * 1024)
            (root / "previews").mkdir()
            job = {"seed": 1, "preview": {"enabled": True, "every_steps": 1}}
            with patch.dict("os.environ", {"UNCANNY_DATA_ROOT": str(root)}):
                engine = DeepImagePriorEngine(); parameters = engine.validate({"source_image": str(source), "width": 32, "height": 32, "iterations": 1, "noise_std": 0})
                Runtime.create("cpu").seed(1); initial_network = build_network(torch, "cpu"); initial_noise = torch.rand((1, 32, 32, 32)); initial = initial_network(initial_noise).detach()
                output = StringIO()
                with redirect_stdout(output): engine.generate(job, parameters, Runtime.create("cpu"), root)
            self.assertTrue((root / "final.png").is_file())
            self.assertIn('"event":"completed"', output.getvalue())
            from PIL import Image
            final = torch.tensor(bytearray(Image.open(root / "final.png").convert("RGB").tobytes()), dtype=torch.uint8)
            initial_bytes = initial[0].mul(255).to(torch.uint8).permute(1, 2, 0).contiguous().view(-1)
            self.assertFalse(torch.equal(final, initial_bytes), "one optimizer step must change the final image")

    @unittest.skipIf(__import__("importlib").util.find_spec("torch") is None, "torch is not installed")
    def test_vgg_normalization_uses_imagenet_statistics(self) -> None:
        normalized = normalize_vgg(torch.zeros((1, 3, 1, 1)))
        expected = torch.tensor([-0.485 / 0.229, -0.456 / 0.224, -0.406 / 0.225])
        torch.testing.assert_close(normalized.view(3), expected)

    @unittest.skipIf(__import__("importlib").util.find_spec("torch") is None, "torch is not installed")
    def test_vector_quantization_uses_nearest_code_with_straight_gradient(self) -> None:
        latent = torch.tensor([[[[0.9]], [[0.1]]]], requires_grad=True)
        codebook = torch.tensor([[0.0, 0.0], [1.0, 0.0]])
        quantized = vector_quantize(latent, codebook)
        torch.testing.assert_close(quantized.detach().view(2), torch.tensor([1.0, 0.0]))
        quantized.sum().backward()
        torch.testing.assert_close(latent.grad, torch.ones_like(latent))

    @unittest.skipIf(__import__("importlib").util.find_spec("torch") is None, "torch is not installed")
    def test_codebook_rejects_noncanonical_key_or_shape(self) -> None:
        with patch("uncanny_lab.engines.clip.load_state_dict", return_value={"codebook.weight": torch.zeros((16384, 256))}):
            with self.assertRaisesRegex(WorkerError, "exactly embedding.weight"):
                load_codebook(Path("/unused"), "cpu")
        with patch("uncanny_lab.engines.clip.load_state_dict", return_value={"embedding.weight": torch.zeros((2, 256))}):
            with self.assertRaisesRegex(WorkerError, r"\[16384,256\]"):
                load_codebook(Path("/unused"), "cpu")

    def test_biggan_rejects_incompatible_fixed_interface_controls(self) -> None:
        with patch("uncanny_lab.engines.clip.clip_parameters", return_value={}):
            with self.assertRaisesRegex(WorkerError, "latent_channels must be between 128 and 128"):
                BigSleepEngine().validate({"latent_channels": 127})
            with self.assertRaisesRegex(WorkerError, "class_count must be between 1000 and 1000"):
                BigSleepEngine().validate({"class_count": 999})

    def test_vgg_rejects_single_missing_feature_tensor(self) -> None:
        state = VGGFeatures().state_dict()
        del state["features.34.bias"]
        with patch("uncanny_lab.common.models.load_state_dict", return_value=state):
            with self.assertRaisesRegex(WorkerError, "complete TorchVision VGG19"):
                load_vgg(Path("/unused"), "cpu")

    def test_portable_generators_require_exact_output_contracts(self) -> None:
        latent = torch.zeros((1, 256, 8, 12))
        validate_vqgan_output(torch.zeros((1, 3, 128, 192)), latent)
        with self.assertRaisesRegex(WorkerError, "16x spatial scale"):
            validate_vqgan_output(torch.zeros((1, 3, 64, 96)), latent)
        z = torch.zeros((1, 128))
        validate_biggan_output(torch.zeros((1, 3, 256, 256)), z)
        with self.assertRaisesRegex(WorkerError, "BigGAN-deep-256"):
            validate_biggan_output(torch.zeros((1, 3, 128, 128)), z)

    @unittest.skipIf(__import__("importlib").util.find_spec("torch") is None, "torch is not installed")
    def test_frozen_models_backpropagate_only_to_inputs(self) -> None:
        model = freeze(torch.nn.Linear(2, 1))
        value = torch.ones((1, 2), requires_grad=True)
        model(value).sum().backward()
        self.assertIsNotNone(value.grad)
        self.assertTrue(all(parameter.grad is None and not parameter.requires_grad for parameter in model.parameters()))

    @unittest.skipIf(__import__("importlib").util.find_spec("torch") is None, "torch is not installed")
    def test_openai_clip_uses_quick_gelu_and_strict_state_loading(self) -> None:
        class FakeCLIP(torch.nn.Module):
            def __init__(self) -> None:
                super().__init__()
                self.weight = torch.nn.Parameter(torch.ones(1))
            def encode_text(self, tokens):
                return self.weight.expand(tokens.shape[0], 1)
            def load_state_dict(self, state, strict=True):
                created["strict"] = strict
                return super().load_state_dict(state, strict=strict)
        created: dict[str, object] = {}
        fake_module = types.SimpleNamespace(
            create_model_and_transforms=lambda *args, **kwargs: (created.update(kwargs) or (FakeCLIP(), None, None)),
            tokenize=lambda prompts: torch.ones((len(prompts), 1), dtype=torch.long),
        )
        with patch.dict("sys.modules", {"open_clip": fake_module}), patch("uncanny_lab.engines.clip.load_state_dict", return_value={"weight": torch.ones(1)}):
            model, _ = open_clip_model({"clip_model": "ViT-B-32", "clip_checkpoint": Path("/unused"), "prompt": "test"}, Runtime.create("cpu"))
        self.assertTrue(created["force_quick_gelu"])
        self.assertIs(created["strict"], True)
        self.assertFalse(next(model.parameters()).requires_grad)


if __name__ == "__main__":
    unittest.main()
