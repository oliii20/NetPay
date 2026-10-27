# Aligned BrokerChain and CLPA+Relay baselines

From the repository root:

```bash
python3 experiments/aggregation_baselines/run.py \
  --shards 4 8 16 24 --tx-number 300000 --per-shard-rate 600 \
  --clpa-interval 50 --timeout 1800 \
  --output exp/aggregation-aligned-baselines-v1
```

Runs each of static BrokerChain and CLPA+Relay once at each shard count: 8 runs.
Injection rates are 2400, 4800, 9600, 14400 TPS. Uses the unchanged ablation Relay
config template, with aggregation disabled, normal block limit 2000, block interval
2000 ms, 4 replicas per shard, broker count 50 and original broker file.
Dataset is `data/25250000to25499999_BlockTransaction.csv`, with contract filtering
and the first 300000 accepted transactions read by the original CSVSource.
CLPA interval is 50 seconds, not a number of blocks. At high speeds the injection
may finish before the first partition round; inspect actual CLPA rounds/logs when
interpreting the result. Migration effects are not guaranteed by the method label.

Outputs are in `shards-N/static_broker/` and `shards-N/clpa_relay/` with config,
results, process logs, and block records. `baseline_runs.csv` is checkpointed after
each successful case. Existing cases are rejected; use a fresh output path to rerun.
Do not overlap with other experiment jobs on the same machine: ports start at 32000.

Summary validates 300000 unique completed original transactions. TPS is completed
count divided by last commit minus first creation. Latency includes all original
transactions, from original creation to final commitment, not individual Broker1/2
objects. Mean and P95 are both written in milliseconds. This is finite-workload
completion throughput, not maximum sustainable throughput. No Beacon nodes run.

If only one method is wanted, use `--methods static_broker` or
`--methods clpa_relay` with a new output directory.

## Interval20 + five hot brokers / 45 random brokers

```bash
python3 experiments/aggregation_baselines/run.py \
  --shards 4 8 16 24 --tx-number 300000 --per-shard-rate 600 \
  --clpa-interval 20 --broker-selection hot-random \
  --broker-count 50 --hot-brokers 5 --broker-seed 1 --timeout 1800 \
  --output exp/aggregation-aligned-baselines-interval20-hot5-v2
```

The current CLI default CLPA interval is 20 seconds; the explicit interval50
command above reproduces the original baseline. Broker policy defaults to original;
the hot-random option selects from accounts in the same first TX_NUMBER accepted
transactions, using the original Go CSVSource and contract filtering. Account
activity is the number of transactions involving it as sender or recipient (a
self-transfer, if accepted, counts once). Sort descending by activity and then
ascending address to break ties. The top five are retained; sample 45 distinct
accounts uniformly from the remaining candidate accounts using seed1. The same
fixed list is used at every shard count. Dataset, order, amounts, liquidity
initialization, and per-transaction broker assignment logic are unchanged.
This seed controls broker-list selection, not every source of runtime randomness.

`selected_brokers.txt` and `.txt.json` retain the chosen addresses, activity counts,
roles, candidate count, and seed. Each BrokerChain config points to this file with
broker_num50. The global original broker file is not overwritten. Selection is an
offline workload-informed policy; account rankings include the whole tested prefix.
