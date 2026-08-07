"""Image IO and tensor conversion shared by all engines."""

from __future__ import annotations

import struct
import zlib
from pathlib import Path
from typing import Any

from uncanny_lab.errors import WorkerError
from uncanny_lab.runtime.device import torch


def write_rgb_png(path: Path, width: int, height: int, pixels: bytes) -> None:
    if len(pixels) != width * height * 3:
        raise ValueError("RGB pixel buffer has the wrong length")
    rows = b"".join(b"\x00" + pixels[start : start + width * 3] for start in range(0, len(pixels), width * 3))

    def chunk(kind: bytes, data: bytes) -> bytes:
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)

    payload = b"\x89PNG\r\n\x1a\n"
    payload += chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
    payload += chunk(b"IDAT", zlib.compress(rows, level=6))
    payload += chunk(b"IEND", b"")
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_bytes(payload)
    temporary.replace(path)


def load_image(path: Path, width: int, height: int, device: str) -> Any:
    """Decode an RGB input into a bounded, normalized BCHW tensor."""
    if torch is None:
        raise WorkerError("UNSUPPORTED_MODEL", "PyTorch is not installed in this worker")
    try:
        from PIL import Image
        with Image.open(path) as source:
            image = source.convert("RGB").resize((width, height), Image.Resampling.LANCZOS)
            data = bytearray(image.tobytes())
    except Exception as error:
        raise WorkerError("INPUT_DECODE_FAILED", f"could not decode image {path.name}: {error}") from error
    return torch.frombuffer(data, dtype=torch.uint8).clone().view(height, width, 3).permute(2, 0, 1).unsqueeze(0).to(device=device, dtype=torch.float32).div_(255)


def save_tensor_png(path: Path, image: Any) -> None:
    if torch is None:
        raise WorkerError("UNSUPPORTED_MODEL", "PyTorch is not installed in this worker")
    value = image.detach().clamp(0, 1)[0].mul(255).to(torch.uint8).cpu().permute(1, 2, 0).contiguous()
    write_rgb_png(path, value.shape[1], value.shape[0], value.numpy().tobytes())
