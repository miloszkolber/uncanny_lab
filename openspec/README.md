# Documentation

This repository uses [OpenSpec](https://github.com/Fission-AI/OpenSpec) to keep source-confirmed behavior, proposed changes and verification separate. Start with [project context](project.md).

| Location | Maintained content |
| --- | --- |
| [Generation jobs](specs/generation-jobs/spec.md) | Registry admission, queue, recovery, cancellation and artifacts |
| [Checkpoint installation](specs/checkpoint-installation/spec.md) | Opt-in catalog, policy, verification and publication |
| [HTTP security](specs/http-security/spec.md) | Host/origin guards, uploads and contained access |
| [UI and runtime](specs/ui-runtime/spec.md) | Imported package, caching, health and worker prerequisites |
| [Release pipeline](specs/release-pipeline/spec.md) | Manual publication and retag safeguards |
| [Runtime reference](references/runtime.md) | Commands, configuration, conversion and verification boundaries |
| [Backlog](backlog.md) | Deferred work, not implementation authorization |
| [Changes](changes/) | Scoped proposals, tasks and evidence |
| [History](changes/history.md) | Documentation baseline and dated checks |
| [Configuration](config.yaml) | OpenSpec context and artifact rules |

## Working a change

1. Read affected specs and implementation. Specs state contracts, not proof that every scenario has been executed.
2. For behavior changes, create `changes/<name>/proposal.md` with intent, scope, affected capabilities, status and authorization. Add `specs/<capability>/spec.md` deltas and numbered `tasks.md` checkboxes. Add `design.md` only for consequential choices.
3. Review scope before implementation. A proposal is not permission to install checkpoints, run generation, publish or deploy.
4. Verify scenarios and record actual commands, results and gaps in `evidence.md`. Keep mocks, CPU fixtures, real engine/XPU acceptance and deployment checks distinct.
5. After verification and review, archive the change using OpenSpec, merge deltas into main specs and validate the assembled result again.

Main specs use `## Purpose`, `## Requirements`, `### Requirement: Name` and `#### Scenario: Name`. Requirements use SHALL or MUST. Scenarios use concrete **WHEN** and **THEN** steps, with **GIVEN** and **AND** where useful. Deltas use ADDED, MODIFIED, REMOVED or RENAMED Requirements. MODIFIED replaces the whole requirement including retained scenarios.

Small documentation corrections need a reviewed diff and checks, not invented behavior deltas. For scoped documentation-only work use `skip_specs: true` in `.openspec.yaml`. Do not manufacture historical application archives. Host-specific state, credentials and incidents belong in deployment documentation.

## Tooling

`openspec/` is the physical documentation and planning directory, not a second copy or a `docs/` alias. Run from the repository root in a development container:

```sh
OPENSPEC_TELEMETRY=0 bun x --bun @fission-ai/openspec@1.14.1 list --specs
OPENSPEC_TELEMETRY=0 bun x --bun @fission-ai/openspec@1.14.1 validate --all --strict
```

OpenSpec is development tooling, not an image dependency. No global installation or generated chat commands are required. To scaffold a behavior change use the same invocation with `new change <name>`. After review use `archive <name>` and rerun validation. Never skip required spec merging or suppress validation to force archival. Structural validation is not implementation acceptance. Local Mewa asset checks need the ignored package input documented in the runtime reference.
