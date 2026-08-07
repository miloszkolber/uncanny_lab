#!/usr/bin/env bash
set -euo pipefail

image=${1:?usage: tools/verify_image.sh IMAGE [EXPECTED_REVISION] [EXPECTED_VERSION]}
expected_revision=${2:-}
expected_version=${3:-}
name="uncanny-lab-verify-$$"
data_dir=$(mktemp -d)

cleanup() {
    docker rm -f "$name" >/dev/null 2>&1 || true
    docker run --rm --user root --entrypoint chmod -v "$data_dir:/data" "$image" -R a+rwx /data >/dev/null 2>&1 || true
    rm -rf "$data_dir" 2>/dev/null || true
}
trap cleanup EXIT

test "$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image")" = linux/amd64
test "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.title" }}' "$image")" = "Uncanny Lab"
test "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.source" }}' "$image")" = "https://github.com/miloszkolber/uncanny-lab"
test "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.url" }}' "$image")" = "https://github.com/miloszkolber/uncanny-lab"
test "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.documentation" }}' "$image")" = "https://github.com/miloszkolber/uncanny-lab#readme"
test "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.description" }}' "$image")" = "A local playground for early image-generation algorithms and visible optimization processes."
test "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.authors" }}' "$image")" = "Milosz Kolber"
test "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.licenses" }}' "$image")" = MIT
version=$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.version" }}' "$image")
test -n "$version"
if [[ -n "$expected_version" ]]; then
    test "$version" = "$expected_version"
fi
revision=$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.revision" }}' "$image")
test -n "$revision"
test "$revision" != ac12083301cbdbe06135882d401f7877b0b512db
if [[ -n "$expected_revision" ]]; then
    test "$revision" = "$expected_revision"
fi

docker run --rm --entrypoint sh "$image" -ec '
    test -f /usr/share/licenses/uncanny-lab/LICENSE
    test -f /usr/share/licenses/uncanny-lab/THIRD_PARTY_NOTICES
    test -f /app/tools/convert_bundle_b.py
    test -f /app/tools/sitecustomize.py
    test -d /app/conversion-sources/taming-transformers
    test -f /app/conversion-sources/taming-transformers/.uncanny-source-pin
    test -d /app/conversion-sources/pytorch-pretrained-BigGAN
    test -f /app/conversion-sources/pytorch-pretrained-BigGAN/.uncanny-source-pin
    test ! -e /app/conversion-sources/taming-transformers/.git
    test ! -e /app/conversion-sources/pytorch-pretrained-BigGAN/.git
    test ! -e /app/python/tests
    test ! -e /.git
    test ! -e /app/.git
    test -z "$(find /app /usr/local/bin -type f \( -name "*.pt" -o -name "*.pth" -o -name "*.ckpt" -o -name "*.safetensors" -o -name "*.onnx" -o -name "*.bin" \) -print -quit)"
'

# This intentionally has no PYTHONPATH, proving that the packaged worker is installed.
docker run --rm --entrypoint python3 "$image" -m uncanny_lab.runner --self-test --device cpu
docker run --rm --entrypoint python3 -e PYTHONPATH=/app/tools "$image" -c 'import socket; s=socket.socket();
try:
    s.connect(("127.0.0.1", 9))
except OSError as exc:
    assert str(exc) == "network access is disabled during model conversion"
else:
    raise AssertionError("socket connect was not blocked")
finally:
    s.close()'

chmod 777 "$data_dir"
docker run --rm --user root --entrypoint chown -v "$data_dir:/data" "$image" -R 1000:1000 /data >/dev/null
docker run -d --name "$name" -e UNCANNY_DEVICE=cpu -v "$data_dir:/data" "$image" >/dev/null
ready=false
for _ in {1..30}; do
    if docker exec "$name" python3 -c 'import urllib.request; urllib.request.urlopen("http://127.0.0.1:8080/healthz", timeout=1).read()' >/dev/null 2>&1; then
        ready=true
        break
    fi
    sleep 1
done
if [[ "$ready" != true ]]; then
    docker logs "$name" >&2
    exit 1
fi

docker exec -e IMAGE_VERSION="$version" "$name" python3 -c '
import json
import os
import urllib.request

for path, expected in (("/styles.css", b"/* Core UI foundation"), ("/lucide.svg", b"symbol id=\"sparkles\"")):
    with urllib.request.urlopen("http://127.0.0.1:8080" + path, timeout=3) as response:
        assert response.read().find(expected) >= 0, path
with urllib.request.urlopen("http://127.0.0.1:8080/", timeout=3) as response:
    assert b"/ui/" not in response.read()
with urllib.request.urlopen("http://127.0.0.1:8080/api/system", timeout=3) as response:
    assert json.load(response)["application_version"] == os.environ["IMAGE_VERSION"]
'
