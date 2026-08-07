# Uncanny Lab

Uncanny Lab is a private, local generative-art instrument for optimization-based neural image techniques. A Go control plane owns the web interface, SQLite history, one-worker queue, process lifecycle, uploads, model inventory, and artifacts. Replaceable Python workers own PyTorch execution through a shared XPU/CPU runtime.

The application deliberately focuses on visible optimization, unstable representations, classifier bias, intermediate frames, and pre-diffusion visual processes.

## Engines

| Workflow | Engine | Local assets |
| --- | --- | --- |
| Text to image | Deep Daze | OpenCLIP ViT-B-32 state dictionary |
| Text to image | VQGAN + CLIP | OpenCLIP state dictionary, VQ codebook, and portable TorchScript decoder |
| Text to image | Big Sleep | OpenCLIP state dictionary and portable class-conditioned BigGAN generator |
| Image to image | Neural Style Transfer | TorchVision-compatible VGG19 state dictionary |
| Image to image | DeepDream | TorchVision-compatible VGG19 state dictionary |
| Image to image | Activation Maximization | TorchVision-compatible VGG19 state dictionary |
| Image to image | Deep Image Prior | No checkpoint |

Workers never download checkpoints. The Models screen reports expected files and verifies local hashes on request. This keeps generation reproducible and prevents a prompt from causing network access.

Engines whose required checkpoints are absent are disabled in the generator and rejected by the API. Deep Image Prior remains available without a checkpoint. Install licensed checkpoints on the host before using the other engines.

Expected default paths inside the data volume:

```text
/data/models/classifiers/vgg19.pt
/data/models/clip/vit-b-32.pt
/data/models/vqgan/decoder.pt
/data/models/vqgan/codebook.pt
/data/models/biggan/generator.pt
```

VGG files must contain a TorchVision-compatible VGG19 `state_dict`. CLIP files must contain an OpenCLIP `ViT-B-32` `state_dict`. The VQGAN decoder accepts a quantized BCHW embedding grid and returns BCHW RGB while its codebook is a weights-only state dictionary containing one `[codes, channels]` embedding tensor. The BigGAN generator accepts both a latent tensor and class-probability tensor, preserving class-conditioned optimization rather than treating BigGAN as an unconditional decoder.

Optional model descriptors live at `/data/models/registry/<id>.json`:

```json
{
  "id": "clip-vit-b-32",
  "path": "clip/vit-b-32.pt",
  "sha256": "optional expected hash",
  "family": "CLIP",
  "engines": ["deep-daze", "vqgan-clip", "big-sleep"],
  "license": "checkpoint license",
  "notes": "local OpenCLIP state_dict"
}
```

## Storage and isolation

The container has one writable external data mount:

```text
/var/lib/uncanny-lab → /data
```

It contains the SQLite index, models, uploads, job specifications, logs, previews, final images, and exports. The only other host mount is the shared Core UI library, read-only. The root filesystem is read-only, Linux capabilities are dropped, privilege escalation is disabled, process count is bounded, `/tmp` and `/run` are isolated tmpfs mounts, and the container uses private shared memory rather than host IPC. The Intel render node is the sole device mapping required for PyTorch XPU.

Prepare storage with the container user's ownership:

```bash
sudo install -d -m 0750 -o 1000 -g 1000 /var/lib/uncanny-lab
export RENDER_GID="$(stat -c %g /dev/dri/renderD128)"
docker compose up --build
```

Open `http://localhost:8080`. Compose binds the service only to host loopback, and the HTTP server accepts only loopback Host headers to resist DNS rebinding. Workers are configured for local checkpoints and Hugging Face offline mode, but the Docker network is not an egress security boundary. Use an authenticated reverse proxy or tunnel rather than widening the port binding. A reverse proxy must send `Host: localhost:8080` upstream.

## Workflow

```text
choose Text to image or Image to image
→ select an engine and its real algorithm controls
→ upload source/style material where applicable
→ queue one GPU job
→ inspect live previews and any earlier iteration
→ cancel or complete without losing frames
→ open the visual history
→ rerun, export ZIP, or send the result to another engine
```

Every job directory under `/data/workspace/jobs/<job-id>` preserves `job.json`, normalized inputs, preview frames, available output images, stdout/stderr logs, and terminal `metadata.json`. SQLite is only the searchable index. At startup, terminal jobs missing from SQLite are restored from their durable metadata. Back up the entire external data directory as one unit.

## API

The embedded UI uses the same local API available to scripts:

```text
GET    /api/engines
GET    /api/models
POST   /api/models/{id}/verify
POST   /api/uploads
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
GET    /api/uploads/{token}
GET    /artifacts/{id}/{path}
```

Mutations enforce same-origin browser requests. Uploads are size and pixel bounded, decoded and normalized to PNG, and assigned random names. Worker inputs and model paths are resolved beneath approved roots with symlink and traversal checks.

## Development and validation

The development configuration listens on `127.0.0.1:8080` and stores disposable state under `/tmp/uncanny-lab`.

```bash
make test
make vet
make run
docker compose config --quiet
docker compose build uncanny-lab
```

The pinned runtime is Intel PyTorch 2.11 XPU on Ubuntu 24.04. CPU fallback supports development and checkpoint-free smoke tests, but large artistic runs are intended for Intel Arc.
