#!/bin/sh
set -eu

# 将 CLI 固定在工程内，避免系统中旧版本 wails3 与 Go 依赖不一致。
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"
cli_version="v3.0.0-beta.29"
cli_path="$project_dir/.tools/wails3"
if [ ! -x "$cli_path" ] || [ "$("$cli_path" version 2>&1)" != "$cli_version" ]; then
  printf '正在准备 Wails %s…\n' "$cli_version"
  mkdir -p "$project_dir/.tools"
  GOBIN="$project_dir/.tools" go install "github.com/wailsapp/wails/v3/cmd/wails3@$cli_version"
fi
PATH="$project_dir/.tools:$PATH"
export PATH
exec "$cli_path" "$@"
