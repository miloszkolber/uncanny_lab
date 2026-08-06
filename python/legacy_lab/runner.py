from __future__ import annotations

import argparse
import json
import signal
import sys
from pathlib import Path
from typing import Any

from legacy_lab.common.progress import diagnostic, fail
from legacy_lab.engines import get_engine
from legacy_lab.errors import WorkerError
from legacy_lab.runtime.device import Runtime


def parse_arguments() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Legacy Image Lab worker")
    parser.add_argument("--engine")
    parser.add_argument("--job", type=Path)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--device", choices=("xpu", "cpu"), default="xpu")
    return parser.parse_args()


def load_job(path: Path) -> dict[str, Any]:
    with path.open("r", encoding="utf-8") as stream:
        value = json.load(stream)
    if not isinstance(value, dict):
        raise ValueError("job specification must be an object")
    return value


def run(args: argparse.Namespace) -> int:
    if args.self_test:
        runtime = Runtime.create(args.device)
        print(json.dumps(runtime.report(), separators=(",", ":")))
        return 0
    if not args.engine or not args.job:
        diagnostic("--engine and --job are required")
        return 2
    try:
        job = load_job(args.job)
        engine = get_engine(args.engine)
        parameters = engine.validate(job.get("parameters", {}))
        runtime_config = job.get("runtime", {})
        runtime = Runtime.create(str(runtime_config.get("device", "xpu")), str(runtime_config.get("precision", "fp32")))
        engine.generate(job, parameters, runtime, args.job.resolve().parent)
        return 0
    except WorkerError as error:
        fail(error.code, error.message)
        return 1
    except ValueError as error:
        fail("INVALID_PARAMETERS", str(error))
        return 1
    except MemoryError:
        fail("OUT_OF_MEMORY", "Unable to allocate memory for this generation")
        return 1
    except Exception as error:
        # Torch reports allocator exhaustion as RuntimeError, including on XPU.
        if "out of memory" in str(error).lower():
            fail("OUT_OF_MEMORY", "Unable to allocate memory for this generation")
            return 1
        diagnostic(f"{type(error).__name__}: {error}")
        fail("ENGINE_CRASHED", "The engine stopped unexpectedly")
        return 1


def terminate(_signum: int, _frame: object) -> None:
    fail("CANCELLED", "Generation cancelled")
    raise SystemExit(143)


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, terminate)
    signal.signal(signal.SIGINT, terminate)
    sys.exit(run(parse_arguments()))
