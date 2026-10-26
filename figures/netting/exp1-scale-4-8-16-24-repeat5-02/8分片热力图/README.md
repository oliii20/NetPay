# Shard-pair matching-rate heatmap

This folder is self-contained. It does not require the original experiment
sessions, logs, databases, or the BlockEmulator-X repository.

## Files

- `plot_heatmap.py`: standalone plotting script.
- `matching_rate_mean.csv`: five-repeat mean and standard deviation for every
  unordered shard pair. This is the direct input to the plotting script.
- `matching_rate_per_repeat.csv`: counts and rates for every repeat, retained
  for verification and alternative aggregation.
- `reference_heatmap.pdf` and `reference_heatmap.png`: reference outputs.
- `requirements.txt`: Python dependencies.

The matching rate for an unordered shard pair is:

```text
fully matched intents / all intents
```

The rate is calculated independently for each repeat and then averaged across
the five repeats. The matrix is symmetric because the two directions are
combined into one unordered shard pair.

## Reproduce the reference figure

```bash
python3 -m pip install -r requirements.txt
python3 plot_heatmap.py
```

This generates:

- `shard8_matching_rate_heatmap.pdf`
- `shard8_matching_rate_heatmap.png`

## Common adjustments

Use a standard, non-desaturated colormap:

```bash
python3 plot_heatmap.py --cmap viridis --cmap-start 0 --cmap-end 1 --neutral-mix 0
```

Change the color range and font size:

```bash
python3 plot_heatmap.py --vmin 0.8 --vmax 1.0 --font-size 11
```

Add values to the cells:

```bash
python3 plot_heatmap.py --annotate
```

Change the figure dimensions:

```bash
python3 plot_heatmap.py --width 4.6 --height 3.6
```

Run `python3 plot_heatmap.py --help` for all options.
