# Beacon-coordinated cross-shard aggregation

When `aggregation.enabled` is true, every cross-shard native transfer follows
this path:

1. The source shard validates the payment, debits the sender, and records a
   replay-protected outbox entry in the same state transition.
2. After the source block commits, its leader sends a finalized-block receipt
   to every Beacon replica. The receipt contains the committed block identity,
   state root, epoch, and the cross-shard payments in that block.
3. Beacon replicas maintain continuous per-shard watermarks. A window closes at
   `batch_size` or `max_window_duration_ms` and is agreed by Beacon PBFT.
4. The deterministic builder groups receipts by
   `(destination shard, asset, recipient)`, sums each group's amount, and only
   then splits the resulting credits by `credit_chunk_size`.
5. Beacon commits `CutRoot`, `ReceiptRoot`, `ShardAggregationRoot`, and their
   `AggregationRoot`. It broadcasts the finalized header and sends each target
   leader only the shard package and two-level Merkle proof it needs.
6. A target shard enqueues settlement only after it has both the finalized
   header and its package. Execution verifies the proofs, credits every
   recipient once, and records `(BatchID, shard, chunk)` for idempotence.

There is no solver, bidirectional matching, escrow reservation, or fallback
path in this protocol. The Beacon chain coordinates ordering and proof
commitments; it does not custody balances.

## Metrics

With `aggregation.metrics_enabled`, the Supervisor writes:

- `aggregation_batch_metrics.csv`: one row per Beacon batch, including source
  receipt count, resulting credit count, collapsed receipt count, number of
  target settlement transactions, vector-cut skew, build time, PBFT traffic,
  and stored header bytes.
- `aggregation_execution_metrics.csv`: source-debit and target-settlement
  events, including represented receipt count, unique account count, logical
  state accesses, and proof-verification time.

Shard `block_record.csv` files separately report
`CrossShardPaymentTxCount/Bytes` and
`AggregationSettlementTxCount/Bytes`, allowing destination transaction and
encoded-byte savings to be compared with the relay and broker baselines.

## Local validation

`bash example_run_aggregation.sh` starts a 4-shard × 4-node cluster, four Beacon
replicas, and the Supervisor using `config.aggregation-e2e.yaml` and
`ip_table_aggregation.json`.
