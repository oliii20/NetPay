#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/../.."

export REPEATS="${REPEATS:-1}" TX_SOURCE=csv_source
export BLOCK_LIMIT="${BLOCK_LIMIT:-2000}" BLOCK_INTERVAL_MS="${BLOCK_INTERVAL_MS:-2000}"
export BATCH_SIZE=0 WINDOW_MS="${WINDOW_MS:-2000}"
export CREDIT_CHUNK_SIZE="${CREDIT_CHUNK_SIZE:-512}" BEACON_INTERVAL_MS="${BEACON_INTERVAL_MS:-1000}"
export TX_NUMBER="${TX_NUMBER:-300000}" TIMEOUT="${TIMEOUT:-1800}"
export METHODS="relay full_aggregation"
for threshold in ${THRESHOLDS:-10 20 30 40}; do
  [[ "${threshold}" =~ ^[1-9][0-9]*$ ]] && ((threshold<=100)) || {
    echo 'Threshold must be an integer from 1 to 100' >&2
    exit 1
  }
  METHODS="${METHODS} cold_exclusion_${threshold}"
done

base="${OUTPUT_BASE:-exp/aggregation-cold-destination}"
for shards in ${SHARDS:-24}; do
  for speed in ${SPEEDS:-10800 14400}; do
    SHARDS="${shards}" TX_SPEED="${speed}" \
      OUTPUT_ROOT="${base}/shards${shards}-speed${speed}" \
      bash experiments/aggregation_scaling/run.sh
    python3 experiments/aggregation_cold_destination/summarize.py \
      --input "${base}/shards${shards}-speed${speed}"
  done
done
