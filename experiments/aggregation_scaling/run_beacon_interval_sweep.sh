#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/../.."
OUTPUT_BASE="${OUTPUT_BASE:-exp/aggregation-beacon-interval}"
SPEEDS="${SPEEDS:-10800 14400}"
INTERVALS="${INTERVALS:-2000 1000 500}"
export INCLUDE_AGGREGATION_ONLY="${INCLUDE_AGGREGATION_ONLY:-0}"
export REPEATS="${REPEATS:-1}" SHARDS="${SHARDS:-24}"
export TX_NUMBER="${TX_NUMBER:-300000}" TIMEOUT="${TIMEOUT:-1800}"
export BLOCK_LIMIT="${BLOCK_LIMIT:-2000}" BATCH_SIZE="${BATCH_SIZE:-0}"
export WINDOW_MS="${WINDOW_MS:-2000}" CREDIT_CHUNK_SIZE="${CREDIT_CHUNK_SIZE:-512}"
for speed in ${SPEEDS}; do
  for interval in ${INTERVALS}; do
    run_root="${OUTPUT_BASE}/speed${speed}-beacon${interval}"
    echo "Injection ${speed} TPS; Beacon interval ${interval} ms; shards ${SHARDS}"
    BEACON_INTERVAL_MS="${interval}" TX_SPEED="${speed}" OUTPUT_ROOT="${run_root}" \
      bash experiments/aggregation_scaling/run.sh
    python3 experiments/aggregation_scaling/diagnose_coordination.py --input "${run_root}"
  done
done
