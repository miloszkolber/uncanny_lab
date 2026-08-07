from __future__ import annotations

import tempfile
import unittest
from unittest.mock import patch
from argparse import Namespace
from contextlib import redirect_stdout
from io import StringIO
from pathlib import Path

from uncanny_lab.common.images import write_rgb_png
from uncanny_lab.engines.test_pattern import TestPatternEngine
from uncanny_lab.engines.dip import DeepImagePriorEngine, build_network
from uncanny_lab.errors import WorkerError
from uncanny_lab.runner import run
from uncanny_lab.runtime.device import Runtime, torch
from uncanny_lab.common.models import normalize_vgg


class WorkerTests(unittest.TestCase):
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


if __name__ == "__main__":
    unittest.main()
