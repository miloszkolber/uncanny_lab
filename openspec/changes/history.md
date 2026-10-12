# Change history

## OpenSpec adoption — 2026-10-12

Moved maintained README guidance into `openspec/references/runtime.md`, added source-backed capability specs, project ownership, artifact rules and a deferred-work backlog, and kept the root README as a short entry point. Preserved the root DALL-E Mini compatibility anchor for existing diagnostic references. The layout follows `oc_build`'s separation of contracts, commands, proposals and dated evidence. No application code, workflows, configuration, immutable UI bytes or model state changed.

Corrected the clean-checkout claim: the local Mewa snapshot is ignored by Git and the release workflow does not provision it. Recorded the build-input gap without changing the tracking policy. Clarified retag idempotency and that the optional worker suite includes tiny fixture generation when dependencies are available.

Executed in the development container:

- The `oc_build` local link/scenario checker: 12 Markdown files, zero failures.
- OpenSpec 1.14.1 `list --specs` and `validate --all --strict`: all 5 specs passed.
- `go test -race ./...` and `go vet ./...`: passed.
- The five suites listed in the runtime reference's Bun test command: 28 tests passed.
- `bun scripts/sync-mewa-ui.test.mjs`: importer regressions passed.
- The active snapshot `--check`: verified identity and all 598 files in the locally supplied package.
- Python `compileall` for `python` and `tools`, with bytecode redirected to temporary storage: passed.
- `bun build web/static/app.js --target browser`: passed, with output in temporary storage.

Both reference Compose configurations passed `config --quiet` using the host Docker CLI. Git checks confirmed documentation-only tracked changes and that `ui/` remains ignored and untracked. Local asset-check success does not resolve the clean-checkout build-input gap.

Python worker/conversion unit tests were not run: the development container lacks `torch`, `torchvision`, `PIL`, `yaml` and `open_clip`. No dependencies were installed to force those tests. No image build, browser render review, checkpoint download/conversion, engine generation, XPU check, publication or deployment was performed.
