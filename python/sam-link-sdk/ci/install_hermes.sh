#!/usr/bin/env bash
# Copyright Solace Corporation
#
# Use of this software is governed by the Apache License, Version 2.0
# (Apache-2.0) included in the LICENSE-APACHE file.

set -euo pipefail

ref="${1:?usage: install_hermes.sh <commit or branch> <directory>}"
dir="$(mkdir -p "${2:?usage: install_hermes.sh <commit or branch> <directory>}" && cd "$2" && pwd)"

mkdir -p "$dir/hermes-agent"
cd "$dir/hermes-agent"
git init -q
git fetch -q --depth 1 https://github.com/NousResearch/hermes-agent.git "$ref"
git checkout -q --detach FETCH_HEAD
echo "Hermes commit: $(git rev-parse HEAD)"

uv export --locked --no-dev --no-emit-project --format requirements-txt > "$dir/requirements.txt"
uv venv -q --python 3.14 "$dir/venv"
uv pip install -q --python "$dir/venv/bin/python" --require-hashes -r "$dir/requirements.txt"
uv pip install -q --python "$dir/venv/bin/python" --no-deps -e .
"$dir/venv/bin/python" -I -c "import run_agent"
