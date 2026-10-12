# UI and runtime

## Purpose

Serve embedded application assets and an imported immutable Mewa package alongside the Python worker runtime.

Source: [web handler](../../../web/embed.go), [client](../../../web/static/app.js), [page](../../../web/static/index.html), [importer](../../../scripts/sync-mewa-ui.mjs), [healthcheck](../../../cmd/server/main.go), [Dockerfile](../../../Dockerfile), [CPU override](../../../compose.cpu.yaml).
Checks: [web tests](../../../web/embed_test.go), [health tests](../../../cmd/server/main_test.go), [navigation tests](../../../web/navigation.test.mjs), [draft tests](../../../web/form-draft.test.mjs), [generation tests](../../../web/generation.test.mjs), [workflow tests](../../../web/workflow-state.test.mjs), [asset graph tests](../../../scripts/ui-assets.test.mjs), [importer tests](../../../scripts/sync-mewa-ui.test.mjs).
Commands: [runtime reference](../../references/runtime.md#build-and-test).

## Requirements

### Requirement: Verified immutable package import
The importer MUST validate complete package inventory, hashes and manifest references before staged publication under a per-identity lock, while preserving existing immutable identities.

#### Scenario: Corrupt or partial input
- **WHEN** a supplied package has missing, extra, symlinked or corrupt files
- **THEN** import fails rather than publishing a partial package

#### Scenario: Local image build with supplied UI
- **GIVEN** the complete required package has been imported into local `ui/`
- **WHEN** the Dockerfile builds the application
- **THEN** it copies that tree into `/ui` without a sibling Mewa build context or runtime asset mount

The local UI tree is ignored by Git. This conditional build contract does not promise that a clean checkout contains the package. The unresolved input is tracked in [backlog](../../backlog.md#clean-checkout-ui-build-input).

### Requirement: Static serving and cache boundaries
Successful canonical versioned UI asset paths SHALL use immutable caching. Embedded application assets SHALL revalidate, and missing UI content MUST NOT be advertised as an immutable success.

#### Scenario: Canonical versioned stylesheet
- **WHEN** an existing canonical versioned CSS asset is requested by GET or HEAD
- **THEN** it has the correct content type and `public, max-age=31536000, immutable`

#### Scenario: Missing or escaping path
- **WHEN** a UI request is missing, traverses outside the UI root or uses a rejected file path
- **THEN** it returns 404 with `no-store` instead of an immutable response

Serving does not reimplement the importer's complete checksum-inventory validation. Do not infer Submarine-style per-request inventory admission from a valid import.

### Requirement: Active UI health probe
Binary healthcheck mode SHALL request `/healthz` and the compiled active foundation stylesheet on loopback using `UNCANNY_PORT` when set, without invoking Python or a shell for the probe.

#### Scenario: Missing active foundation
- **WHEN** `/healthz` succeeds but the active foundation stylesheet is unavailable
- **THEN** `uncanny-lab -healthcheck` exits nonzero

### Requirement: Dynamic worker image and CPU reference mode
The image SHALL retain the pinned Intel PyTorch runtime, static Go server, imported UI, conversion sources and non-root default UID/GID 1000. The CPU Compose override MUST remove device and render-group mappings.

#### Scenario: CPU override
- **WHEN** both Compose files are rendered together
- **THEN** runtime device is CPU and XPU device/group mappings are reset
- **AND** this does not establish XPU engine acceptance or remove the image's Python dependency
