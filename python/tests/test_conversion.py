from __future__ import annotations

import importlib.util
from pathlib import Path
import tempfile
import unittest

import torch


TOOL_PATH = Path(__file__).resolve().parents[2] / "tools" / "convert_bundle_b.py"
SPEC = importlib.util.spec_from_file_location("convert_bundle_b", TOOL_PATH)
assert SPEC is not None and SPEC.loader is not None
converter = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(converter)


class ConversionToolTests(unittest.TestCase):
    def test_clip_conversion_removes_only_documented_metadata(self) -> None:
        state = {
            "input_resolution": torch.tensor(224),
            "context_length": torch.tensor(77),
            "vocab_size": torch.tensor(49408),
            "visual.conv1.weight": torch.ones((1, 1, 1, 1)),
        }
        cleaned, removed = converter.strip_clip_metadata(state)
        self.assertEqual(removed, ["context_length", "input_resolution", "vocab_size"])
        self.assertEqual(set(cleaned), {"visual.conv1.weight"})

    def test_failed_validation_preserves_existing_artifact(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "artifact.pt"
            path.write_bytes(b"known-good")
            with self.assertRaisesRegex(ValueError, "reject"):
                converter.atomic_artifact(path, lambda temporary: temporary.write_bytes(b"invalid"), lambda temporary: (_ for _ in ()).throw(ValueError("reject")))
            self.assertEqual(path.read_bytes(), b"known-good")
            self.assertEqual(list(Path(directory).glob(".artifact.pt.*.tmp")), [])

    def test_rejects_source_without_approved_hash(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "ViT-B-32.pt"
            source.write_bytes(b"not the approved checkpoint")
            with self.assertRaisesRegex(ValueError, "unapproved source hash"):
                converter.require_approved_source(source)

    def test_bundle_publication_uses_stable_atomic_symlink(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            models = Path(directory) / "models"

            def build(stage: Path) -> dict[str, object]:
                artifact = stage / "clip" / "vit-b-32.pt"
                artifact.parent.mkdir(parents=True)
                artifact.write_bytes(b"validated")
                return {"artifact": {"path": str(artifact), "sha256": converter.sha256(artifact)}}

            report = converter.publish_bundle(models, build)
            stable = models / "bundle-b"
            self.assertTrue(stable.is_symlink())
            self.assertEqual((stable / "clip/vit-b-32.pt").read_bytes(), b"validated")
            self.assertEqual(report["artifact"]["path"], str(stable / "clip/vit-b-32.pt"))
            provenance = (stable / "provenance/bundle-b-conversion-report.json").read_text(encoding="utf-8")
            self.assertIn(str(stable / "clip/vit-b-32.pt"), provenance)
            previous_target = stable.readlink()

            with self.assertRaisesRegex(RuntimeError, "conversion failed"):
                converter.publish_bundle(models, lambda stage: (_ for _ in ()).throw(RuntimeError("conversion failed")))
            self.assertEqual(stable.readlink(), previous_target)
            self.assertEqual(len(list((models / "bundles").glob("bundle-b-*"))), 1)
            self.assertEqual(list((models / "bundles").glob(".bundle-b-*.staging")), [])

    def test_bundle_publication_refuses_physical_stable_directory(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            models = Path(directory) / "models"
            (models / "bundle-b").mkdir(parents=True)
            with self.assertRaisesRegex(ValueError, "physical directory"):
                converter.publish_bundle(models, lambda stage: {})

    def test_packaged_source_marker_is_accepted_and_tampering_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "source"
            source.mkdir()
            (source / ".uncanny-source-pin").write_text('{"commit":"abc","tree":"def"}', encoding="utf-8")
            self.assertEqual(converter.require_pinned_clean_source(source, "abc", "def", "test")["tree"], "def")
            (source / ".uncanny-source-pin").write_text('{"commit":"abc","tree":"bad"}', encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "marker"):
                converter.require_pinned_clean_source(source, "abc", "def", "test")

    def test_explicit_invalid_operation_id_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            with self.assertRaisesRegex(ValueError, "operation ID"):
                converter.publish_bundle(Path(directory) / "models", lambda stage: {}, "not-an-operation-id")

    def test_deferred_publication_keeps_stable_target_unchanged(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            models = Path(directory) / "models"
            old = models / "bundles" / "bundle-b-old"
            old.mkdir(parents=True)
            (models / "bundle-b").symlink_to(Path("bundles") / old.name)
            report = converter.publish_bundle(models, lambda stage: {"artifact": {"path": str(stage / "x")}}, "0123456789abcdef0123456789abcdef", True)
            self.assertEqual((models / "bundle-b").readlink(), Path("bundles") / old.name)
            self.assertTrue(Path(report["publication"]["version"]).is_dir())


if __name__ == "__main__":
    unittest.main()
