# 4-shard / 100-transaction comparison

All TPS and latency values count logical user transactions; aggregation settlement transactions are not counted as extra user transactions.

| Metric | Monoxide mean ± SD | Aggregation mean ± SD | Mean change |
|---|---:|---:|---:|
| logical_tps | 17.264 ± 0.000 | 12.250 ± 0.000 | -29.04% |
| latency_mean_ms | 4777.135 ± 0.000 | 6699.581 ± 0.000 | +40.24% |
| latency_p95_ms | 5792.350 ± 0.000 | 8163.472 ± 0.000 | +40.94% |
| txpool_mean_total | 35.273 ± 0.000 | 18.938 ± 0.000 | -46.31% |
| txpool_peak_total | 100.000 ± 0.000 | 100.000 ± 0.000 | +0.00% |
| block_TxBodyLen | 172.000 ± 0.000 | 104.000 ± 0.000 | -39.53% |

## Aggregation compression

- aggregation_receipts: 72.000
- aggregation_credits: 69.000
- aggregation_settlement_txs: 4.000
- receipts_per_credit: 1.043
- receipts_per_settlement_tx: 18.000
