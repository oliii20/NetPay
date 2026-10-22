#!/bin/bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT_DIR}"

SHARD_NUM="${SHARD_NUM:-4}"
NODE_NUM="${NODE_NUM:-4}"
BEACON_SHARD_ID=2147483646
SUPERVISOR_SHARD_ID=2147483647
REPEATS="${REPEATS:-1}"
TX_NUMBER="${TX_NUMBER:-300000}"
TX_SPEED="${TX_SPEED:-20000}"
TX_SOURCE="${TX_SOURCE:-csv_source}"
BLOCK_LIMIT="${BLOCK_LIMIT:-2000}"
BATCH_SIZE="${BATCH_SIZE:-0}"
TIMEOUT="${TIMEOUT:-900}"
OUTPUT_ROOT="${OUTPUT_ROOT:-exp/aggregation-ablation}"
IP_TABLE="${IP_TABLE:-ip_table_aggregation.json}"
BIN_DIR="${OUTPUT_ROOT}/bin"
PROCESS_IDS=()

cleanup_processes() {
  for process_id in "${PROCESS_IDS[@]:-}"; do
    kill "${process_id}" 2>/dev/null || true
  done
  PROCESS_IDS=()
}
trap cleanup_processes EXIT INT TERM

# Fail before starting any nodes if local socket access is unavailable.
python3 - <<'PYCODE'
import socket
with socket.socket() as probe:
    probe.bind(('127.0.0.1', 0))
PYCODE
mkdir -p "${BIN_DIR}"
GOCACHE="${GOCACHE:-/tmp/block-emulator-x-comparison-cache}" go build -o "${BIN_DIR}/consensusnode" ./cmd/consensusnode
GOCACHE="${GOCACHE:-/tmp/block-emulator-x-comparison-cache}" go build -o "${BIN_DIR}/beaconnode" ./cmd/beaconnode
GOCACHE="${GOCACHE:-/tmp/block-emulator-x-comparison-cache}" go build -o "${BIN_DIR}/supervisor" ./cmd/supervisor

run_case() {
  local repeat="$1"
  local method="$2"
  local use_beacon="$3"
  local run_dir="${OUTPUT_ROOT}/repeat-$(printf '%02d' "${repeat}")/${method}"
  local config_path="${run_dir}/config.yaml"
  local process_log_dir="${run_dir}/process_logs"

  if [[ -e "${run_dir}" ]]; then echo "output already exists: ${run_dir}; set OUTPUT_ROOT to a new path" >&2; return 1; fi
  mkdir -p "${process_log_dir}"
  sed -e "s|__RUN_DIR__|${run_dir}|g" -e "s|__TX_NUMBER__|${TX_NUMBER}|g" -e "s|__TX_SPEED__|${TX_SPEED}|g" -e "s|__TX_SOURCE__|${TX_SOURCE}|g" -e "s|__BLOCK_LIMIT__|${BLOCK_LIMIT}|g" \
    "experiments/aggregation_ablation/${method}.yaml.tmpl" | \
    sed -e "s/^  shard_num: 4$/  shard_num: ${SHARD_NUM}/" -e "s/^  node_num: 4$/  node_num: ${NODE_NUM}/" -e "s/^  batch_size: 0$/  batch_size: ${BATCH_SIZE}/" > "${config_path}"

  PROCESS_IDS=()
  for ((shard_id=0; shard_id<SHARD_NUM; shard_id++)); do
    for ((node_id=0; node_id<NODE_NUM; node_id++)); do
      "${BIN_DIR}/consensusnode" -config="${config_path}" -ip_table="${IP_TABLE}" \
        -shard_id="${shard_id}" -node_id="${node_id}" \
        > "${process_log_dir}/shard-${shard_id}-node-${node_id}.log" 2>&1 &
      PROCESS_IDS+=("$!")
    done
  done

  if [[ "${use_beacon}" == "true" ]]; then
    for ((node_id=0; node_id<NODE_NUM; node_id++)); do
      "${BIN_DIR}/beaconnode" -config="${config_path}" -ip_table="${IP_TABLE}" \
        -shard_id="${BEACON_SHARD_ID}" -node_id="${node_id}" \
        > "${process_log_dir}/beacon-node-${node_id}.log" 2>&1 &
      PROCESS_IDS+=("$!")
    done
  fi

  "${BIN_DIR}/supervisor" -config="${config_path}" -ip_table="${IP_TABLE}" \
    -shard_id="${SUPERVISOR_SHARD_ID}" -node_id=0 \
    > "${process_log_dir}/supervisor.log" 2>&1 &
  local supervisor_pid="$!"
  PROCESS_IDS+=("${supervisor_pid}")

  local deadline=$((SECONDS + TIMEOUT))
  while kill -0 "${supervisor_pid}" 2>/dev/null; do
    if grep -R -q -E 'failed to listen|level=(ERROR|WARN)' "${process_log_dir}"; then
      echo "${method}: node startup/runtime error; inspect ${process_log_dir}" >&2
      return 1
    fi
    if (( SECONDS >= deadline )); then
      echo "${method}: timed out after ${TIMEOUT}s; partial results are not a completed experiment" >&2
      return 1
    fi
    sleep 1
  done
  wait "${supervisor_pid}"
  sleep 3
  cleanup_processes

  test -s "${run_dir}/results/relay_stats_detail_tx_info.csv"
  if [[ "${use_beacon}" == "true" ]]; then
    test -s "${run_dir}/results/aggregation_tx_lifecycle.csv"
    test -s "${run_dir}/results/aggregation_batch_metrics.csv"
  fi

  if grep -R -E "level=(ERROR|WARN)" "${run_dir}/logs" "${process_log_dir}"; then
    echo "${method} repeat ${repeat} contains Error/Warn logs" >&2
    return 1
  fi
}

for ((repeat=1; repeat<=REPEATS; repeat++)); do
  methods=(relay batch_only full_aggregation)
  for ((offset=0; offset<3; offset++)); do
    method="${methods[$(((repeat-1+offset)%3))]}"
    use_beacon=true
    [[ "${method}" == relay ]] && use_beacon=false
    echo "running ${SHARD_NUM} shards, repeat ${repeat}/${REPEATS}: ${method}"
    run_case "${repeat}" "${method}" "${use_beacon}"
  done
done

python3 experiments/aggregation_ablation/analyze.py --input "${OUTPUT_ROOT}" --expected-txs "${TX_NUMBER}"
echo "comparison complete: ${OUTPUT_ROOT}/ablation_runs.csv"
