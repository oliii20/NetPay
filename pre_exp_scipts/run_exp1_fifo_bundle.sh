#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PYTHON="${PYTHON:-python3}"
RAW_CSV="${RAW_CSV:-$ROOT_DIR/Raw_data/25250000区块高度-25255233区块高度_500K.csv}"
OUTPUT_DIR="${OUTPUT_DIR:-$ROOT_DIR/画图_500K_inject_1w}"
MAX_VALID_TXS="${MAX_VALID_TXS:-500000}"
INJECT_SIZE="${INJECT_SIZE:-10000}"
BLOCK_SIZE="${BLOCK_SIZE:-2000}"
SHARDS="${SHARDS:-8}"

"$PYTHON" "$ROOT_DIR/生成画图数据/generate_exp1_fifo_data.py" \
  --input "$RAW_CSV" \
  --output-dir "$OUTPUT_DIR" \
  --shards "$SHARDS" \
  --inject-size "$INJECT_SIZE" \
  --block-size "$BLOCK_SIZE" \
  --max-valid-txs "$MAX_VALID_TXS"

"$PYTHON" "$ROOT_DIR/生成画图数据/plot_exp1_height_matching_rate.py" \
  --input "$OUTPUT_DIR/height_matching_rate.csv" \
  --output "$OUTPUT_DIR/global_height_matching_rate.png"

"$PYTHON" "$ROOT_DIR/生成画图数据/plot_directional_ctx_heatmap.py" \
  --input "$OUTPUT_DIR/directional_pair_ctx_counts.csv" \
  --output "$OUTPUT_DIR/directional_ctx_count_heatmap.png"

"$PYTHON" "$ROOT_DIR/生成画图数据/plot_bidirectional_netting_potential_heatmap.py" \
  --input "$OUTPUT_DIR/bidirectional_pair_ctx_balance.csv" \
  --output "$OUTPUT_DIR/bidirectional_netting_potential_heatmap.png" \
  --csv-output "$OUTPUT_DIR/bidirectional_netting_potential.csv"
