# Eight-shard ablation figure package

This folder is self-contained. It reproduces the compact two-panel figure from
the aggregated CSV and does not import any BlockEmulator-X project modules.

## Contents

- `fig_shard8_ablation_compact_data.csv`: aggregated means and 95% confidence
  intervals used by the figure.
- `plot_fig_shard8_ablation_compact.py`: standalone Matplotlib plotting script.
- `fig_shard8_ablation_compact.png`: 300-DPI preview.
- `fig_shard8_ablation_compact.pdf`: vector figure for the paper.
- `requirements.txt`: the only Python dependency.

## Reproduce the figure

From this directory:

```bash
python -m pip install -r requirements.txt
python plot_fig_shard8_ablation_compact.py
```

Or specify custom paths:

```bash
python plot_fig_shard8_ablation_compact.py \
  --data fig_shard8_ablation_compact_data.csv \
  --out fig_shard8_ablation_compact \
  --dpi 300
```

The script writes both PNG and PDF outputs.

## Restyling

Edit the constants near the top of the script to change:

- `FULL_MATCH_COLOR` and `THROUGHPUT_COLOR` for the palette;
- `FIGSIZE` for the layout;
- `FONT_SIZE` and `configure_matplotlib()` for typography;
- line styles, markers, and legend placement in `draw_panel()` and `plot()`.

`full_match_mean` and `full_match_ci95` are ratios in `[0, 1]`.
Throughput columns are in transactions per second. The current CSV contains one
seed, so both CI columns are zero. A repeated-seed CSV can be substituted without
changing the plotting code.
