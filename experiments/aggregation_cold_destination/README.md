# CTX2 capacity-threshold cold-destination exclusion

This experiment keeps low-load destination shards on Relay and enables existing
Full aggregation only for destinations whose expected CTX2 arrival rate reaches
a configured fraction of that shard's transaction-processing capacity. The
Beacon does not classify shards: selection happens offline and the supervisor
uses the resulting fixed `destination_shards` allowlist during injection.

For destination `d`, the selector estimates:

```text
ctx2_tps(d) = injection_speed * ctx2_count(d) / sampled_transactions
capacity_tps = block_limit * 1000 / block_interval_ms
utilization(d) = ctx2_tps(d) / capacity_tps
```

Destination `d` is aggregated when `utilization(d) >= threshold`. Counts exactly
on the threshold are included. CTX2 is counted only for cross-shard transactions;
intra-shard transactions remain unchanged. The same filtered dataset prefix and
account mapping used by the supervisor are used for selection.

## Preflight analysis

Analyze the dataset without starting any nodes:

```bash
SHARDS="4 8 16 24" SPEEDS="2400 4800 9600 14400" \
TX_NUMBER=300000 THRESHOLDS="10 20 30 40" \
bash experiments/aggregation_cold_destination/preflight.sh
```

Results are written under `exp/aggregation-cold-destination-preflight/`. Each
speed directory contains `capacity_analysis.json` and `capacity_analysis.csv`
with per-shard CTX2 rate, capacity utilization, routing decision and selected
CTX coverage. This is a feasibility screen, not evidence of runtime benefit;
Beacon coordination, queueing and settlement cost require a real experiment.
The report labels a selection as `relay_equivalent`, `partial_aggregation` or
`full_aggregation_equivalent`; prioritize partial selections for formal runs so
that threshold methods do not merely duplicate a baseline.

## Formal experiment

Run Relay, unrestricted Full aggregation and thresholds 10/20/30/40%:

```bash
SHARDS=24 SPEEDS="10800 14400" TX_NUMBER=300000 \
bash experiments/aggregation_cold_destination/run.sh
```

`BLOCK_LIMIT` defaults to 2000 and `BLOCK_INTERVAL_MS` to 2000, giving each
ordinary shard a theoretical capacity of 1000 transactions/second. A 20%
threshold therefore selects destinations with at least 200 expected CTX2/s.
Every method uses the same workload and protocol settings. Each selected method
writes `selection.json`; standard scaling summaries and plots remain compatible.
`cold_exclusion_summary.csv` joins selection coverage, selected/cold shard counts,
completion throughput and latency for direct comparison.

Selection uses the complete tested prefix, so it is a static oracle experiment,
not an online predictor. A later adaptive policy would belong at the supervisor
or require authenticated shard-load reports; the Beacon alone cannot observe
Relay traffic and therefore cannot safely make this classification.
