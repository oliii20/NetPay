#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/../.."
OUTPUT_ROOT="${OUTPUT_ROOT:-exp/aggregation-scaling}"
SHARDS="${SHARDS:-4 8 16 24}"
export REPEATS="${REPEATS:-3}" TX_NUMBER="${TX_NUMBER:-300000}" TX_SPEED="${TX_SPEED:-20000}" TIMEOUT="${TIMEOUT:-1800}"
export NODE_NUM="${NODE_NUM:-4}"
for shards in ${SHARDS}; do
  run_root="${OUTPUT_ROOT}/shards-${shards}"
  if [[ -e "${run_root}" ]]; then echo "Already exists: ${run_root}; choose a new OUTPUT_ROOT" >&2; exit 1; fi
  mkdir -p "${run_root}"
  python3 - "${run_root}/ip_table.json" "${shards}" "${NODE_NUM}" <<'PY'
import json, sys
path, shards, nodes = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
ids = list(range(shards)) + [2147483646, 2147483647]
port = 32000
table = {}
for shard in ids:
    table[str(shard)] = {}
    for node in range(1 if shard == 2147483647 else nodes):
        table[str(shard)][str(node)] = f'127.0.0.1:{port}'
        port += 1
with open(path, 'w') as out:
    json.dump(table, out, indent=2)
PY
  SHARD_NUM="${shards}" IP_TABLE="${run_root}/ip_table.json" OUTPUT_ROOT="${run_root}" bash experiments/aggregation_ablation/run.sh
done
python3 experiments/aggregation_scaling/analyze.py --input "${OUTPUT_ROOT}" --expected-txs "${TX_NUMBER}"
