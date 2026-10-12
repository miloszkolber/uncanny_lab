# Release pipeline

## Purpose

Document manual build/publication and retag safeguards without treating the unresolved checkout input or unexecuted image checks as accepted deployment.

Source: [build workflow](../../../.github/workflows/build.yml), [retag workflow](../../../.github/workflows/retag.yml), [Dockerfile](../../../Dockerfile), [ignore policy](../../../.gitignore).
Verification owners: workflow Go/race, Python syntax and source-mounted unit tests, UI/package checks, Compose checks and final-image CPU smoke steps. These require a complete UI input that the checkout does not supply.
Commands and release defaults: [runtime reference](../../references/runtime.md#quick-start).

## Requirements

### Requirement: Manual serialized publication
Build/publication and retagging SHALL require workflow dispatch, validate semantic-version targets and share a non-cancelling publication concurrency group.

#### Scenario: Release dispatch
- **WHEN** an operator dispatches a build with a non-semantic version
- **THEN** version validation fails rather than publishing the supplied tag

### Requirement: Publication gated on declared checks
The build workflow SHALL place source checks, one Linux amd64 image build, image Python tests and final-image CPU health/UI smoke checks before registry publication.

#### Scenario: Missing UI checkout input
- **WHEN** the workflow runs from a checkout without the required ignored Mewa snapshot
- **THEN** the package-dependent checks or Docker copy cannot satisfy the declared gates
- **AND** the source layout is not described as a working clean-checkout publication path

#### Scenario: Failed image acceptance
- **WHEN** the tested image fails the configured architecture, version-label, health or foundation-byte checks
- **THEN** the workflow fails before its publication steps

### Requirement: Immutable version and revision tags
Publication MUST reject an existing version or revision tag that points to a different tested image, while accepting an identical image idempotently. Registry inspection errors MUST NOT be treated as proof that a tag is absent.

#### Scenario: Existing conflicting release
- **WHEN** a release tag already points to a different image digest
- **THEN** publication fails instead of overwriting it

#### Scenario: Registry inspection failure
- **WHEN** tag inspection fails for a reason other than a recognized missing manifest
- **THEN** the workflow fails instead of proceeding as if the tag were unused

### Requirement: Metadata-consistent retagging
Retagging SHALL reuse an existing image without rebuilding only when its version label equals the target semantic version and any existing target has the same image digest.

#### Scenario: Mismatched version label
- **WHEN** the source image's version metadata differs from the requested target
- **THEN** retagging fails rather than attaching inconsistent release metadata

#### Scenario: Already identical target
- **WHEN** the target already points to the source image digest
- **THEN** the target is accepted idempotently, not treated as a conflicting release
