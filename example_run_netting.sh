#!/bin/bash

set -euo pipefail

SHARD_NUM=4
NODE_NUM=4
BEACON_SHARD_ID=2147483646
SOLVER_SHARD_ID=2147483645
SUPERVISOR_SHARD_ID=2147483647
CONFIG_PATH="config.netting-e2e.yaml"
IP_TABLE_PATH="ip_table_netting.json"
RUN_DIR="./.exp/netting-e2e"
BIN_DIR="${RUN_DIR}/bin"
PROCESS_LOG_DIR="${RUN_DIR}/process_logs"
PROCESS_IDS=()

cleanup() {
  for process_id in "${PROCESS_IDS[@]:-}"; do
    kill "${process_id}" 2>/dev/null || true
  done
}
trap cleanup EXIT INT TERM

rm -rf "${RUN_DIR}"
mkdir -p "${BIN_DIR}"
mkdir -p "${PROCESS_LOG_DIR}"

go mod download
go build -o "${BIN_DIR}/consensusnode" ./cmd/consensusnode
go build -o "${BIN_DIR}/beaconnode" ./cmd/beaconnode
go build -o "${BIN_DIR}/solver" ./cmd/solver
go build -o "${BIN_DIR}/supervisor" ./cmd/supervisor

for ((shard_id=0; shard_id<SHARD_NUM; shard_id++)); do
  for ((node_id=0; node_id<NODE_NUM; node_id++)); do
    "${BIN_DIR}/consensusnode" \
      -config="${CONFIG_PATH}" -ip_table="${IP_TABLE_PATH}" \
      -shard_id="${shard_id}" -node_id="${node_id}" \
      > "${PROCESS_LOG_DIR}/shard-${shard_id}-node-${node_id}.log" 2>&1 &
    PROCESS_IDS+=("$!")
  done
done

for ((node_id=0; node_id<NODE_NUM; node_id++)); do
  "${BIN_DIR}/beaconnode" \
    -config="${CONFIG_PATH}" -ip_table="${IP_TABLE_PATH}" \
    -shard_id="${BEACON_SHARD_ID}" -node_id="${node_id}" \
    > "${PROCESS_LOG_DIR}/beacon-node-${node_id}.log" 2>&1 &
  PROCESS_IDS+=("$!")
done

"${BIN_DIR}/solver" \
  -config="${CONFIG_PATH}" -ip_table="${IP_TABLE_PATH}" \
  -shard_id="${SOLVER_SHARD_ID}" -node_id=0 \
  > "${PROCESS_LOG_DIR}/solver.log" 2>&1 &
PROCESS_IDS+=("$!")

"${BIN_DIR}/supervisor" \
  -config="${CONFIG_PATH}" -ip_table="${IP_TABLE_PATH}" \
  -shard_id="${SUPERVISOR_SHARD_ID}" -node_id=0 \
  > "${PROCESS_LOG_DIR}/supervisor.log" 2>&1 &
SUPERVISOR_PID="$!"
PROCESS_IDS+=("${SUPERVISOR_PID}")

wait "${SUPERVISOR_PID}"

BATCH_METRICS="${RUN_DIR}/results/netting_batch_metrics.csv"
INTENT_METRICS="${RUN_DIR}/results/netting_intent_metrics.csv"
test -s "${BATCH_METRICS}"
test -s "${INTENT_METRICS}"
test "$(wc -l < "${BATCH_METRICS}")" -gt 1
test "$(wc -l < "${INTENT_METRICS}")" -gt 1

if grep -R -E "level=(ERROR|WARN)" "${RUN_DIR}/logs"; then
  echo "experiment logs contain errors or warnings" >&2
  exit 1
fi

echo "netting 4x4 experiment completed"
echo "batch metrics: ${BATCH_METRICS}"
echo "intent metrics: ${INTENT_METRICS}"
