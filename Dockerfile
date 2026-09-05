# syntax=docker/dockerfile:1.7
ARG VERSION=development
ARG REVISION=unknown
ARG CREATED=unknown

FROM docker.io/library/alpine:3.21@sha256:4bcff63911fcb4448bd4fdacec207030997caf25e9bea4045fa6c8c44de311d1 AS conversion-sources
RUN apk add --no-cache git
WORKDIR /src
RUN set -eux; \
    git clone --no-checkout https://github.com/CompVis/taming-transformers.git taming-transformers; \
    git -C taming-transformers fetch --depth=1 origin 3ba01b241669f5ade541ce990f7650a3b8f65318; \
    git -C taming-transformers checkout --detach 3ba01b241669f5ade541ce990f7650a3b8f65318; \
    test "$(git -C taming-transformers rev-parse HEAD^{tree})" = cb6fd749bbad796fdef2dc7e9ad9f680c8ca462c; \
    rm -rf taming-transformers/.git; \
    printf '{"commit":"3ba01b241669f5ade541ce990f7650a3b8f65318","tree":"cb6fd749bbad796fdef2dc7e9ad9f680c8ca462c"}\n' > taming-transformers/.uncanny-source-pin; \
    git clone --no-checkout https://github.com/huggingface/pytorch-pretrained-BigGAN.git pytorch-pretrained-BigGAN; \
    git -C pytorch-pretrained-BigGAN fetch --depth=1 origin 1e18aed2dff75db51428f13b940c38b923eb4a3d; \
    git -C pytorch-pretrained-BigGAN checkout --detach 1e18aed2dff75db51428f13b940c38b923eb4a3d; \
    test "$(git -C pytorch-pretrained-BigGAN rev-parse HEAD^{tree})" = f9c893ec07560e132e24aad0bf1040394892ced1; \
    rm -rf pytorch-pretrained-BigGAN/.git; \
    printf '{"commit":"1e18aed2dff75db51428f13b940c38b923eb4a3d","tree":"f9c893ec07560e132e24aad0bf1040394892ced1"}\n' > pytorch-pretrained-BigGAN/.uncanny-source-pin; \
    chmod -R a=rX,u+w /src

FROM docker.io/library/golang:1.24-bookworm@sha256:1a6d4452c65dea36aac2e2d606b01b4a029ec90cc1ae53890540ce6173ea77ac AS go-builder

ARG VERSION REVISION

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.revision=${REVISION}" -o /out/uncanny-lab ./cmd/server

# Distroless is not possible for the final image: the Python worker needs the
# Intel XPU PyTorch runtime and userspace compute libraries from the Intel
# base image. The Go server itself is a static binary and follows the cuddler
# pattern where practical: baked mewa_ui, binary -healthcheck, non-root USER.
# Intel's XPU image includes PyTorch and the userspace compute runtime required by Arc B-series GPUs.
FROM docker.io/intel/pytorch:xpu-2.11.0-ubuntu24.04@sha256:dda613c2e1ab34d9630626ffac50d530fe5e2ef5576f6fb68de7b2d360b41cd5

ENV HF_HUB_OFFLINE=1 \
    HF_HUB_DISABLE_TELEMETRY=1 \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

ARG VERSION
ARG REVISION
ARG CREATED
LABEL org.opencontainers.image.title="Uncanny Lab" \
       org.opencontainers.image.description="A local playground for early image-generation algorithms and visible optimization processes." \
      org.opencontainers.image.source="https://github.com/miloszkolber/uncanny-lab" \
      org.opencontainers.image.url="https://github.com/miloszkolber/uncanny-lab" \
      org.opencontainers.image.documentation="https://github.com/miloszkolber/uncanny-lab#readme" \
      org.opencontainers.image.authors="Milosz Kolber" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}"

COPY python/requirements-runtime.txt /tmp/requirements-runtime.txt
# Requirements install into this image's python3 interpreter, while the app
# package below is copied into /opt/venv/lib/python3.12/site-packages. The
# runtime config points PYTHONPATH at that directory, so `python3 -m
# uncanny_lab.runner` resolves the app code there and dependencies from the
# interpreter, whichever absolute location the interpreter uses.
RUN python3 -m pip install --no-cache-dir --no-deps --require-hashes -r /tmp/requirements-runtime.txt \
    && rm /tmp/requirements-runtime.txt
COPY python/uncanny_lab /opt/venv/lib/python3.12/site-packages/uncanny_lab

WORKDIR /app
COPY --from=go-builder /out/uncanny-lab /usr/local/bin/uncanny-lab
# mewa_ui is bundled at build time from the mewa_ui additional context
# (same pattern as cuddler) and served from /ui. No vendored copy in git.
COPY --from=mewa_ui library/ /ui/
COPY manifests /app/manifests
COPY tools/convert_bundle_b.py /app/tools/convert_bundle_b.py
COPY tools/sitecustomize.py /app/tools/sitecustomize.py
COPY --from=conversion-sources /src/taming-transformers /app/conversion-sources/taming-transformers
COPY --from=conversion-sources /src/pytorch-pretrained-BigGAN /app/conversion-sources/pytorch-pretrained-BigGAN
COPY config/config.yaml /config/config.yaml
COPY LICENSE THIRD_PARTY_NOTICES /usr/share/licenses/uncanny-lab/

EXPOSE 8080
USER 1000:1000
HEALTHCHECK --interval=30s --timeout=3s --start-period=15s --retries=3 CMD ["/usr/local/bin/uncanny-lab", "-healthcheck"]
ENTRYPOINT ["uncanny-lab"]
CMD ["--config", "/config/config.yaml"]
