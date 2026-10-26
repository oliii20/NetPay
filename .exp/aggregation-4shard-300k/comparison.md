# 4-shard / 300,000-transaction comparison

All TPS and latency values count logical user transactions; aggregation settlement transactions are not counted as extra user transactions.

| Metric | Monoxide mean ± SD | Aggregation mean ± SD | Mean change |
|---|---:|---:|---:|
| logical_tps | 4121.213 ± 0.000 | 1885.271 ± 0.000 | -54.25% |
| latency_mean_ms | 35107.446 ± 0.000 | 59540.541 ± 0.000 | +69.60% |
| latency_p95_ms | 54327.566 ± 0.000 | 133693.756 ± 0.000 | +146.09% |
| txpool_mean_total | 131423.924 ± 0.000 | 21823.546 ± 0.000 | -83.39% |
| txpool_peak_total | 258618.000 ± 0.000 | 180030.000 ± 0.000 | -30.39% |
| block_TxBodyLen | 524654.000 ± 0.000 | 300365.000 ± 0.000 | -42.75% |

## Aggregation compression

- aggregation_receipts: 224654.000
- aggregation_credits: 157678.000
- aggregation_settlement_txs: 365.000
- receipts_per_credit: 1.425
- receipts_per_settlement_tx: 615.490
