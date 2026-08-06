# Legacy Image Lab

Legacy Image Lab is a local generative-art instrument for optimization-based neural image techniques. It uses a Go control plane, replaceable Python workers, and PyTorch XPU on Intel Arc hardware.

This implementation delivers the Phase 1 platform foundation:

- embedded Go web interface and local REST API
- persistent SQLite job index and filesystem artifacts
- one-worker FIFO GPU queue
- subprocess cancellation and startup recovery
- newline-delimited JSON worker protocol
- live progress over server-sent events
- engine manifests
- reproducible job directories and metadata
- PyTorch XPU diagnostics with explicit CPU fallback
- an iterative `test-pattern` engine that validates the complete Go → Python → tensor runtime → artifact path

Deep Daze and Neural Style Transfer are the next vertical slice. They are not presented as implemented engines yet.

## Run in Docker

The container targets Intel Arc B-series GPUs and pins Intel's PyTorch 2.11 XPU image (`xpu-2.11.0-ubuntu24.04`) by digest. Verify that the host kernel supports the installed GPU.

Create the bind-mount directories with ownership matching the container user:

```bash
sudo install -d -o 1000 -g 1000 \
  /data/models/legacy-image-lab \
  /data/storage/legacy-image-lab/inputs \
  /data/storage/legacy-image-lab/outputs \
  /var/lib/legacy-image-lab/workspace
```

Determine the host device group IDs and pass them when they differ from the Compose defaults:

```bash
export VIDEO_GID="$(stat -c %g /dev/dri/card0)"
export RENDER_GID="$(stat -c %g /dev/dri/renderD128)"
docker compose up --build
```

Open `http://localhost:8080`. Compose maps `/dev/dri` because PyTorch XPU needs the render device. It uses host IPC because PyTorch uses shared memory for tensor and worker data exchange.

## Develop locally

Go 1.24 and Python 3.11 or later are required. PyTorch is optional for control-plane development. The test engine has a slower standard-library CPU fallback.

```bash
go mod download
make test
make run
```

The development configuration writes disposable state under `/tmp/legacy-image-lab` and listens only on `127.0.0.1:8080`.

## API

Key endpoints:

```text
GET  /healthz
GET  /api/engines
GET  /api/jobs
POST /api/jobs
GET  /api/jobs/{id}
POST /api/jobs/{id}/cancel
POST /api/jobs/{id}/duplicate
GET  /api/events
GET  /api/system
```

Submit a small integration job:

```bash
curl -H 'Content-Type: application/json' \
  -d '{"engine":"test-pattern","parameters":{"prompt":"signal","width":64,"height":64,"iterations":5}}' \
  http://localhost:8080/api/jobs
```

Each job is stored under `/workspace/jobs/<job-id>` with its specification, previews, final image, logs, and completion metadata.

## Project boundaries

The initial service assumes one trusted local user and should not be exposed directly to the public internet. Model installation is explicit. Workers never download checkpoints during generation. Large artifacts remain on disk rather than in SQLite.
