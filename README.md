# Uncanny Lab

Uncanny Lab is a local generative-art instrument for optimization-based neural image techniques. Its Go control plane provides the web interface, SQLite history, one-worker queue, process lifecycle, uploads, model inventory, and artifacts. Replaceable Python workers execute PyTorch workloads through a shared Intel XPU or CPU runtime.

The application deliberately focuses on visible optimization, unstable representations, classifier bias, intermediate frames, and pre-diffusion visual processes.

## Requirements

Images target Linux x86-64 (`linux/amd64`). Production generation requires a compatible Intel XPU runtime, access to its render device, and the Intel PyTorch XPU userspace included in the image. CPU mode is suitable for development and checkpoint-free smoke tests, but substantial generation workloads need XPU hardware.

The application expects an external Core UI library containing `core-ui.css` and `lucide.svg`. It is mounted read-only at `/ui-library`. By default Compose uses `./ui_library`, so create or bind that directory before starting the service.

Checkpoints are not downloaded or distributed by this repository or its image. Obtain them separately and comply with their terms. See [MODEL_LICENSING.md](MODEL_LICENSING.md) for the reviewed VQGAN and BigGAN checkpoint terms and the resulting local-use policy.

## Engines and model storage

| Workflow | Engine | Local assets |
| --- | --- | --- |
| Text to image | Deep Daze | OpenCLIP state dictionary |
| Text to image | VQGAN + CLIP | OpenCLIP state dictionary, VQ codebook, and portable TorchScript decoder |
| Text to image | Big Sleep | OpenCLIP state dictionary and portable class-conditioned generator |
| Image to image | Neural Style Transfer | TorchVision-compatible VGG19 state dictionary |
| Image to image | DeepDream | TorchVision-compatible VGG19 state dictionary |
| Image to image | Activation Maximization | TorchVision-compatible VGG19 state dictionary |
| Image to image | Deep Image Prior | No checkpoint |

Workers never download checkpoints. The Models screen reports expected files and verifies local hashes on request. Engines with missing required checkpoints are disabled in the generator and rejected by the API. Deep Image Prior remains available without a checkpoint.

The standard bundle layout inside the data mount is:

```text
/data/models/bundle-b/classifiers/vgg19.pt
/data/models/bundle-b/clip/vit-b-32.pt
/data/models/bundle-b/vqgan/decoder.pt
/data/models/bundle-b/vqgan/codebook.pt
/data/models/bundle-b/biggan/generator.pt
```

VGG files must contain a TorchVision-compatible VGG19 `state_dict`. CLIP files must contain an OpenCLIP `ViT-B-32` `state_dict` built with QuickGELU. The VQGAN decoder accepts a quantized BCHW embedding grid and returns BCHW RGB at 16 times the input spatial dimensions. Its codebook is a weights-only state dictionary containing `embedding.weight` `[16384,256]`. The class-conditioned generator accepts `z [N,128]` and class-probability tensors `[N,1000]` with truncation fixed to 1.0.

Optional descriptors live at `/data/models/registry/<id>.json`:

```json
{
  "id": "clip-vit-b-32",
  "path": "bundle-b/clip/vit-b-32.pt",
  "sha256": "optional expected hash",
  "family": "CLIP",
  "engines": ["deep-daze", "vqgan-clip", "big-sleep"],
  "license": "checkpoint license",
  "notes": "local OpenCLIP state_dict"
}
```

## Install and deploy

The public Linux AMD64 image is available at `ghcr.io/miloszkolber/uncanny-lab:latest`. Commit-SHA and semantic-version tags are also published by GitHub Actions. Compose is image-based and has no host-specific paths or source build step.

The base Compose file is for Intel XPU. It maps the selected render device and its access group:

```bash
mkdir -p data ui_library
export RENDER_GID="$(stat -c %g /dev/dri/renderD128)"
docker compose up -d
```

Compose variables are optional and can be supplied in the shell or a local `.env` file:

| Variable | Default | Purpose |
| --- | --- | --- |
| `UNCANNY_IMAGE` | `ghcr.io/miloszkolber/uncanny-lab:latest` | Image reference, including a pinned release or commit tag. |
| `UNCANNY_DATA_DIR` | `./data` | Host directory for all persistent state and locally obtained checkpoints. |
| `UNCANNY_UI_LIBRARY_DIR` | `./ui_library` | Required external Core UI library directory, mounted read-only. |
| `UNCANNY_UID` | `1000` | Numeric user ID used inside the container for mounted data. |
| `UNCANNY_GID` | `1000` | Numeric primary group ID used inside the container for mounted data. |
| `UNCANNY_RENDER_DEVICE` | `/dev/dri/renderD128` | Host Intel render-device node mapped to the fixed container render node. |
| `RENDER_GID` | `991` | Numeric group that can access `UNCANNY_RENDER_DEVICE`. |
| `UNCANNY_DEVICE` | `xpu` | Worker device for the XPU stack. The CPU override sets this to `cpu`. |
| `UNCANNY_BIND_ADDRESS` | `127.0.0.1` | Host address used for the published port. |
| `UNCANNY_PORT` | `8080` | Host and container HTTP port. |
| `UNCANNY_ALLOWED_HOSTS` | loopback hosts for `UNCANNY_PORT` | Comma-separated exact HTTP `Host` header values, such as `lab.example.test:8080`. |

