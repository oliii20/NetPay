# 8-shard stage-latency figure package

This folder is self-contained and can be sent directly to a coauthor. The plot
script reads the already-computed CSV; it does not require BlockEmulator-X or
the original experiment logs.

## Files

- `stage_latency_shard8.csv`: exact values used by the stacked bar chart.
- `plot_stage_latency.py`: independent Matplotlib plotting script.
- `clpa_reconfiguration_details.csv`: optional diagnostic data; it is not used
  or displayed by the transaction-stage figure.
- `requirements.txt`: Python dependency.

## Run

```bash
python3 -m venv .venv
source .venv/bin/activate
python3 -m pip install -r requirements.txt
python3 plot_stage_latency.py
```

The script writes PNG, vector PDF, and vector SVG files to `output/`.

To change colors, fonts, hatches, bar width, DPI, or figure dimensions, edit
the clearly marked `STYLE` section near the top of `plot_stage_latency.py`.

## Statistical scope

The source session is `exp1-scale-4-8-16-24-repeat5-02`, shard count 8. Each
row is first averaged over all 300,000 completed original transactions within
a repeat, then repeat 01 and repeat 02 are averaged with equal weight.

The plotted stages are mutually exclusive, and their sum equals `total_s`.

## Stage definitions

### Monoxide

- **Source:** transaction creation to Relay1 commit. For an intra-shard
  transaction, creation to final commit.
- **Target:** Relay1 commit to Relay2/final commit, including relay transfer and
  target-shard consensus.

### BrokerChain

- **Source:** transaction creation to Broker1 commit.
- **Coord.:** Broker1 commit to Broker2 creation.
- **Target:** Broker2 creation to Broker2/final commit.

### CLPA

- **Source:** transaction creation to Relay1 commit. For an intra-shard
  transaction, creation to final commit.
- **Target:** Relay1 commit to final commit, including relay transfer and
  target-shard consensus.

Source and Target retain their full protocol durations. CLPA reconfiguration
time is neither subtracted from them nor displayed as a separate stage.

### Netting

- **Source:** transaction creation to reservation commit. Intra-shard
  transactions use creation to final commit.
- **Matching:** window-open duration + matching time + Merkle-build time +
  beacon consensus latency.
- **Settlement:** for intents with `UsedFallback=false`, end-to-end latency
  minus Source minus Matching; zero for fallback intents.
- **Fallback:** for intents with `UsedFallback=true`, end-to-end latency minus
  Source minus Matching; zero for normally settled intents.

Settlement and Fallback are additive contributions to the overall average:
each is summed over its corresponding intents and divided by the number of all
completed original transactions. They are not conditional per-path averages.

## Plotted means (seconds)

| Method | Source | Matching | Coordination | Target | Settlement | Fallback | Total |
|---|---:|---:|---:|---:|---:|---:|---:|
| Monoxide | 14.113 | 0 | 0 | 14.001 | 0 | 0 | 28.114 |
| BrokerChain | 14.190 | 0 | 0.402 | 14.377 | 0 | 0 | 28.970 |
| CLPA | 15.211 | 0 | 0 | 14.275 | 0 | 0 | 29.485 |
| Netting | 3.748 | 2.503 | 0 | 0 | 2.377 | 0.066 | 8.694 |
