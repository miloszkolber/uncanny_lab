from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from legacy_lab.common.images import write_rgb_png
from legacy_lab.engines.test_pattern import TestPatternEngine
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


if __name__ == "__main__":
    unittest.main()
