from legacy_lab.engines.base import Engine
from legacy_lab.engines.test_pattern import TestPatternEngine


ENGINES: dict[str, Engine] = {TestPatternEngine.id: TestPatternEngine()}


def get_engine(engine_id: str) -> Engine:
    try:
        return ENGINES[engine_id]
    except KeyError as error:
        raise ValueError(f"unknown engine: {engine_id}") from error
