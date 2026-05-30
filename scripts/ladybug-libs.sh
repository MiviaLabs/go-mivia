#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd -- "${script_dir}/.." && pwd)"
target_dir="${LBUG_TARGET_DIR:-${repo_root}/lib-ladybug}"

mkdir -p "${target_dir}"

curl -fsSL https://raw.githubusercontent.com/LadybugDB/ladybug/refs/heads/main/scripts/download-liblbug.sh | LBUG_TARGET_DIR="${target_dir}" bash

if [[ "$(uname)" == "Darwin" ]]; then
  ln -sf liblbug.dylib "${target_dir}/liblbug.0.dylib"
else
  ln -sf liblbug.so "${target_dir}/liblbug.so.0"
fi

cat <<MSG
LadybugDB native libraries installed in ${target_dir}.

For native LadybugDB builds:
  export CGO_LDFLAGS="-L${target_dir} -llbug -Wl,-rpath,${target_dir}"
  go test -tags 'ladybug_native system_ladybug' ./...
MSG
