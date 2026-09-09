# Netting paper experiments

This document describes the seven paper-facing experiments implemented in
`scripts/netting_experiments`.

Default dataset:

```text
data/selectedTxs_300K.csv
```

The dataset is not copied into the repository. Put `selectedTxs_300K.csv` under
`data/`, or pass a local path with `--dataset`. The runner derives per-run CSV
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

Windows `cmd.exe` example:

```bat
set "GOCACHE=%CD%\.exp\gocache" && python scripts\netting_experiments\run_experiments.py --profile full --seeds 1 --experiments exp1 --tx-number 50000 --tx-speed 2000 --timeout 1000 --progress-interval 20 --dataset "D:\path\to\selectedTxs_300K.csv"
```

Before running on Windows, make sure `go version` works in the same `cmd.exe`
window. If Go is installed but not on `PATH`, pass the compiler explicitly:

```bat
set "GOCACHE=%CD%\.exp\gocache" && python scripts\netting_experiments\run_experiments.py --go "C:\Program Files\Go\bin\go.exe" --profile full --seeds 1 --experiments exp1 --tx-number 3000 --tx-speed 500 --timeout 600 --progress-interval 20
```

The runner writes built executables under `.exp/netting-paper/bin/`. On Windows
it automatically uses `.exe` names, while Unix-like systems keep extensionless
binary names. It also builds for Go's native `GOHOSTOS/GOHOSTARCH` target, so
stale `GOOS` or `GOARCH` environment variables do not accidentally produce a
non-Windows executable named `*.exe`.

Use `--tx-number` and `--tx-speed` to override the workload size and injection
rate chosen by `--profile`.

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

The plotting script writes PNG files under `figures/netting/`:

- `fig1_baseline_comparison.png`
- `fig2_netting_balance.png`
- `fig3_batch_size.png`
- `fig4_window_duration.png`
- `fig5_matcher_ablation.png`
- `fig6_scale.png`
- `fig7_async_latency.png`

## Metric notes

Netting metrics come from `netting_batch_metrics.csv` and
`netting_intent_metrics.csv`. Relay and broker baselines use their existing
brief/detail CSVs. `cross_messages_per_tx` is a protocol-level estimate derived
from each mechanism's cross-shard phases; it intentionally excludes TCP/IP,
gRPC, and protobuf framing overhead. Beacon byte metrics are protocol payload
bytes collected by the netting metric path.
