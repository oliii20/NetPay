# Online aggregation routing

This runner generates an isolated config/network table and starts N x 4 ordinary
nodes, four Beacon replicas, and a Supervisor. It refuses to overwrite an output
directory, and cleans up only the processes it starts. Dependencies: Python with
PyYAML, Go 1.25.7, and the existing transaction CSV in data/.

24-shard experiment, 300000 originals, 600 tx/s per shard:

```bash
/opt/anaconda3/bin/python3 experiments/adaptive_aggregation/run.py \
  --shards 24 --tx-number 300000 --per-shard-rate 600 \
  --output exp/adaptive-routing-24-v1
```

4x4 integration test which deliberately makes small batches and creates backlog:

```bash
/opt/anaconda3/bin/python3 experiments/adaptive_aggregation/run.py \
  --shards 4 --tx-number 4000 --per-shard-rate 100 --pressure-test
```

The normal experiment starts with **all** destinations aggregated. There is no
offline destination ranking or static 75% allowlist. The 24 -> 18 transition must
be caused by an actual frozen queue waiting more than 2 seconds. Lack of such a
transition is a valid observation, not a failed experiment. Further reductions
or recovery depend on live queue behavior. The pressure test instead uses a
100 ms high threshold, 25 ms low threshold, 2 s cooldown and tiny batch-size 20;
it validates functional switching and is **not** a scaling-performance benchmark.

The generated YAML contains the control parameters:

```yaml
aggregation:
  enabled: true
  metrics_enabled: true
  source_aggregate_abstracts: true
  # Do not set destination_shards in adaptive mode.
  adaptive_routing:
    enabled: true
    freeze_high_ms: 2000
    freeze_low_ms: 500
    observation_window_ms: 10000
    cooldown_ms: 10000
    reduction_percent: 25
    min_destination_percent: 25
```

For an existing config the other required aggregation settings (store path,
window duration, chunk size) still apply. Adaptive mode is currently static_relay
only. It is not compatible with legacy injection cutoffs or ablation switches.

Every completed run is audited for:

- exactly TX_NUMBER distinct original hashes across ordinary and aggregated completions;
- zero source CrossShardPayment transaction entries;
- committed Relay1 count = committed Relay2 count + aggregated original count;
- zero ERROR/WARN log records;
- for pressure tests, at least one policy change and both delivery routes.

Outputs include config.yaml, ip_table.json, block_record/, process_logs/, logs/,
results/aggregation_routing_policy.csv and validation.json. The routing CSV keeps
the online observed count vectors, decision reason and freeze-wait measurement.

## Verified integration run

[validation-4x4.json](validation-4x4.json) preserves the validation report from
the local `.exp/adaptive-routing-20261027-210427/` run:

- 4000/4000 unique original transactions completed, zero duplicates;
- 1453 aggregated originals and 2547 ordinary Relay/inner-shard originals;
- 2965 source Relay1 = 1512 target Relay2 + 1453 aggregated originals;
- zero CrossShardPayment entries and zero WARN/ERROR records;
- three overload-driven policies: all four destinations -> three -> two -> one.

This is a functional 4x4 validation. The 24-shard 300000-transaction experiment
has a runnable configuration generator but has not been run as part of this change.
The earlier static 24/75% figures remain historical ablation results and must not
be presented as measurements of this new adaptive controller.
