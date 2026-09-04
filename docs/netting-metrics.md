# Netting experiment metrics

When `netting.metrics_enabled` is true, the Supervisor writes two additional files under
`supervisor.result_output_dir`:

- `netting_batch_metrics.csv` contains one row per committed MatchRoot batch.
- `netting_intent_metrics.csv` contains one row per payment intent lifecycle.

All durations are integer nanoseconds. Amount ratios are calculated from arbitrary-precision
integer totals and are emitted with six decimal places. Duplicate or reordered metric messages
are merged by `(phase, BatchID, IntentID, shard)` and do not change a result twice.

## Measurement boundaries

- Window time starts when the first intent enters the open Solver window and ends when the
  Vector-Cut is frozen.
- Match time covers the deterministic matcher. Merkle time covers settlement-tree and global-root
  construction.
- Beacon consensus time starts when the leader accepts the Solver proposal and ends when its
  MatchRoot block commits. Validation time is measured on the leader's proposal validation.
- PBFT byte columns are total payload bytes for a four-step topology calculation: one leader
  PrePrepare broadcast and all-node Prepare/Commit broadcasts. They include the wrapped message
  type and gob payload, but not TCP/IP, gRPC, or protobuf framing overhead.
- Reservation, settlement, fallback, capital-lock, and end-to-end intervals use normal shard
  block commit timestamps. Missing lifecycle endpoints produce an empty per-intent field and are
  excluded from batch averages.
- `StateReadCount` and `StateWriteCount` are protocol-level logical StateDB accesses, not physical
  LevelDB reads or trie-node writes. `ProofVerificationTime` is a metrics-path replay of the same
  deterministic two-level proof verification, so collection does not modify consensus execution.

The existing relay/broker CSV files remain unchanged. Netting protocol control messages are routed
away from the legacy collectors so they are not reported as unsupported transaction traffic.
