# Aggregation ablation (4 shards × 4 nodes)

Run from the repository root. Requires Go 1.25.7, Python 3, local TCP sockets,
and `data/25250000to25499999_BlockTransaction.csv` (existing default dataset).

```bash
# Small real-data integration run, all four schemes:
TX_NUMBER=1000 TX_SPEED=1000 OUTPUT_ROOT=exp/aggregation-ablation-check \
  bash experiments/aggregation_ablation/run.sh

# Paper run: same first 300,000 transfers for every scheme, five repeats:
REPEATS=5 TX_NUMBER=300000 TX_SPEED=20000 TIMEOUT=1800 \
  OUTPUT_ROOT=exp/aggregation-ablation-300k \
  bash experiments/aggregation_ablation/run.sh

# Recompute the combined CSV without rerunning nodes:
python3 experiments/aggregation_ablation/analyze.py \
  --input exp/aggregation-ablation-300k --expected-txs 300000
```

Use a fresh OUTPUT_ROOT: existing per-run directories are deliberately not deleted.
The runner builds all three binaries, rotates method order across repeats, stops
its own child processes on failure/timeout, and rejects Error/Warn logs.
`BLOCK_LIMIT` defaults to 2000 transaction entries per shard block. All methods
use 2-second shard block intervals; both Beacon methods use a 2-second window
and at most 512 credit operations per settlement. Source-side abstract merging
is enabled only for full_aggregation; batch_only retains individual source summaries.

- `relay`: ordinary per-transaction Relay2 destination credits, no Beacon.
- `batch_only`: Beacon authenticates a batch and commits individual credits;
  multiple credits to the same account remain separate AddBalance operations.
- `full_aggregation`: Source shards merge eligible payment abstracts, then Beacon
  merges credits by destination/account/asset before destination execution.
  Each merged credit performs one AddBalance operation. Original payment IDs
  remain available for logical transaction counts and individual latency records.

The full_aggregation vs batch_only comparison measures the combined effect of
source-side abstract merging and destination-side account aggregation. It does
not isolate destination-side aggregation alone. Earlier runs with source merging
disabled represent a different configuration and must not be pooled with new runs.

Batch-only changes committed credit data and real execution; it does not merely
change reported counters. Its mode is included in the Merkle-committed chunk
payload and checked against the finalized header. Existing stored batches from
before this format change must not be mixed with new runs.

## Output and definitions

`OUTPUT_ROOT/ablation_runs.csv` contains one row per method/repeat:

| Column | Meaning |
|---|---|
| logical_tps | All completed original user transactions / total observation seconds |
| ctx_tps | Completed original CTXs / the same observation seconds |
| latency_mean_ms, latency_p95_ms | All original transactions, creation to final destination commit |
| ctx_latency_mean_ms, ctx_latency_p95_ms | CTXs only, creation to actual destination chunk commit |
| destination_entries | Leader committed Relay2 + aggregation settlement transaction entries |
| receiver_updates | Receiver AddBalance operations for CTXs, excluding initialization and intra-shard transfers |
| destination_entries_per_ctx | Destination entries / completed CTXs |
| receiver_updates_per_ctx | Receiver updates / completed CTXs |
| completed_txs, completed_ctx, completion_rate | Completion checks; incomplete runs fail analysis |

Observation seconds run from the earliest user creation to the latest final
commit, including injection and drain time. This is end-to-end run throughput,
not a steady-state capacity estimate. Settlement transactions are not additional
user transactions. All source/Beacon waiting is included in latency. Only node 0
per shard contributes block counts, avoiding PBFT replica multiplication.

Receiver updates are logical execution operations, not disk writes: Relay2 has
one receiver addition per committed entry; settlement publishes the number of
credit operations executed by its executor. UniqueAccountCount separately counts
distinct accounts in each settlement. Pre-execution/proposal validation is not
counted as committed application work. No fallback is enabled by these templates.

Detailed records live under `repeat-XX/METHOD/results/` and `block_record/`.
These experiments use the existing transaction-count block limit. They do not
establish gains under equal byte/gas limits; assess those limits separately.
Random-source runs (`TX_SOURCE=random_source`) are useful only for smoke checks:
they do not replay an identical transaction set across methods.

## Checks

```bash
python3 -m unittest discover -s experiments/aggregation_ablation -p 'test_*.py'
go test ./pkg/aggregation/... ./supervisor/measure/aggregationstats ./config
```

New runs also include `aggregation_only`: Beacon merges same-account/asset credits
but emits one settlement per resulting credit (effective chunk size 1), with source
summary merging disabled. Full keeps source merging enabled. See
`../aggregation_scaling/README.md` for the four-way controls and interpretation.
Set `INCLUDE_AGGREGATION_ONLY=0` to retain the previous three-method runner.
