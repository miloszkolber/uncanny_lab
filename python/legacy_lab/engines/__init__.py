from legacy_lab.engines.base import Engine
from legacy_lab.engines.clip import BigSleepEngine, DeepDazeEngine, VQGANClipEngine
from legacy_lab.engines.dip import DeepImagePriorEngine
from legacy_lab.engines.test_pattern import TestPatternEngine
from legacy_lab.engines.vision import ActivationMaxEngine, DeepDreamEngine, NeuralStyleEngine


ENGINES: dict[str, Engine] = {engine.id: engine for engine in (TestPatternEngine(), NeuralStyleEngine(), DeepDreamEngine(), ActivationMaxEngine(), DeepImagePriorEngine(), DeepDazeEngine(), VQGANClipEngine(), BigSleepEngine())}


def get_engine(engine_id: str) -> Engine:
    try:
        return ENGINES[engine_id]
    except KeyError as error:
        raise ValueError(f"unknown engine: {engine_id}") from error
