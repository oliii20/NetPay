# 4-shard / 300,000-transaction comparison

All TPS and latency values count logical user transactions; aggregation settlement transactions are not counted as extra user transactions.

| Metric | Monoxide mean ± SD | Aggregation mean ± SD | Mean change |
|---|---:|---:|---:|
| logical_tps | 4110.449 ± 0.000 | 5925.211 ± 0.000 | +44.15% |
| latency_mean_ms | 35116.344 ± 0.000 | 19025.241 ± 0.000 | -45.82% |
| latency_p95_ms | 54524.201 ± 0.000 | 30204.207 ± 0.000 | -44.60% |
| txpool_mean_total | 131538.641 ± 0.000 | 68738.248 ± 0.000 | -47.74% |
| txpool_peak_total | 252056.000 ± 0.000 | 180086.000 ± 0.000 | -28.55% |
| block_TxBodyLen | 524654.000 ± 0.000 | 300317.000 ± 0.000 | -42.76% |

## Aggregation compression

- aggregation_receipts: 224654.000
- aggregation_source_abstracts: 157658.000
- aggregation_credits: 148423.000
- aggregation_settlement_txs: 317.000
- receipts_per_source_abstract: 1.425
- receipts_per_credit: 1.514
- receipts_per_settlement_tx: 708.688
