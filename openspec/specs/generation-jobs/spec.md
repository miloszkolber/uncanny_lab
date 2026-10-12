# Generation jobs

## Purpose

Run locally admitted engines through one recoverable worker queue and preserve job inputs, progress and results.

Source: [API](../../../internal/api/api.go), [job validation](../../../internal/jobs/jobs.go), [registry](../../../internal/engines/registry.go), [compatibility catalog](../../../internal/engines/compatibility.go), [orchestrator](../../../internal/orchestrator/orchestrator.go), [worker runner](../../../python/uncanny_lab/runner.py), [device selection](../../../python/uncanny_lab/runtime/device.py).
Checks: [API tests](../../../internal/api/api_test.go), [job tests](../../../internal/jobs/jobs_test.go), [orchestrator tests](../../../internal/orchestrator/orchestrator_test.go), [materialization tests](../../../internal/orchestrator/materialize_test.go), [worker tests](../../../python/tests/test_worker.py).
Commands and dependency boundaries: [runtime reference](../../references/runtime.md#build-and-test).

## Requirements

### Requirement: Registry and model admission
Job creation SHALL validate the engine, manifest defaults, required inputs and required local model availability before queueing work. Known unsupported engines MUST return their compatibility diagnostic rather than create a job.

#### Scenario: Missing required model
- **WHEN** a model-dependent engine is requested without an available required model
- **THEN** job creation is rejected instead of starting a worker that downloads it implicitly

#### Scenario: DALL-E Mini request
- **WHEN** a valid job request selects `dalle-mini`
- **THEN** the API returns `DALL_E_MINI_UNSUPPORTED` without creating a job
- **AND** the same boundary applies to duplication of an unsupported job

### Requirement: Serial execution and restart recovery
The orchestrator SHALL execute one generation worker at a time, restore queued jobs after restart and mark interrupted active jobs failed rather than pretend to resume their optimization state.

#### Scenario: Queued and interrupted jobs
- **WHEN** startup recovery finds queued jobs and jobs in preparing, loading-model, running or saving states
- **THEN** queued jobs are re-enqueued
- **AND** interrupted active jobs become failed with `ENGINE_CRASHED`

### Requirement: Cancellation respects terminal state
Cancellation SHALL terminate active worker process groups and reject attempts to cancel jobs that have already reached a terminal state.

#### Scenario: Running worker cancellation
- **WHEN** cancellation is admitted for an active worker
- **THEN** the process group receives SIGTERM with delayed force-kill handling
- **AND** the resulting job is recorded as cancelled rather than successful generation

#### Scenario: Completion wins the race
- **WHEN** completion has already been committed before cancellation is claimed
- **THEN** cancellation reports that the job has finished rather than overwriting completed state

### Requirement: Durable inputs and bounded previews
The server SHALL materialize image inputs as contained regular files and preserve durable copies when reusing previews or duplicating jobs. Completed-job preview retention SHALL follow `max_frames`, where zero disables pruning.

#### Scenario: Original job deleted
- **WHEN** a job using a durable copied input outlives the job that supplied it
- **THEN** deleting the original job does not remove the copied input needed by the new job

#### Scenario: Preview retention limit
- **WHEN** a completed job has more preview frames than a positive `max_frames` limit
- **THEN** older frames are pruned and retained previews remain available to the UI

### Requirement: Explicit runtime fallback
The Python runtime SHALL select CPU when requested XPU is unavailable, report that fallback and reject fp16 on CPU.

#### Scenario: No available XPU
- **WHEN** XPU is requested but unavailable with fp32 precision
- **THEN** runtime selection reports CPU with `fallback: true`

#### Scenario: CPU fp16
- **WHEN** fp16 is requested and the selected device is CPU
- **THEN** the worker reports `UNSUPPORTED_PRECISION`
