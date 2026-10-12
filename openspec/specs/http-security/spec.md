# HTTP security

## Purpose

Protect the trusted local HTTP surface from unapproved hosts, cross-origin mutations and unsafe file access without claiming user authentication.

Source: [API and guards](../../../internal/api/api.go), [host configuration](../../../internal/config/config.go), [image materialization](../../../internal/orchestrator/orchestrator.go), [Compose](../../../compose.yaml).
Checks: [API tests](../../../internal/api/api_test.go), [configuration tests](../../../internal/config/config_test.go), [materialization tests](../../../internal/orchestrator/materialize_test.go).
Deployment boundary: [runtime reference](../../references/runtime.md#data-and-security).

## Requirements

### Requirement: Explicit host allowlist
HTTP requests MUST pass the configured Host-header allowlist. Default entries SHALL cover loopback hosts at the configured port, and explicit configuration MUST reject empty entries and wildcards.

#### Scenario: DNS-rebinding host
- **WHEN** a request uses a Host value outside the allowlist
- **THEN** the server responds with HTTP 421 and `UNTRUSTED_HOST`

#### Scenario: Deliberate proxy configuration
- **WHEN** an operator configures an additional complete Host-header value
- **THEN** that value is accepted without introducing a wildcard allowlist

### Requirement: Origin and content-type mutation checks
JSON mutation handlers SHALL require JSON content type and reject an Origin header whose host differs from the request Host. An absent Origin header SHALL remain permitted rather than being represented as authentication.

#### Scenario: Cross-origin mutation
- **WHEN** a mutation includes an Origin with a different host
- **THEN** the API rejects it with `CROSS_ORIGIN_REQUEST`

#### Scenario: Invalid JSON media type
- **WHEN** a JSON mutation uses an invalid content type
- **THEN** it is rejected rather than processed as JSON by suffix guessing

### Requirement: Contained inputs and artifacts
Uploads SHALL validate supported image content and dimensions and normalize accepted image data. Job file access and deletion MUST reject unsafe job directories and path escapes.

#### Scenario: Oversized image dimensions
- **WHEN** an uploaded image exceeds allowed dimensions
- **THEN** it is rejected rather than admitted as an input token

#### Scenario: Symlinked job directory
- **WHEN** deletion targets a symlinked job directory
- **THEN** it is rejected without deleting the symlink's target

### Requirement: Response privacy and execution boundaries
JSON API responses SHALL use `no-store`, and HTTP responses SHALL apply the same-origin CSP, no-referrer, nosniff and frame-denial headers. Compose MUST default to loopback exposure without claiming a network egress sandbox.

#### Scenario: Browser receives API state
- **WHEN** an API response is returned
- **THEN** caching is disabled and security headers constrain browser execution

#### Scenario: Default deployment binding
- **WHEN** the reference Compose configuration is rendered without overrides
- **THEN** HTTP is published on `127.0.0.1`, not all host interfaces
