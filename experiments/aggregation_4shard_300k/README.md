# 4-shard, 300K Monoxide vs. aggregation

This experiment compares the existing `static_relay` implementation (the
Monoxide-style baseline) with Beacon-coordinated aggregation. Both cases use:

- 4 shards and 4 PBFT replicas per shard;
- the first 300,000 valid non-contract transfers read from
  `data/25250000to25499999_BlockTransaction.csv`;
- block capacity 5,000, block interval 2 seconds, and injection rate 20,000 tx/s;
- duration-only 2-second Beacon windows (no payment-count threshold) and
  settlement chunks of at most 512 aggregated credits;
- optional source-shard abstract aggregation is enabled for the aggregation
  case: each finalized source block compacts payments by destination shard,
  asset, and recipient before publishing them to the Beacon chain;
- the same account-to-shard mapping, network settings, binaries, and machine.

Run one paired repetition from the repository root:

```bash
bash experiments/aggregation_4shard_300k/run_comparison.sh
```

For a result suitable for a preliminary paper plot, run at least five paired
repetitions:

```bash
REPEATS=5 bash experiments/aggregation_4shard_300k/run_comparison.sh
```

Outputs are written below `.exp/aggregation-4shard-300k/`. The main summary is
`comparison.md`; per-run values are in `comparison_runs.csv`.

## Metric semantics

- `logical_tps`: 300,000 completed user transfers divided by the interval from
  the earliest injection timestamp to the latest final destination commit.
- latency: final destination commit minus original injection time for each
  logical user transfer. Aggregation protocol transactions are not counted as
  additional user transfers. The current aggregation value conservatively uses
  the time when the transfer's whole Beacon batch has settled, so it cannot
  make aggregation latency look artificially better.
- transaction pool: sum of the four shard-leader pool sizes sampled during the
  logical execution interval. The report includes mean, p95, and peak.
- `block_TxBodyLen`: total transaction entries committed by the four shard
  leaders. This exposes the destination-entry reduction directly.
- aggregation compression: finalized receipt count divided by aggregated
  credits and settlement transactions. `aggregation_batch_metrics.csv` also
  reports `SourceAbstractCount` and `SourceCollapsedPaymentCount` for the
  optional source-shard-to-Beacon compaction stage.

Do not compare the legacy `relay_stats_brief_info.csv` TPS directly against the
aggregation batch CSV. The former counts completed logical relay transactions,
whereas the latter describes protocol batches. `analyze.py` joins inner-shard
relay lifecycles with cross-shard aggregation lifecycles to keep the numerator
and latency endpoint identical between methods.

The runner alternates method order between repetitions to reduce thermal and
cache-order bias. The report includes per-run values and mean ± sample standard
deviation; use confidence intervals as well for publication-quality claims.
