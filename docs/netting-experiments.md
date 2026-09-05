# Netting paper experiments

This document describes the seven paper-facing experiments implemented in
`scripts/netting_experiments`.

Default dataset:

```text
/Users/ljn/Desktop/Newidea2026July/block-emulator-main-画预实验的图/selectedTxs_300K.csv
```

The dataset is not copied into the repository. The runner derives per-run CSV
workloads under `.exp/netting-paper/runs/<run_id>/workload.csv`.

## Running experiments

Quick end-to-end check:

```bash
GOCACHE="$PWD/.exp/gocache" python3 scripts/netting_experiments/run_experiments.py --profile smoke
python3 scripts/netting_experiments/plot_experiments.py
```

Pilot pre-experiment:

```bash
GOCACHE="$PWD/.exp/gocache" python3 scripts/netting_experiments/run_experiments.py --profile pilot
python3 scripts/netting_experiments/plot_experiments.py
```

Paper-scale run with repeated seeds:

```bash
GOCACHE="$PWD/.exp/gocache" python3 scripts/netting_experiments/run_experiments.py --profile full --seeds 1,2,3,4,5
python3 scripts/netting_experiments/plot_experiments.py
```

Run only selected groups:

```bash
GOCACHE="$PWD/.exp/gocache" python3 scripts/netting_experiments/run_experiments.py --profile pilot --experiments exp1,exp3,exp5
```

The plotting script requires all seven experiment groups by default. For
debugging a partial run, use:

```bash
python3 scripts/netting_experiments/plot_experiments.py --allow-missing
```

## Experiment groups

1. Baseline comparison: `static_relay`, `static_broker`, and
   `netting_static_relay` on the same selectedTxs trace slice.
2. Netting benefit: synthetic controlled bidirectional traffic with reverse
   ratios `0, 0.25, 0.5, 0.75, 1.0`.
3. BatchSize sensitivity: `10, 20, 50, 100, 200`.
4. MaxWindowDuration sensitivity: `100ms, 500ms, 1s, 2s, 5s`.
5. Matcher ablation: `exact_only`, `best_fit`, and `full`.
   This group uses a larger batch and longer window than the smoke default so
   the measured difference comes from the matching algorithm rather than from
   premature Vector-Cut closure.
6. Scale sensitivity: shard counts `4, 8, 16`, with four nodes per shard.
7. Asynchrony and network latency: direct-RPC latency sweep with heterogeneous
   shard block intervals.

## Output

The runner writes:

- `.exp/netting-paper/summary.csv`: one row per run with aggregated metrics.
- `.exp/netting-paper/runs.jsonl`: run metadata and metrics as JSON Lines.
- `.exp/netting-paper/runs/<run_id>/`: config, IP table, workload, logs, and raw
  metric CSVs for that run.

The plotting script writes vector SVG files under `figures/netting/`:

- `fig1_baseline_comparison.svg`
- `fig2_netting_balance.svg`
- `fig3_batch_size.svg`
- `fig4_window_duration.svg`
- `fig5_matcher_ablation.svg`
- `fig6_scale.svg`
- `fig7_async_latency.svg`

## Metric notes

Netting metrics come from `netting_batch_metrics.csv` and
`netting_intent_metrics.csv`. Relay and broker baselines use their existing
brief/detail CSVs. `cross_messages_per_tx` is a protocol-level estimate derived
from each mechanism's cross-shard phases; it intentionally excludes TCP/IP,
gRPC, and protobuf framing overhead. Beacon byte metrics are protocol payload
bytes collected by the netting metric path.
