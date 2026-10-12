#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PYTHON="${PYTHON:-python3}"
RAW_CSV="${RAW_CSV:-$ROOT_DIR/Raw_data/25250000区块高度-25255233区块高度_500K.csv}"
OUTPUT_DIR="${OUTPUT_DIR:-$ROOT_DIR/画图_sensitivity}"
MAX_VALID_TXS="${MAX_VALID_TXS:-500000}"
BLOCK_SIZE="${BLOCK_SIZE:-2000}"
BASE_INJECT_SIZE="${BASE_INJECT_SIZE:-10000}"
BASE_SHARDS="${BASE_SHARDS:-8}"
SHARD_COUNTS="${SHARD_COUNTS:-4,8,16,32}"
SHARD_INJECT_SIZES="${SHARD_INJECT_SIZES:-5000,10000,20000,40000}"
INJECT_SIZES="${INJECT_SIZES:-8000,10000,12000,14000}"
METRIC="${METRIC:-global_matching_rate}"

"$PYTHON" "$ROOT_DIR/生成画图数据/generate_exp2_sensitivity_data.py" \
  --input "$RAW_CSV" \
  --output-dir "$OUTPUT_DIR" \
  --generator "$ROOT_DIR/生成画图数据/generate_exp1_fifo_data.py" \
  --max-valid-txs "$MAX_VALID_TXS" \
  --block-size "$BLOCK_SIZE" \
  --base-inject-size "$BASE_INJECT_SIZE" \
  --base-shards "$BASE_SHARDS" \
  --shard-counts "$SHARD_COUNTS" \
  --shard-inject-sizes "$SHARD_INJECT_SIZES" \
  --inject-sizes "$INJECT_SIZES"

"$PYTHON" "$ROOT_DIR/生成画图数据/plot_exp2_sensitivity_bars.py" \
  --shard-input "$OUTPUT_DIR/shard_count_sweep.csv" \
  --inject-input "$OUTPUT_DIR/inject_size_sweep.csv" \
  --output-dir "$OUTPUT_DIR" \
  --metric "$METRIC"
