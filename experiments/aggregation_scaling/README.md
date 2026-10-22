# Three-method shard scaling experiment

From the repository root:

```bash
# Smoke check before the full grid (choose a fresh output directory).
REPEATS=1 SHARDS="4" TX_NUMBER=1000 TX_SPEED=1000 TIMEOUT=300 \
OUTPUT_ROOT=exp/aggregation-scaling-check \
bash experiments/aggregation_scaling/run.sh

# Full grid: 4 shard counts × 3 methods × 3 repeats.
REPEATS=3 SHARDS="4 8 16 24" TX_NUMBER=300000 TX_SPEED=20000 TIMEOUT=1800 \
OUTPUT_ROOT=exp/aggregation-scaling \
bash experiments/aggregation_scaling/run.sh

# Plotting uses matplotlib; run with your activated Python environment.
python3 -m pip install matplotlib
bash experiments/aggregation_scaling/plot_all.sh --input exp/aggregation-scaling
```

`SHARDS` defaults to `4 8 16 24`; `NODE_NUM=4` is fixed per shard. Each setting uses the
same dataset prefix, total transaction count and total injection rate. This is a
fixed-load scaling experiment, not a measurement of maximum sustainable capacity.
Full aggregation enables source summary merging and destination account merging;
batch-only enables neither merging step. Both use the same Beacon path and window.
The runner inherits `BLOCK_LIMIT=2000` (transaction entries), 2000 ms block interval,
2000 ms aggregation window, 512 credit chunk size from the existing ablation runner.
It generates an isolated network table and configuration for every shard count;
it does not modify the root config or root IP table. Runs execute sequentially,
rotating method order across repeats. Choose a fresh relative OUTPUT_ROOT for each
new experiment. Do not run multiple local grids simultaneously: ports overlap.

At 24 shards there are 96 consensus processes, plus 4 Beacon processes and 1
supervisor for the aggregation methods. Record hardware details; on one machine,
increasing process competition can affect scaling results.

Outputs:

- `scaling_runs.csv`: one row per complete run: logical TPS, CTX TPS, overall and CTX
  mean/p95 latency, completed counts, destination entries/receiver updates and their
  per-CTX ratios. Incomplete runs fail instead of extrapolating throughput.
- `stage_latency.csv`: mean CTX latency components per run.
- `txpool_timeseries.csv`: 1-second common time grid, total and maximum-shard leader
  pool entry counts. Raw roughly 500 ms samples remain in each run's
  `block_record/shard=*_node=0/txpool_record.csv`.
- `figures/*.png` and `figures/*.pdf`: each figure in both formats.
- `shards-N/repeat-XX/METHOD/`: raw results, logs, configs and block records.

Each figure has its own script (shared styling only is in `plot_common.py`):

```bash
python3 experiments/aggregation_scaling/plot_tps.py --input exp/aggregation-scaling
python3 experiments/aggregation_scaling/plot_latency.py --input exp/aggregation-scaling
python3 experiments/aggregation_scaling/plot_latency_p95.py --input exp/aggregation-scaling
python3 experiments/aggregation_scaling/plot_destination_entries.py --input exp/aggregation-scaling
python3 experiments/aggregation_scaling/plot_receiver_updates.py --input exp/aggregation-scaling
python3 experiments/aggregation_scaling/plot_txpool.py --input exp/aggregation-scaling
python3 experiments/aggregation_scaling/plot_stage_latency.py --input exp/aggregation-scaling
```

Bars use the run mean with sample standard deviation (zero with one repeat).
The latency bars default to **CTX** latency; overall latency is also in the CSV.
TPS counts original completed user transactions (ITX + CTX), not settlement objects:
completed count / (last completion - first creation). A fixed-load finite replay
TPS should not be interpreted as the maximum sustainable throughput.

The stage chart is a stacked **mean**, never a sum of p95 values:

