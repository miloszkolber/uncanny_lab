#!/usr/bin/env bash
set -euo pipefail

image=${1:?usage: tools/verify_image.sh IMAGE}
name="uncanny-lab-verify-$$"
data_dir=$(mktemp -d)

cleanup() {
    docker rm -f "$name" >/dev/null 2>&1 || true
    rm -rf "$data_dir"
}
trap cleanup EXIT

test "$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image")" = linux/amd64
test "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.title" }}' "$image")" = "Uncanny Lab"
test "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.source" }}' "$image")" = "https://github.com/miloszkolber/uncanny-lab"
test "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.authors" }}' "$image")" = "Milosz Kolber"
test "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.licenses" }}' "$image")" = MIT
test -n "$(docker image inspect --format '{{ index .Config.Labels "org.opencontainers.image.version" }}' "$image")"

docker run --rm --entrypoint sh "$image" -ec '
    test -f /usr/share/licenses/uncanny-lab/LICENSE
    test -f /usr/share/licenses/uncanny-lab/THIRD_PARTY_NOTICES
    test ! -e /tools
    test ! -e /app/python/tests
    test ! -e /.git
    test ! -e /app/.git
    test -z "$(find /app /usr/local/bin -type f \( -name "*.pt" -o -name "*.pth" -o -name "*.ckpt" -o -name "*.safetensors" -o -name "*.onnx" \) -print -quit)"
'

# This intentionally has no PYTHONPATH, proving that the packaged worker is installed.
docker run --rm --entrypoint python3 "$image" -m uncanny_lab.runner --self-test --device cpu

chmod 777 "$data_dir"
docker run --rm --user root --entrypoint sh -v "$data_dir:/data" "$image" -ec 'chown 1000:1000 /data'
docker run -d --name "$name" -e UNCANNY_DEVICE=cpu -v "$data_dir:/data" "$image" >/dev/null
for _ in {1..30}; do
    if docker exec "$name" python3 -c 'import urllib.request; urllib.request.urlopen("http://127.0.0.1:8080/healthz", timeout=1).read()' >/dev/null 2>&1; then
        exit 0
    fi
    sleep 1
done
docker logs "$name" >&2
exit 1
