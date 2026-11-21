#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

case "$(uname -m)" in
  aarch64|arm64) arch=arm64 ;;
  x86_64) arch=amd64 ;;
  *) echo 'Unsupported Linux architecture' >&2; exit 1 ;;
esac

output="$PWD/state/cpa-plugins/linux/$arch"
mkdir -p "$output"
sudo -n docker run --rm --user "$(id -u):$(id -g)" \
  -v "$PWD/plugins/cpamc:/src:ro" -v "$output:/out" \
  golang:1.26-bookworm@sha256:dc40a13b61304647ed7e5d581d246b86413dfc29d63c6d018310feb93cd98fc0 \
  sh -eu -c 'cc -shared -O2 -Wall -Wextra -Werror -fPIC -fvisibility=hidden -o /out/.cpa-ext.so /src/plugin.c
             mv /out/.cpa-ext.so /out/cpa-ext.so'
