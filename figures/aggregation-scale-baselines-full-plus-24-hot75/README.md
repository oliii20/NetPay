# Four-method scaling comparison

TPS is completed original transactions / (last final commit - first original creation),
using logical_tps from each source summary. Latency is mean original-transaction
creation-to-final-commit latency, converted from latency_mean_ms to seconds.
These are finite-workload completion measurements. No epoch-TPS values are mixed in.

All selected runs completed 300000 transactions. Configurations were checked for
the same dataset path, contract filtering, four nodes per shard, block capacity
2000, shard block interval 2000 ms, and injection rate 600 tx/s per shard.
CLPA denotes CLPA+Relay with a 20-second partition interval. BrokerChain uses
static_broker from the hot5-v3 baselines (4/8/16) and the latest recheck (24).
Monoxide uses the relay method with aggregation disabled.

Proposed preserves the previous plot's data: full aggregation for 4/8/16,
hot_destination_75 from the latest recheck for 24. The 75% scope selects 18/24
destination shards; actual cross-shard transaction coverage is 78.8393%.

Monoxide and Proposed each have three repeats at 4/8/16; error bars are sample SD.
Other points have one measurement; their SD fields are blank and no error bars
are drawn. Cross-batch selection and unequal repeat counts are retained explicitly
in selected_runs.csv and comparison_summary.csv. This config audit does not
establish identical executable revisions or runtime conditions across batches.

Reproduce with:
MPLCONFIGDIR=/tmp/codex-mpl-cache /opt/anaconda3/bin/python3 figures/gen_fig_aggregation_scale_baselines.py
