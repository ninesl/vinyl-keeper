#!/usr/bin/env sh
set -eu

# Native local runner (non-container fallback):
# 1) starts Python image-service
# 2) runs templ/tailwind watchers
# 3) runs `air` for the app module

host="${IMAGE_SERVICE_HOST:-127.0.0.1}"
port="${IMAGE_SERVICE_PORT:-8081}"
script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repo_root="$(dirname -- "$script_dir")"
db_path="${DB_PATH:-${repo_root}/data/vinylkeeper.db}"
legacy_db_root="${repo_root}/vinylkeeper.db"
legacy_db_app="${repo_root}/app/vinylkeeper.db"

embed_model_path="${EMBED_MODEL_PATH:-models/ViT-B-32__openai/visual/model.onnx}"
case "$embed_model_path" in
  /*) ;;
  *) embed_model_path="${repo_root}/${embed_model_path}" ;;
esac

tls_cert="${TLS_CERT:-certs/dev.crt}"
case "$tls_cert" in
  /*) ;;
  *) tls_cert="${repo_root}/${tls_cert}" ;;
esac

tls_key="${TLS_KEY:-certs/dev.key}"
case "$tls_key" in
  /*) ;;
  *) tls_key="${repo_root}/${tls_key}" ;;
esac

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
export EMBED_MODEL_PATH="$embed_model_path"
export TLS_CERT="$tls_cert"
export TLS_KEY="$tls_key"
echo "Using DB_PATH=$DB_PATH"
echo "Using EMBED_MODEL_PATH=$EMBED_MODEL_PATH"

cleanup() {
  if [ -n "${uvicorn_pid:-}" ]; then
    kill "$uvicorn_pid" 2>/dev/null || true
  fi
  if [ -n "${templ_pid:-}" ]; then
    kill "$templ_pid" 2>/dev/null || true
  fi
  if [ -n "${tailwind_pid:-}" ]; then
    kill "$tailwind_pid" 2>/dev/null || true
  fi
}
trap cleanup INT TERM EXIT

(cd image_service && uv run uvicorn image_service:app --host "$host" --port "$port") &
uvicorn_pid=$!

(cd "$repo_root" && make --no-print-directory templ-watch) &
templ_pid=$!

(cd "$repo_root" && make --no-print-directory tailwind-watch) &
tailwind_pid=$!

cd app && air
