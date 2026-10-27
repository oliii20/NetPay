# Five-method comparison with interval-20 CLPA and hot-random BrokerChain

All 20 plotted points are single completed runs; no error bars are shown. Every selected run completed 300,000 original transactions.

## Metric definitions

- TPS is the arithmetic mean of per-epoch native TPS, retaining zero-transaction epochs. For Batch-only and Proposed, the native epoch window is extended from recorded relay entries to all 300,000 logical payments; native TPS values are rounded to two decimals in the source CSV, so those extended values are approximate.
- Latency is the mean end-to-end time from original transaction creation to final commit for all 300,000 original transactions.
- Proposed uses full aggregation at 4/8/16 shards and 50% hot-destination aggregation at 24 shards.

## Final values

| Shards | Method | Mean epoch TPS | Mean latency (s) | Completion TPS |
|---:|---|---:|---:|---:|
| 4 | Monoxide | 1526.30 | 28.141 | 1662.85 |
| 4 | BrokerChain | 1607.90 | 24.079 | 1759.86 |
| 4 | CLPA+Relay | 1404.75 | 10.817 | 1578.78 |
| 4 | Batch-only | 2011.84 | 7.245 | 2252.78 |
| 4 | Proposed | 2012.44 | 6.906 | 2252.68 |
| 8 | Monoxide | 2338.22 | 21.152 | 2669.08 |
| 8 | BrokerChain | 2523.50 | 17.963 | 2918.13 |
| 8 | CLPA+Relay | 2083.14 | 13.673 | 2552.10 |
| 8 | Batch-only | 3399.16 | 9.063 | 4113.06 |
| 8 | Proposed | 3359.28 | 8.832 | 4089.91 |
| 16 | Monoxide | 3389.41 | 14.526 | 4137.19 |
| 16 | BrokerChain | 3717.87 | 12.494 | 4689.37 |
| 16 | CLPA+Relay | 3113.90 | 13.647 | 2731.06 |
| 16 | Batch-only | 4578.23 | 8.418 | 6021.60 |
| 16 | Proposed | 4495.06 | 8.864 | 6008.57 |
| 24 | Monoxide | 3773.10 | 11.852 | 4740.28 |
| 24 | BrokerChain | 4387.94 | 10.639 | 5852.27 |
| 24 | CLPA+Relay | 2953.32 | 22.480 | 2917.42 |
| 24 | Batch-only | 4903.99 | 12.816 | 6782.48 |
| 24 | Proposed | 4998.39 | 8.928 | 6962.18 |

## Main comparison

- At 4 shards, Proposed delivers 25.2% more mean epoch TPS than the strongest non-aggregation baseline (BrokerChain) and 36.2% lower mean latency than the lowest-latency non-aggregation baseline (CLPA+Relay).
- At 8 shards, Proposed delivers 33.1% more mean epoch TPS than the strongest non-aggregation baseline (BrokerChain) and 35.4% lower mean latency than the lowest-latency non-aggregation baseline (CLPA+Relay).
- At 16 shards, Proposed delivers 20.9% more mean epoch TPS than the strongest non-aggregation baseline (BrokerChain) and 29.1% lower mean latency than the lowest-latency non-aggregation baseline (BrokerChain).
- At 24 shards, Proposed delivers 13.9% more mean epoch TPS than the strongest non-aggregation baseline (BrokerChain) and 16.1% lower mean latency than the lowest-latency non-aggregation baseline (BrokerChain).

## CLPA interval: 50 s versus 20 s

| Shards | TPS change | Latency change | Migrations (50→20 s) | Total migration time (50→20 s) | Zero epochs (50→20 s) |
|---:|---:|---:|---:|---:|---:|
| 4 | +8.7% | -50.0% | 3→7 | 34.96→72.97 s | 1→1 |
| 8 | +25.1% | -32.6% | 2→4 | 36.00→50.99 s | 1→1 |
| 16 | +33.4% | -16.0% | 1→2 | 79.03→65.01 s | 0→0 |
| 24 | +32.9% | +90.3% | 1→2 | 164.03→151.98 s | 1→1 |

The 20-second interval raises CLPA mean epoch TPS at every shard count, but it does not uniformly reduce latency. At 4/8/16 shards, mean latency falls; at 24 shards it rises from 11.810 s to 22.480 s. The 24-shard run now performs 2 migrations instead of 1; the longest migration is 100.99 s, and the second migration occurs while a substantial transaction tail remains. This shifts migration cost into the observed transaction lifecycle rather than leaving most of it after workload completion.

For 24 shards, 95% completion moves from 44.53 s to 81.96 s and full completion from 61.52 s to 102.83 s. Thus, the higher mean epoch TPS and worse end-to-end latency are not contradictory: epoch TPS measures throughput inside each epoch's active block window, while latency includes waiting across repartition pauses.

The result supports treating the 20-second interval as a throughput/latency trade-off rather than a universally superior setting. A longer workload covering multiple steady-state migration cycles is still needed before claiming sustained-capacity superiority.

## Audit notes

- v3 contains eight complete baseline detail files with 300,000 unique original transactions each.
- No WARN or ERROR records were found in the v3 process logs.
- The BrokerChain list contains 50 unique brokers: five highest-activity accounts plus 45 seeded random accounts (seed 1), shared across shard counts.
- All selected runs use the same filtered 300,000-transaction dataset, 600 tx/s per shard, four nodes per shard, block capacity 2,000, and a 2,000 ms shard block interval. Aggregation runs use a 1,000 ms Beacon interval, a 2,000 ms aggregation window, and credit chunks of 512.
