# Project guidance

Read [project context](openspec/project.md), affected capability specs and [the documentation workflow](openspec/README.md) before changing behavior. Commands live in [the runtime reference](openspec/references/runtime.md). Record executed checks separately from source findings and proposed acceptance.

Keep this project documentation host-neutral. Do not edit immutable UI package bytes. The ignored `ui/` build input and its clean-checkout limitation are documented in [the backlog](openspec/backlog.md), not resolved by assuming the package is tracked.

Use a development container for checks. Do not download checkpoints, install the large Python runtime, run generation against operator data, publish images or deploy solely to validate documentation. Those actions need their own scope and authorization. DALL-E Mini remains unsupported.
