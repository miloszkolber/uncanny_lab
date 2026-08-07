FROM docker.io/library/golang:1.24-bookworm AS go-builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=0.2.0" -o /out/uncanny-lab ./cmd/server

# Intel's XPU image includes PyTorch and the userspace compute runtime required by Arc B-series GPUs.
FROM docker.io/intel/pytorch:xpu-2.11.0-ubuntu24.04@sha256:dda613c2e1ab34d9630626ffac50d530fe5e2ef5576f6fb68de7b2d360b41cd5

ARG DEBIAN_FRONTEND=noninteractive
COPY python/requirements-runtime.txt /tmp/requirements-runtime.txt
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* \
    && python3 -m pip install --no-cache-dir -r /tmp/requirements-runtime.txt \
    && rm /tmp/requirements-runtime.txt

WORKDIR /app
COPY --from=go-builder /out/uncanny-lab /usr/local/bin/uncanny-lab
COPY python /app/python
COPY manifests /app/manifests
COPY config/config.yaml /config/config.yaml

EXPOSE 8080
USER 1000:1000
ENTRYPOINT ["uncanny-lab"]
CMD ["--config", "/config/config.yaml"]
