#!/bin/bash
# Focused scan; use the unrestricted Full path for the 100% reference.
set -euo pipefail
cd "$(dirname "$0")/../.."
export SHARDS=24 NODE_NUM=4 REPEATS=1 TX_SOURCE=csv_source
export TX_NUMBER="${TX_NUMBER:-300000}" TX_SPEED="${TX_SPEED:-14400}"
export BLOCK_LIMIT=2000 BATCH_SIZE=0 WINDOW_MS=2000 CREDIT_CHUNK_SIZE=512 BEACON_INTERVAL_MS=1000
export TIMEOUT="${TIMEOUT:-1800}"
export METHODS="hot_destination_75 hot_destination_85 hot_destination_95 full_aggregation"
export OUTPUT_ROOT="${OUTPUT_ROOT:-exp/aggregation-hot-destination-fine-v1}"
bash experiments/aggregation_scaling/run.sh
python3 experiments/aggregation_scaling/diagnose_coordination.py --input "$OUTPUT_ROOT"
python3 experiments/aggregation_hot_destination/summarize_fine_sweep.py --input "$OUTPUT_ROOT"
