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

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repo_root="$(dirname -- "$script_dir")"
db_path="${DB_PATH:-${repo_root}/data/vinylkeeper.db}"
legacy_db_root="${repo_root}/vinylkeeper.db"
legacy_db_app="${repo_root}/app/vinylkeeper.db"

retries="${IMAGE_SERVICE_WAIT_RETRIES:-60}"
sleep_seconds="${IMAGE_SERVICE_WAIT_SECONDS:-1}"

mkdir -p "$(dirname -- "$db_path")"

if [ ! -f "$db_path" ]; then
  if [ -f "$legacy_db_root" ]; then
    cp "$legacy_db_root" "$db_path"
    if [ -f "${legacy_db_root}-wal" ]; then cp "${legacy_db_root}-wal" "${db_path}-wal"; fi
    if [ -f "${legacy_db_root}-shm" ]; then cp "${legacy_db_root}-shm" "${db_path}-shm"; fi
    echo "Copied legacy DB to canonical DB path: $db_path"
  elif [ -f "$legacy_db_app" ]; then
    cp "$legacy_db_app" "$db_path"
    if [ -f "${legacy_db_app}-wal" ]; then cp "${legacy_db_app}-wal" "${db_path}-wal"; fi
    if [ -f "${legacy_db_app}-shm" ]; then cp "${legacy_db_app}-shm" "${db_path}-shm"; fi
    echo "Copied legacy app DB to canonical DB path: $db_path"
  fi
fi

if [ -f "$legacy_db_root" ] && [ -f "$db_path" ] && [ "$legacy_db_root" != "$db_path" ]; then
  echo "Note: legacy DB exists at $legacy_db_root; canonical DB in use is $db_path"
fi

if [ -f "$legacy_db_app" ] && [ -f "$db_path" ] && [ "$legacy_db_app" != "$db_path" ]; then
  echo "Note: legacy app DB exists at $legacy_db_app; canonical DB in use is $db_path"
fi

export DB_PATH="$db_path"
echo "Using DB_PATH=$DB_PATH"

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
