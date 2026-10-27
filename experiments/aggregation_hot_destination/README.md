# Static hot-destination aggregation

## Fine scan near full coverage

After other experiments have stopped, run:

```bash
bash experiments/aggregation_hot_destination/run_fine_sweep.sh
```

This runs 75/85/95/100% once each, with 24 shards and 14400 injected TPS.
The existing ceiling rule selects 18/21/23/24 shards, respectively; the final
reference uses unrestricted `full_aggregation`. Other settings match the first
scan. Set `OUTPUT_ROOT` to a new directory for another run.
The default output is `exp/aggregation-hot-destination-fine-v1/`.
`fine_sweep_summary.csv` joins overall completion TPS, overall mean/p95 latency,
CTX route coverage, per-stage CTX latency and Beacon batch diagnostics. Latency
columns ending in `_ms` are milliseconds; diagnostic columns ending in `Seconds`
are seconds. Throughput uses complete original transactions over first creation
to final completion, consistently for all four runs. Existing epoch statistics
remain available in raw results but are not the TPS reported by this summary.
Compare queue wait, consensus, validation, dispatch work and destination latency
before attributing any improvement to Beacon saturation. The scan alone cannot
establish causality; selection also changes traffic distribution and batch sizes.

Run from the repository root on branch `aggregation-hot-destination`.

## Dataset and selection

This experiment uses exactly the previous experiment dataset:
`data/25250000to25499999_BlockTransaction.csv`, `exclude_contract_txs: true`.
The ranker reuses the supervisor's Go CSV reader and `partition.DefaultAccountLoc`,
reads the first `TX_NUMBER` valid transactions (not raw CSV rows), excludes
intra-shard transactions from ranking, and sorts by incoming CTX count descending,
with shard ID ascending as the deterministic tie-breaker. It fails if insufficient
valid transactions exist. The file SHA256 is recorded for reproducibility.

For percentage p, K=ceil(p*S/100) destinations are selected before launching nodes.
All sources use the same fixed destination set throughout the run. CTXs for that
set use existing Full aggregation, including source abstract merging. Other CTXs
remain ordinary Relay transactions; intra-shard transactions remain unchanged.
There is no periodic update or mid-flight fallback. The route is encoded in the
transaction type at injection, before source confirmation. Nodes execute the
existing respective protocols. The static allowlist is an experiment workload
policy, not a new Byzantine authorization rule; existing proof checks remain.

The entire tested prefix is used for offline selection. This measures known-hotspot
feasibility, not an online predictor. A selected shard percentage is not the same
as the CTX percentage covered. No transactions are reordered, resampled, or removed.

## First scan: 24 shards, one run per configuration

```bash
SHARDS=24 SPEEDS="10800 14400" HOT_PERCENTAGES="25 50 75" \
TX_NUMBER=300000 OUTPUT_BASE=exp/aggregation-hot-destination-v1 \
bash experiments/aggregation_hot_destination/run.sh
```

Each speed runs Relay once, Full once, and hot 25/50/75% once: 10 runs total.
REPEATS is always 1, even if the environment specifies otherwise. Batch-only and
Aggregation-only are not included. Defaults: ordinary block limit2000, interval2s,
Beacon interval1s, aggregation window2s, credit chunk512, batch size0, timeout1800s.
The original settlement-per-block scheduler cap remains unchanged.
Existing output directories are rejected; choose a new OUTPUT_BASE to rerun.
Do not run overlapping jobs on the same machine ports (32000 onward).

## Four shard settings, 600 injected TPS per shard

```bash
for shards in 4 8 16 24; do
  SHARDS="$shards" SPEEDS="$((shards * 600))" HOT_PERCENTAGES="25 50 75" \
  TX_NUMBER=300000 OUTPUT_BASE=exp/aggregation-hot-destination-scale-v1 \
  bash experiments/aggregation_hot_destination/run.sh || break
done
```

This runs 20 experiments, without repeats. The dataset stays the same across
shard counts; accounts are remapped using each setting's original shard mapping.

## Outputs and plotting

For example: `exp/aggregation-hot-destination-v1/shards24-speed14400/` contains:

- `scaling_runs.csv`: complete logical TPS, CTX mean/p95, entries and receiver updates;
  also includes each route's completed CTX count, mean/p95 and actual aggregation ratio.
- `stage_latency.csv`, `txpool_timeseries.csv`: existing stage and queue summaries.
- `shards-24/destination_ranking.json`: dataset hash, filtered count, CTX counts/rank.
- `shards-24/repeat-01/hot_destination_25/selection.json`: fixed set and CTX coverage.
- Each method's `config.yaml`, raw results, leader queue samples and process logs.

Analysis rejects incomplete total counts and hot-route counts inconsistent with
preselection. Existing coordination diagnostics can be run without a rerun:

```bash
python3 experiments/aggregation_scaling/diagnose_coordination.py \
  --input exp/aggregation-hot-destination-v1/shards24-speed14400
```

Every figure has a separate script; the wrapper invokes all seven:

```bash
PATH=/opt/anaconda3/bin:$PATH MPLCONFIGDIR=/tmp/aggregation-mpl \
bash experiments/aggregation_scaling/plot_all.sh \
  --input exp/aggregation-hot-destination-v1/shards24-speed14400
```

Figures are saved as PNG/PDF under `figures/`. Single runs have no repeat error
bars. TPS is finite-workload completion throughput, including the drain tail.
300K transactions at high speed are a short screening experiment, not evidence of
long-term sustainable throughput.
