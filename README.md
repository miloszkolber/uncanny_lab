# Uncanny Lab

Uncanny Lab is a local playground for early, pre-diffusion image-generation algorithms and their visible optimization processes. Run an engine, tune its parameters, and follow intermediate frames from the browser. The browser UI and backend are served together, with no separate API setup required.

## Features

- Local, one-worker image-generation queue with saved previews, artifacts, and history
- Text-to-image and image-to-image optimization workflows
- Intel XPU runtime with a CPU-only development mode
- Checkpoint downloads are disabled by default

## Supported engines

| Type | Engines |
| --- | --- |
| Text to image | Deep Daze, VQGAN + CLIP, Big Sleep |
| Image to image | Neural Style Transfer, DeepDream, Activation Maximization, Deep Image Prior |

Deep Image Prior needs no checkpoint. The other engines remain unavailable until their required local files are present.

## Checkpoints

Use the opt-in UI installer or download and convert checkpoints manually. In either case, verify their terms before use. Recommended sources are [TorchVision VGG19](https://download.pytorch.org/models/vgg19-dcbb9e9d.pth), [official OpenAI CLIP ViT-B/32](https://openaipublic.azureedge.net/clip/models/40d365715913c9da98579312b702a82c18be219cc2a73407c4526f58eba950af/ViT-B-32.pt), [CompVis VQGAN ImageNet f16/16384](https://heibox.uni-heidelberg.de/d/a7530b09fed84f80a887/files/?p=%2Fckpts%2Flast.ckpt&dl=1), and [Hugging Face BigGAN-deep-256](https://s3.amazonaws.com/models.huggingface.co/biggan/biggan-deep-256-pytorch_model.bin).

An administrator can opt in to the browser’s single Bundle B installer with `UNCANNY_ENABLE_CHECKPOINT_DOWNLOADS=true`. The UI shows the fixed upstream files, hashes, bundled pinned conversion sources, and policy before it starts. It downloads and converts locally into the mounted data directory, never into the image. Uncanny Lab is MIT, but that does not cover checkpoints. You remain responsible for permissions. VQGAN and BigGAN checkpoint-specific terms are uncertain, and generated images may involve other rights.

The original files are not runtime-ready. Convert them with `tools/convert_bundle_b.py` and its documented pinned conversion sources. The resulting layout under the data directory must be:

```text
models/bundle-b/classifiers/vgg19.pt
models/bundle-b/clip/vit-b-32.pt
models/bundle-b/vqgan/decoder.pt
models/bundle-b/vqgan/codebook.pt
models/bundle-b/biggan/generator.pt
```

The converter validates the expected VGG19 and CLIP state dictionaries and creates the portable VQGAN decoder, VQGAN codebook, and BigGAN generator formats that the app expects. Do not substitute arbitrary checkpoints. You are responsible for verifying each checkpoint's license and terms before downloading, converting, or using it.

### DALL-E Mini compatibility

DALL-E Mini was assessed against the current image and is not supported by this build. The upstream project pins a legacy JAX/Flax runtime and uses separate Flax model artifacts, while Uncanny Lab currently ships a pinned PyTorch 2.11 Intel XPU/CPU worker. The current image contains no JAX, Flax, or DALL-E Mini packages. Installing the existing Bundle B checkpoints or changing configuration cannot make DALL-E Mini runnable. Valid job requests for `dalle-mini` return an explicit compatibility diagnostic instead of creating a job. Future support would require a separately pinned worker environment, local model verification, and independent CPU/XPU validation.

### Bundle B conversion

The converter requires clean checkouts of [`CompVis/taming-transformers`](https://github.com/CompVis/taming-transformers) at `3ba01b241669f5ade541ce990f7650a3b8f65318` and [`huggingface/pytorch-pretrained-BigGAN`](https://github.com/huggingface/pytorch-pretrained-BigGAN) at `1e18aed2dff75db51428f13b940c38b923eb4a3d`. Download the source files with these exact names: `data/models/classifiers/vgg19.pt`, `data/model-sources/ViT-B-32.pt`, `data/model-sources/vqgan-imagenet-f16-16384.ckpt`, `data/model-sources/vqgan-imagenet-f16-16384.yaml` from `https://heibox.uni-heidelberg.de/d/a7530b09fed84f80a887/files/?p=%2Fconfigs%2Fmodel.yaml&dl=1`, `data/model-sources/biggan-deep-256.bin`, and `data/model-sources/biggan-deep-256-config.json` from `https://s3.amazonaws.com/models.huggingface.co/biggan/biggan-deep-256-config.json`.

```bash
mkdir -p data/model-sources data/models/classifiers
curl -L https://download.pytorch.org/models/vgg19-dcbb9e9d.pth -o data/models/classifiers/vgg19.pt
curl -L https://openaipublic.azureedge.net/clip/models/40d365715913c9da98579312b702a82c18be219cc2a73407c4526f58eba950af/ViT-B-32.pt -o data/model-sources/ViT-B-32.pt
curl -L 'https://heibox.uni-heidelberg.de/d/a7530b09fed84f80a887/files/?p=%2Fckpts%2Flast.ckpt&dl=1' -o data/model-sources/vqgan-imagenet-f16-16384.ckpt
curl -L 'https://heibox.uni-heidelberg.de/d/a7530b09fed84f80a887/files/?p=%2Fconfigs%2Fmodel.yaml&dl=1' -o data/model-sources/vqgan-imagenet-f16-16384.yaml
curl -L https://s3.amazonaws.com/models.huggingface.co/biggan/biggan-deep-256-pytorch_model.bin -o data/model-sources/biggan-deep-256.bin
curl -L https://s3.amazonaws.com/models.huggingface.co/biggan/biggan-deep-256-config.json -o data/model-sources/biggan-deep-256-config.json
git clone https://github.com/CompVis/taming-transformers.git taming-transformers
git -C taming-transformers checkout 3ba01b241669f5ade541ce990f7650a3b8f65318
git clone https://github.com/huggingface/pytorch-pretrained-BigGAN.git pytorch-pretrained-BigGAN
git -C pytorch-pretrained-BigGAN checkout 1e18aed2dff75db51428f13b940c38b923eb4a3d
docker run --rm --user "$(id -u):$(id -g)" --entrypoint python3 \
  -e PYTHONPATH=/workspace/python -v "$PWD:/workspace:ro" -v "$PWD/data:/data" \
  -v "$PWD/taming-transformers:/sources/taming:ro" -v "$PWD/pytorch-pretrained-BigGAN:/sources/biggan:ro" \
  -w /workspace ghcr.io/miloszkolber/uncanny-lab:0.1.2 tools/convert_bundle_b.py \
  --sources /data/model-sources --models /data/models --vgg-source /data/models/classifiers/vgg19.pt \
  --taming-source /sources/taming --biggan-source /sources/biggan
```

## Quick start

The current image release is `0.1.2`. Image builds and publication are manual GitHub Actions dispatches. Patch releases use the `0.1.x` series until a feature release is justified. Existing images can be retagged without rebuilding only when the source image already carries the target version metadata and the target tag does not exist.

For Intel XPU, create a persistent data directory, provide access to the render device, and start Compose:

```bash
export UNCANNY_UID="$(id -u)"
export UNCANNY_GID="$(id -g)"
mkdir -p data
export RENDER_GID="$(stat -c %g /dev/dri/renderD128)"
docker compose up -d
```

For CPU-only development or checkpoint-free smoke tests:

```bash
export UNCANNY_UID="$(id -u)"
export UNCANNY_GID="$(id -g)"
mkdir -p data
docker compose -f compose.yaml -f compose.cpu.yaml up -d
```

Open `http://localhost:8080`. The browser UI communicates with its own local backend and does not require a separate API setup.

## Minimal configuration

Compose defaults to `ghcr.io/miloszkolber/uncanny-lab:0.1.2`, stores persistent state in `./data`, and binds HTTP to `127.0.0.1:8080`. Set only the variables you need in the shell or a local `.env` file:

```text
UNCANNY_IMAGE=ghcr.io/miloszkolber/uncanny-lab:0.1.2
UNCANNY_DATA_DIR=./data
UNCANNY_UID=1000
UNCANNY_GID=1000
UNCANNY_PORT=8080
UNCANNY_DEVICE=xpu
UNCANNY_ENABLE_CHECKPOINT_DOWNLOADS=false
```

For XPU hosts, `UNCANNY_RENDER_DEVICE` defaults to `/dev/dri/renderD128` and `RENDER_GID` must be the group allowed to use that device. Use the CPU Compose override instead of mapping a device when no XPU is available. Set `UNCANNY_BIND_ADDRESS` and `UNCANNY_ALLOWED_HOSTS` deliberately when placing the service behind a reverse proxy.

## Build and test

Build a local Linux AMD64 image and use it with either Compose configuration:

```bash
docker build --platform linux/amd64 -t uncanny-lab:local .
UNCANNY_IMAGE=uncanny-lab:local docker compose -f compose.yaml -f compose.cpu.yaml up
```

Useful checks:

```bash
go test -race ./...
go vet ./...
python3 -m compileall -q python tools
PYTHONPATH=python python3 -m unittest discover -s python/tests -v
bun build web/static/app.js --target browser --outfile /tmp/uncanny-lab-app.js
docker compose -f compose.yaml config --quiet
docker compose -f compose.yaml -f compose.cpu.yaml config --quiet
tools/verify_image.sh uncanny-lab:local
```

## Data and security

`/data` holds the SQLite index, checkpoints, uploads, job specifications, logs, previews, final images, and exports. Back it up as one unit. Checkpoint downloads are disabled by default and can be enabled only for the fixed catalog through the explicit installer setting. Docker networking is not an egress boundary. Keep checkpoints and generated images private as appropriate, review model terms, and expose the default loopback-only service only through a deliberately configured proxy or tunnel.
