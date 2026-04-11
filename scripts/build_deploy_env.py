#!/usr/bin/env python3
from pathlib import Path
import sys


def parse_env(path: Path) -> dict[str, str]:
    env: dict[str, str] = {}
    for raw in path.read_text().splitlines():
        line = raw.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        env[key.strip()] = value.strip()
    return env


def main() -> int:
    if len(sys.argv) != 3:
        print("usage: build_deploy_env.py <src_env> <dst_env>")
        return 2

    src = Path(sys.argv[1])
    dst = Path(sys.argv[2])

    env = parse_env(src)

    # Production-safe overrides.
    env["ENABLE_TLS"] = "false"
    env["IMAGE_SERVICE_HOST"] = "image-service"

    # Dev-only keys should not be shipped.
    for key in ("TLS_CERT", "TLS_KEY", "EMBED_MODEL_PATH_DEV", "LAN_IP"):
        env.pop(key, None)

    lines = [f"{key}={value}" for key, value in env.items()]
    dst.write_text("\n".join(lines) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
