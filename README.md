# Uncanny Lab

A local playground for pre-diffusion image-generation algorithms and their visible optimization processes. Run text-to-image or image-to-image engines, tune parameters and follow intermediate frames through the combined browser UI and backend.

- [Project scope and ownership](openspec/project.md)
- [OpenSpec documentation and change workflow](openspec/README.md)
- [Quick start, checkpoints, configuration, build and verification](openspec/references/runtime.md)
- [Deferred work and build limitations](openspec/backlog.md)
- [Security policy](SECURITY.md)

Compose defaults to a loopback-only service on port 8080 with an Intel XPU runtime. A CPU override is available. Checkpoint downloads are disabled by default and model terms require operator review. The Docker build also requires a locally imported Mewa UI snapshot: `ui/` is ignored by Git and is not supplied by a clean checkout.

## DALL-E Mini compatibility

DALL-E Mini is not supported by this PyTorch/XPU build. Bundle B installation does not make it runnable. Requests for `dalle-mini` return an explicit compatibility diagnostic without creating a job. See [compatibility details](openspec/references/runtime.md#dall-e-mini-compatibility).
