#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

BUNDLED_PY="/Users/ljn/.cache/codex-runtimes/codex-primary-runtime/dependencies/python/bin/python3"
if [[ -x "$BUNDLED_PY" ]]; then
  PYTHON="$BUNDLED_PY"
else
  PYTHON="${PYTHON:-python3}"
fi

"$PYTHON" pre_exp_scipts/plot_exp1_height_matching_rate.py "$@"
