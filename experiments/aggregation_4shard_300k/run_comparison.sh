#!/bin/bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT_DIR}"

SHARD_NUM=4
NODE_NUM=4
BEACON_SHARD_ID=2147483646
SUPERVISOR_SHARD_ID=2147483647
REPEATS="${REPEATS:-1}"
TX_NUMBER="${TX_NUMBER:-300000}"
OUTPUT_ROOT="${OUTPUT_ROOT:-.exp/aggregation-4shard-300k}"
IP_TABLE="ip_table_aggregation.json"
BIN_DIR="${OUTPUT_ROOT}/bin"
PROCESS_IDS=()

cleanup_processes() {
  for process_id in "${PROCESS_IDS[@]:-}"; do
    kill "${process_id}" 2>/dev/null || true
  done
  PROCESS_IDS=()
}
trap cleanup_processes EXIT INT TERM

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

  rm -rf "${run_dir}"
  mkdir -p "${process_log_dir}"
  sed -e "s|__RUN_DIR__|${run_dir}|g" -e "s|__TX_NUMBER__|${TX_NUMBER}|g" \
    "experiments/aggregation_4shard_300k/${method}.yaml.tmpl" > "${config_path}"

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
  if (( repeat % 2 == 1 )); then
    echo "running repeat ${repeat}/${REPEATS}: monoxide then aggregation"
    run_case "${repeat}" monoxide false
    run_case "${repeat}" aggregation true
  else
    echo "running repeat ${repeat}/${REPEATS}: aggregation then monoxide"
    run_case "${repeat}" aggregation true
    run_case "${repeat}" monoxide false
  fi
done

python3 experiments/aggregation_4shard_300k/analyze.py --input "${OUTPUT_ROOT}" --expected-txs "${TX_NUMBER}"
echo "comparison complete: ${OUTPUT_ROOT}/comparison.md"
