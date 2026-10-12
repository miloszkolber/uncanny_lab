# Backlog

These items are not implementation authorization. Main specs describe existing source contracts, not these proposed outcomes.

## Clean-checkout UI build input

Status: unresolved build-input ownership. `.gitignore` excludes `ui/`, while the Dockerfile unconditionally copies it and package checks require the active snapshot. The release workflow checks out Git without supplying this tree. Local checks can use the imported package, but they do not demonstrate a self-contained clean-checkout build.

Recommended approach: vendor the maintainer-supplied complete immutable package, as the other application repositories do. An alternative is a verified, pinned retrieval step with an owned source and availability contract. Choose and authorize one approach before changing the tracking policy or workflow.

Acceptance: from a clean checkout with no sibling repository or pre-existing `ui/`, obtain the declared package through the chosen mechanism, verify its full inventory, run UI checks, and build/smoke-test the resulting image. This documentation change neither imports nor publishes a package.

## DALL-E Mini worker

Status: unsupported, not scheduled. A future implementation would need a separately pinned JAX/Flax worker, local model verification and independent CPU/XPU acceptance. Bundle B or configuration changes alone cannot enable it. Preserve the existing compatibility diagnostic until such support is implemented and verified.
