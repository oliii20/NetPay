# Repeat-5 eight-shard ablation figure

This self-contained package reproduces the repeat-5 compact ablation figure.
It does not import any BlockEmulator-X project modules.

## Files

- `fig_shard8_ablation_compact_repeat5_data.csv`: means and 95% confidence
  intervals aggregated across five seeds.
- `plot_fig_shard8_ablation_compact_repeat5.py`: standalone plotting code.
- `fig_shard8_ablation_compact_repeat5.png`: 300-DPI preview.
- `fig_shard8_ablation_compact_repeat5.pdf`: vector output for LaTeX.
- `requirements.txt`: Python dependency.

## Reproduce

```bash
python -m pip install -r requirements.txt
python plot_fig_shard8_ablation_compact_repeat5.py
```

Custom paths can be supplied with `--data`, `--out`, and `--dpi`:

```bash
python plot_fig_shard8_ablation_compact_repeat5.py \
  --data fig_shard8_ablation_compact_repeat5_data.csv \
  --out fig_shard8_ablation_compact_repeat5 \
  --dpi 300
```

Colors, fonts, figure size, markers, and line styles are intentionally exposed
near the top of the script and in `configure_style()` / `draw_panel()`.

The figure intentionally omits baseline and nominal-capacity reference lines.

The CSV stores ratios in `[0, 1]`, throughput in transactions per second, and
95% confidence-interval half-widths in the same units as their corresponding
means. Every row has `sample_count=5`.
