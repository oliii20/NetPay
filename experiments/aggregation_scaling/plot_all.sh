#!/bin/bash
set -euo pipefail
cd "$(dirname "$0")/../.."
for figure in tps latency latency_p95 destination_entries receiver_updates txpool stage_latency; do
  python3 "experiments/aggregation_scaling/plot_${figure}.py" "$@"
done
