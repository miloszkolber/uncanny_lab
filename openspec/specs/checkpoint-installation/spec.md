# Checkpoint installation

## Purpose

Provide one explicitly enabled, fixed-catalog Bundle B installer without treating software licensing as checkpoint permission.

Source: [configuration](../../../internal/config/config.go), [catalog and policy](../../../internal/modelinstall/catalog.go), [manager](../../../internal/modelinstall/manager.go), [installer API](../../../internal/api/api.go), [converter](../../../tools/convert_bundle_b.py).
Checks: [configuration tests](../../../internal/config/config_test.go), [catalog tests](../../../internal/modelinstall/catalog_test.go), [manager tests](../../../internal/modelinstall/manager_test.go), [API tests](../../../internal/api/api_test.go), [conversion tests](../../../python/tests/test_conversion.py).
Conversion commands and terms: [runtime reference](../../references/runtime.md#checkpoints), [security policy](../../../SECURITY.md).

## Requirements

### Requirement: Explicit enablement and policy acceptance
Checkpoint installation SHALL be disabled by default and require explicit enablement, affirmative policy acceptance and the active policy version before starting an operation.

#### Scenario: Default disabled installer
- **WHEN** a client requests installation without enabled checkpoint downloads
- **THEN** the API rejects it with `DOWNLOADS_DISABLED`

#### Scenario: Stale policy
- **WHEN** an enabled installation request accepts a policy version other than the active one
- **THEN** the API returns `STALE_POLICY` rather than starting a download

### Requirement: Fixed verified upstream downloads
The installer MUST use its fixed catalog, restrict HTTPS downloads and redirects to approved hosts, reject non-public resolved addresses and verify exact source byte counts and SHA-256 before conversion.

#### Scenario: Corrupt source bytes
- **WHEN** a download's size or SHA-256 differs from the catalog entry
- **THEN** it fails without publishing that source as verified

#### Scenario: Unsafe address or redirect
- **WHEN** an approved hostname resolves to a non-public address or a redirect changes to an unapproved host
- **THEN** the request is rejected rather than dialing the untrusted destination

### Requirement: Verified local conversion and recoverable publication
The installer SHALL convert using pinned bundled source code, verify candidate provenance and artifacts and publish through the stable Bundle B target with prior-target recovery.

#### Scenario: Invalid candidate after interruption
- **WHEN** recovery finds an interrupted publication whose candidate fails verification
- **THEN** the prior valid target is restored rather than accepting the invalid candidate

#### Scenario: Publication already committed
- **WHEN** a client attempts cancellation after publication has committed
- **THEN** cancellation is rejected rather than claiming the published bundle was cancelled

### Requirement: Separate checkpoint permissions
Installer policy and documentation MUST distinguish the MIT application license from checkpoint terms and disclose uncertain VQGAN and BigGAN checkpoint-specific permissions.

#### Scenario: Operator reviews installer
- **WHEN** the installer catalog and policy are presented before installation
- **THEN** the operator sees source files, hashes, conversion-source pins and responsibility for checkpoint permissions
- **AND** generated-image rights are not guaranteed by the application license