Open `http://localhost:8080`. The default port binding is loopback-only. To publish through an authenticated reverse proxy or tunnel, set both `UNCANNY_BIND_ADDRESS` and `UNCANNY_ALLOWED_HOSTS` deliberately. The allowlist trims and lowercases entries, always includes loopback hosts for the configured port, and rejects empty or wildcard entries. Use complete Host header values, including non-default ports. A reverse proxy must preserve or set an allowed upstream `Host` value.

For CPU-only development or smoke testing, use the CPU override. It removes the XPU device and supplementary render group requirements and selects the CPU worker:

```bash
mkdir -p data ui_library
UNCANNY_IMAGE=uncanny-lab:local docker compose -f compose.yaml -f compose.cpu.yaml up -d
```

The container has one writable external mount, `/data`, which holds the SQLite index, models, uploads, job specifications, logs, previews, final images, and exports. The only other host mount is the external Core UI library, read-only. The root filesystem is read-only, capabilities are dropped, privilege escalation is disabled, process count is bounded, `/tmp` and `/run` are isolated tmpfs mounts, and the Intel render node is the only device mapping. Docker networking is not an egress security boundary. Mutating requests retain same-origin checks even when additional HTTP hosts are allowed.

Back up the complete data directory as one unit.

## Build from source

Build the production image locally when developing or testing a change:

```bash
docker build --platform linux/amd64 -t uncanny-lab:local .
UNCANNY_IMAGE=uncanny-lab:local docker compose up
```

For Go and Python development, use a copy of `config/config.dev.yaml` with absolute paths appropriate to your checkout and UI library. The sample assumes a source mount at `/workspace` and stores disposable data under `/tmp/uncanny-lab`.

```bash
make test
go test -race ./...
make vet
bun build web/static/app.js --target browser --outfile /tmp/uncanny-lab-app.js
docker compose config --quiet
docker compose -f compose.yaml -f compose.cpu.yaml config --quiet
tools/verify_image.sh uncanny-lab:local
```

## Bundle B conversion and provenance

Conversion is intentionally separate from the production image. It requires the production Python environment plus `git` and local source checkouts. Use generic absolute paths for your checkout, persistent data directory, and conversion sources:

```bash
docker run --rm --user 1000:1000 --entrypoint python \
  -e PYTHONPATH=/workspace/python \
  -v /absolute/path/to/uncanny-lab:/workspace:ro \
  -v /absolute/path/to/uncanny-data:/data \
  -v /absolute/path/to/taming-transformers:/sources/taming:ro \
  -v /absolute/path/to/pytorch-pretrained-BigGAN:/sources/biggan:ro \
  -w /workspace ghcr.io/miloszkolber/uncanny-lab:latest tools/convert_bundle_b.py \
  --sources /data/model-sources --models /data/models \
  --vgg-source /data/models/classifiers/vgg19.pt \
  --taming-source /sources/taming --biggan-source /sources/biggan
```

The converter requires its documented source revisions and clean source trees. It safely loads tensor-only checkpoints where supported, validates shapes, strict state loading, TorchScript interfaces, and input gradients. It builds all artifacts and provenance in one staging directory under `/data/models/bundles`, then atomically publishes `/data/models/bundle-b` as a stable symlink. Existing bundle versions remain available for rollback. The machine-readable report is `/data/models/bundle-b/provenance/bundle-b-conversion-report.json` and records source and output hashes, source trees, environment, canonical URLs, license references, interfaces, and validation cases.

## Workflow

```text
choose Text to image or Image to image
→ select an engine and its real algorithm controls
→ upload source/style material where applicable
→ queue one job
→ inspect live previews and earlier iterations
→ cancel or complete without losing frames
→ open the visual history
→ rerun, export ZIP, or send the result to another engine
```

Every job directory under `/data/workspace/jobs/<job-id>` preserves `job.json`, normalized inputs, preview frames, available output images, stdout/stderr logs, and terminal `metadata.json`. SQLite is the searchable index. At startup, terminal jobs missing from SQLite are restored from durable metadata.

## API

The embedded UI uses the same local API available to scripts:

```text
GET    /api/engines
GET    /api/models
POST   /api/models/{id}/verify
POST   /api/uploads
GET    /api/uploads/{token}
GET    /api/jobs
POST   /api/jobs
GET    /api/jobs/{id}
POST   /api/jobs/{id}/cancel
POST   /api/jobs/{id}/duplicate
POST   /api/jobs/{id}/use-as-input
GET    /api/jobs/{id}/export
DELETE /api/jobs/{id}
GET    /api/events
GET    /api/system
GET    /healthz
GET    /artifacts/{id}/{path}
```

Mutations enforce same-origin browser requests. Uploads are size- and pixel-bounded, decoded and normalized to PNG, and assigned random names. Worker inputs and model paths are resolved beneath approved roots with symlink and traversal checks.
