# Project context

## Scope

Uncanny Lab runs pre-diffusion image optimization algorithms through a Go HTTP server and a single Python worker queue. Supported algorithms and checkpoint prerequisites are listed in [the runtime reference](references/runtime.md#supported-engines). Local SQLite data, specifications, logs, previews and results preserve job history.

| Source | Responsibility |
| --- | --- |
| `cmd/server/`, `internal/config/` | Startup, healthcheck and configuration |
| `internal/api/`, `internal/jobs/`, `internal/database/` | HTTP contracts, job validation and persistence |
| `internal/orchestrator/`, `internal/events/` | Worker lifecycle, artifact materialization and progress |
| `internal/engines/`, `manifests/engines/`, `python/uncanny_lab/` | Engine registry, local model checks and implementations |
| `internal/modelinstall/`, `tools/` | Opt-in fixed Bundle B installation and conversion |
| `web/`, `scripts/`, local `ui/` | Embedded application UI and imported Mewa assets |
| `Dockerfile`, `compose*.yaml`, `.github/workflows/` | Image, reference deployment and manual publication |

## Goals and boundaries

- Keep generation, intermediate frames and saved results local to one operator's service.
- Support the pinned PyTorch Intel XPU runtime and CPU development mode without claiming CPU checks establish XPU support.
- Keep checkpoint acquisition opt-in, fixed-catalog and separate from software licensing.
- Preserve one maintained source for commands, contracts, proposals and dated evidence.

The service is designed for trusted local access, not an authenticated multi-user deployment. Compose binds loopback by default. Host-header and same-origin guards are not user authentication or an egress sandbox. Checkpoint and output rights are independent of the MIT application license. DALL-E Mini requires a separate worker environment and is not implemented here.

The Dockerfile needs the complete imported Mewa package, but `ui/` is ignored and absent from Git. The local package is present in this workspace. The manual release workflow does not provision it on checkout. This source-confirmed build limitation is tracked in [backlog](backlog.md), not promoted into a clean-checkout guarantee.

## Owners

| Subject | Maintained owner |
| --- | --- |
| Implemented behavior | `openspec/specs/<capability>/spec.md` and source |
| Proposed behavior, tasks and evidence | One scoped folder in `openspec/changes/` |
| Build, install, configuration and verification commands | [Runtime reference](references/runtime.md) |
| Deferred work and unresolved ownership | [Backlog](backlog.md) |
| Vulnerability disclosure | [Security policy](../SECURITY.md) |
| Model licenses and runtime attribution | Upstream model terms, [LICENSE](../LICENSE), [third-party notices](../THIRD_PARTY_NOTICES) |
| Host credentials, devices, persistent data and deployment | External deployment runbook |
| Mewa package generation | Upstream Mewa maintainer |

Image publication does not deploy. A proposed change is not permission to install checkpoints, restart services or run generation. Never persist private credentials in documentation or diagnostics.
