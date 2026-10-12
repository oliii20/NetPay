#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

export GOCACHE="${GOCACHE:-/private/tmp/block-emulator-x-go-build-cache}"

go run ./pre_exp_scipts/exp1_real_workload_netting.go "$@"
