from __future__ import annotations

import tempfile
import unittest
from argparse import Namespace
from contextlib import redirect_stdout
from io import StringIO
from pathlib import Path

from legacy_lab.common.images import write_rgb_png
from legacy_lab.engines.test_pattern import TestPatternEngine
from legacy_lab.engines.dip import DeepImagePriorEngine
from legacy_lab.errors import WorkerError
from legacy_lab.runner import run
from legacy_lab.runtime.device import Runtime


class TestPatternTests(unittest.TestCase):
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
        with tempfile.TemporaryDirectory(dir="/data" if Path("/data").is_dir() else None) as directory:
            root = Path(directory)
            source = root / "source.png"; write_rgb_png(source, 32, 32, bytes([128, 64, 32]) * 1024)
            # Engine contracts deliberately require /data. Skip on development hosts where temp files cannot be placed there.
            if not str(source.resolve()).startswith("/data/"):
                self.skipTest("temporary source is not under /data")
            (root / "previews").mkdir()
            job = {"seed": 1, "preview": {"enabled": True, "every_steps": 1}}
            engine = DeepImagePriorEngine(); parameters = engine.validate({"source_image": str(source), "width": 32, "height": 32, "iterations": 1})
            output = StringIO()
            with redirect_stdout(output): engine.generate(job, parameters, Runtime.create("cpu"), root)
            self.assertTrue((root / "final.png").is_file())
            self.assertIn('"event":"completed"', output.getvalue())


if __name__ == "__main__":
    unittest.main()