1. Source queue + confirmation: transaction creation → source debit commitment.
2. Coordination wait + confirmation: source commitment → Beacon finalization.
   Includes source-to-coordinator transmission, aggregation/window/queue waiting,
   batch construction and Beacon consensus; these are not independently separated.
3. Destination transfer + queue + confirmation: Beacon finalization → actual
   original transaction's destination chunk commitment.

For relay, the coordination component is zero; its third component covers
source commitment → relay destination commitment, including relay transmission.
Fallback CTXs, if present, use the relay decomposition. New runs are required for
the exported `BeaconFinalizedAtUnixNano`; old batch CSVs do not contain it.
Missing or non-monotonic stage timestamps cause an explicit error.

Pool length measures queued **transaction objects**, excluding proposed transactions
already removed from the pool, Beacon queues and in-flight messages. One settlement
counts as one entry even if it contains many receipts. Thus the curve measures pool
entry occupancy, not unfinished original transactions or queue bytes. Only shard
leaders are counted, avoiding multiplication by PBFT replicas. Curves show each
repeat separately and align samples by wall-clock time with last-observation carry
forward; runs start at the first original transaction creation.

Destination entries count committed Relay2 + settlement objects; receiver updates
count receiver balance additions for those CTXs, not physical database writes.
Aggregation does not grant a settlement a realistic one-transfer execution cost:
this grid retains the existing transaction-entry block limit.

To regenerate aggregate CSVs without rerunning nodes:

```bash
python3 experiments/aggregation_scaling/analyze.py \
  --input exp/aggregation-scaling --expected-txs 300000
```

## Coordination diagnostics after the dispatch fix

Shard replicas now receive header-only finalizations. Original IDs and chunk
mappings are sent once to the supervisor; settlement packages and proofs are
unchanged. The RPC connection-pool mutex no longer spans network calls. This
transport fix applies to relay too, so rerun all three methods for a fair comparison.

Use a fresh output directory:

```bash
REPEATS=1 SHARDS="16 24" TX_NUMBER=300000 TX_SPEED=20000 TIMEOUT=1800 \
OUTPUT_ROOT=exp/aggregation-scaling-dispatch-fixed \
bash experiments/aggregation_scaling/run.sh

python3 experiments/aggregation_scaling/diagnose_coordination.py \
  --input exp/aggregation-scaling-dispatch-fixed
```

Batch CSVs now include window open/seal, build start and proposal-ready timestamps,
frozen queue wait/length (includes the current batch), total build and local
validation durations. `SourceToHandlerMeanNs` measures source block commitment to
entry into the receipt handler: source publication, network delivery and Beacon
message-buffer waiting are combined. `HandlerToBuildMeanNs` covers decode, receipt
processing, bootstrap, window and queue waiting until build start. Both are weighted
by original logical IDs; `ReceiptTimingCount` reports coverage. They do not isolate
network transit from receive-buffer waiting. Source CommitTime uses the same source
block commitment timestamp as the source metrics.

Dispatch timing separates supervisor notification, header broadcast and package
build/encoding/send. These happen **after** this batch's commitment, can delay later
batches, and must not be added to this batch's precommit latency. `CommitDeliveryTimeNs`
is the inclusive delivery operation duration (not an additional independent stage).
The diagnostic CSV reports CTX-weighted precommit durations and accumulated dispatch
cost separately; frozen queue wait overlaps handler-to-build waiting.

The 512-credit chunk limit does not bound a Beacon batch. `BATCH_SIZE` optionally
sets the existing window close threshold (default 0, unchanged). For example,
`BATCH_SIZE=20000` closes a window when pending summaries reach that threshold.
It counts source abstracts when source merging is enabled, and a whole source block
can overshoot the threshold. It is **not** a strict byte or original-CTX limit.
First rerun with the unchanged default to isolate the dispatch fixes; evaluate a
finite threshold as a separate experiment. Authentication, replay checks and batch
validation remain enabled.
