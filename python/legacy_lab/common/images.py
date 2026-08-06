"""Small image helpers that keep the infrastructure engine dependency-free."""

from __future__ import annotations

import struct
import zlib
from pathlib import Path


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
