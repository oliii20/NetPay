#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/../.."
export REPEATS=1
export BLOCK_LIMIT="${BLOCK_LIMIT:-2000}" BATCH_SIZE=0 WINDOW_MS="${WINDOW_MS:-2000}" CREDIT_CHUNK_SIZE="${CREDIT_CHUNK_SIZE:-512}" BEACON_INTERVAL_MS="${BEACON_INTERVAL_MS:-1000}"
export TX_NUMBER="${TX_NUMBER:-300000}" TIMEOUT="${TIMEOUT:-1800}"
export TX_SOURCE=csv_source
export METHODS="relay full_aggregation"
for percent in ${HOT_PERCENTAGES:-25 50 75}; do
  [[ "$percent" =~ ^[1-9][0-9]*$ ]] && ((percent<=100)) || { echo 'Percent must be integer 1..100' >&2; exit 1; }
  METHODS="${METHODS} hot_destination_${percent}"
done
base="${OUTPUT_BASE:-exp/aggregation-hot-destination}"
for shards in ${SHARDS:-24}; do
  for speed in ${SPEEDS:-10800 14400}; do
    SHARDS="$shards" TX_SPEED="$speed" OUTPUT_ROOT="${base}/shards${shards}-speed${speed}" bash experiments/aggregation_scaling/run.sh
  done
done
