# Pre-Experiment Scripts

This directory contains lightweight data-analysis scripts for pre-experiments.
They do not start BlockEmulator nodes.

## Experiment 1: real-workload bilateral netting space

Question: does a real Ethereum workload contain bidirectional offsetting space?

Default setting:

- Input: `data/25250000to25499999_BlockTransaction.csv`
- Shards: `8`
- Account placement: `partition.DefaultAccountLoc`, the same hash-style placement used by the codebase
- Valid transactions collected: first `300000` after filtering invalid rows
- Invalid rows: unparsable `from`/`to`/`value`, missing `to`, `from == to`, or `value <= 0`
- Injection size: `10000` valid transactions per round
- Block size: `2000` transactions per shard block
- Matcher: `pkg/netting/matcher` in `full` mode
- CTX denominator: positive-value cross-shard transactions
- Full-match CTX: `MatchedAmount == Amount`

Run:

```bash
bash pre_exp_scipts/run_exp1_real_workload_netting.sh
```

Useful overrides:

```bash
bash pre_exp_scipts/run_exp1_real_workload_netting.sh \
  -input data/25250000to25499999_BlockTransaction.csv \
  -output pre_exp_scipts/outputs/exp1_real_workload \
  -shards 8 \
  -inject-size 10000 \
  -block-size 2000 \
  -max-rows 300000 \
  -matcher-mode full
```

FIFO split matcher:

```bash
bash pre_exp_scipts/run_exp1_real_workload_netting.sh \
  -matcher-mode fifo_split \
  -output pre_exp_scipts/outputs/exp1_real_workload_fifo_split
```

`fifo_split` is a weaker local matcher for this script: transactions are
processed in their original valid-workload order, each transaction matches the
earliest unfinished opposite-direction transaction in the same shard pair, and
splitting consumes the earlier opposite transaction first.

Draw the publication-style block-height bar chart with Python:

```bash
bash pre_exp_scipts/plot_exp1_height_matching_rate.sh \
  --input pre_exp_scipts/outputs/exp1_real_workload_fifo_split/height_matching_rate.csv \
  --output pre_exp_scipts/outputs/exp1_real_workload_fifo_split/global_height_matching_rate.png
```

Outputs:

- `height_matching_rate.csv`: global full-match CTX rate per block height
- `pair_height_matching_rate.csv`: full-match CTX rate for every shard pair at every block height
- `pair_aggregate_matching_rate.csv`: aggregate full-match CTX rate by shard pair
- `directional_pair_ctx_counts.csv`: directional CTX counts such as `S0->S1` and `S1->S0`
- `bidirectional_pair_ctx_balance.csv`: side-by-side directional counts and min/max balance ratios for each shard pair
- `run_summary.csv`: run parameters and skipped-row counters, including how many raw CSV rows were scanned to obtain the valid transactions
- `global_height_matching_rate.png`: bar chart for global rate by block height
- `pair_aggregate_matching_rate.png`: bar chart for aggregate rate by shard pair
- `pair_height_matching_rate_heatmap.png`: heatmap for shard-pair rates by height

## Experiment 2

The second pre-experiment script is intentionally not added yet because its
workload, variables, metrics, and plotting target are not specified in the
request. Add it here once the experiment design is fixed.
