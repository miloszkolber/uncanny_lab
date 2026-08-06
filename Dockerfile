FROM docker.io/library/golang:1.24-bookworm AS go-builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=0.1.0" -o /out/legacy-image-lab ./cmd/server

# Intel's XPU image includes PyTorch and the userspace compute runtime required by Arc B-series GPUs.
FROM docker.io/intel/pytorch:xpu-2.11.0-ubuntu24.04@sha256:dda613c2e1ab34d9630626ffac50d530fe5e2ef5576f6fb68de7b2d360b41cd5

ARG DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=go-builder /out/legacy-image-lab /usr/local/bin/legacy-image-lab
COPY python /app/python
COPY manifests /app/manifests
COPY config/config.yaml /config/config.yaml

EXPOSE 8080
ENTRYPOINT ["legacy-image-lab"]
CMD ["--config", "/config/config.yaml"]
