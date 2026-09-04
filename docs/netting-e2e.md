# Netting end-to-end experiment

This repository includes a reproducible 4-shard by 4-node local run for the
netting pipeline.

Run it from the repository root:

```bash
./example_run_netting.sh
```

The script uses:

- `config.netting-e2e.yaml` for a 4x4 `static_relay` topology with netting
  enabled.
- `ip_table_netting.json` for ordinary shards, the solver shard, the beacon
  shard, and the supervisor.
- `./.exp/netting-e2e` as the isolated experiment output directory.

The script builds fresh binaries, starts ordinary shard nodes, beacon nodes, the
solver, and the supervisor, then waits for the supervisor to finish injecting and
observing the configured workload. It fails if either netting metrics file is
missing, if a metrics file contains only its header, or if the experiment logs
contain `level=ERROR` or `level=WARN`.

Successful runs write:

- `./.exp/netting-e2e/results/netting_batch_metrics.csv`
- `./.exp/netting-e2e/results/netting_intent_metrics.csv`

The configured workload converts cross-shard `NormalTx` values into payment
intents at the supervisor. Same-shard transactions still use the ordinary local
transaction path. The supervisor tracks cross-shard intent completion through
netting progress messages so the run terminates only after reserved, settled,
and fallback intent work has been observed.
