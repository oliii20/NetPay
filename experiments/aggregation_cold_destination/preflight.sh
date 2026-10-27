#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/../.."

TX_NUMBER="${TX_NUMBER:-300000}"
BLOCK_LIMIT="${BLOCK_LIMIT:-2000}"
BLOCK_INTERVAL_MS="${BLOCK_INTERVAL_MS:-2000}"
THRESHOLDS="${THRESHOLDS:-10 20 30 40}"
OUTPUT_ROOT="${OUTPUT_ROOT:-exp/aggregation-cold-destination-preflight}"

for shards in ${SHARDS:-24}; do
  ranking="${OUTPUT_ROOT}/shards-${shards}/destination_ranking.json"
  mkdir -p "$(dirname "${ranking}")"
  GOCACHE="${GOCACHE:-/tmp/block-emulator-x-comparison-cache}" \
    go run ./cmd/hotdestinations -shards="${shards}" -tx-number="${TX_NUMBER}" -output="${ranking}"
  for speed in ${SPEEDS:-10800 14400}; do
    output="${OUTPUT_ROOT}/shards-${shards}/speed-${speed}"
    python3 experiments/aggregation_cold_destination/analyze.py \
      --ranking "${ranking}" --thresholds ${THRESHOLDS} \
      --injection-speed "${speed}" --block-limit "${BLOCK_LIMIT}" \
      --block-interval-ms "${BLOCK_INTERVAL_MS}" --output "${output}"
  done
done
