#!/usr/bin/env sh
set -eu

# Native local runner (non-container fallback):
# 1) starts Python image-service
# 2) waits for /health
# 3) runs `go run` for the app module

host="${IMAGE_SERVICE_HOST:-127.0.0.1}"
port="${IMAGE_SERVICE_PORT:-8081}"
health_endpoint="${IMAGE_SERVICE_HEALTH_ENDPOINT:-/health}"
health_url="${IMAGE_SERVICE_HEALTH_URL:-http://${host}:${port}${health_endpoint}}"

retries="${IMAGE_SERVICE_WAIT_RETRIES:-60}"
sleep_seconds="${IMAGE_SERVICE_WAIT_SECONDS:-1}"

cleanup() {
  if [ -n "${uvicorn_pid:-}" ]; then
    kill "$uvicorn_pid" 2>/dev/null || true
  fi
}
trap cleanup INT TERM EXIT

(cd image_service && uv run uvicorn image_service:app --host "$host" --port "$port") &
uvicorn_pid=$!

i=0
while [ "$i" -lt "$retries" ]; do
  if curl -fsS "$health_url" >/dev/null 2>&1; then
    break
  fi
  i=$((i + 1))
  sleep "$sleep_seconds"
done

if ! curl -fsS "$health_url" >/dev/null 2>&1; then
  echo "image service not healthy: $health_url" >&2
  exit 1
fi

go -C app run .
