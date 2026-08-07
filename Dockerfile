ARG VERSION=development

FROM docker.io/library/golang:1.24-bookworm@sha256:1a6d4452c65dea36aac2e2d606b01b4a029ec90cc1ae53890540ce6173ea77ac AS go-builder

ARG VERSION

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/uncanny-lab ./cmd/server

# Intel's XPU image includes PyTorch and the userspace compute runtime required by Arc B-series GPUs.
FROM docker.io/intel/pytorch:xpu-2.11.0-ubuntu24.04@sha256:dda613c2e1ab34d9630626ffac50d530fe5e2ef5576f6fb68de7b2d360b41cd5

ARG VERSION
LABEL org.opencontainers.image.title="Uncanny Lab" \
      org.opencontainers.image.source="https://github.com/miloszkolber/uncanny-lab" \
      org.opencontainers.image.authors="Milosz Kolber" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"

COPY python/requirements-runtime.txt /tmp/requirements-runtime.txt
RUN python3 -m pip install --no-cache-dir --no-deps --require-hashes -r /tmp/requirements-runtime.txt \
    && rm /tmp/requirements-runtime.txt
COPY python/uncanny_lab /opt/venv/lib/python3.12/site-packages/uncanny_lab

WORKDIR /app
COPY --from=go-builder /out/uncanny-lab /usr/local/bin/uncanny-lab
COPY manifests /app/manifests
COPY config/config.yaml /config/config.yaml
COPY LICENSE THIRD_PARTY_NOTICES /usr/share/licenses/uncanny-lab/

EXPOSE 8080
USER 1000:1000
ENTRYPOINT ["uncanny-lab"]
CMD ["--config", "/config/config.yaml"]
