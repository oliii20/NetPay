# Shard-pair heatmaps

This folder is self-contained and does not require the original experiment
sessions or the BlockEmulator-X repository.

## Included figures

1. Five-repeat mean full-matching rate for each unordered shard pair.
2. Five-repeat mean transaction/intent count for each unordered shard pair.

The matrices are symmetric because both directions are combined into one
unordered shard pair. Diagonal cells are not applicable and are shown in gray.

## Files

- `plot_pair_heatmaps.py`: standalone script that draws both heatmaps.
- `matching_rate_mean.csv`: mean matching rate and standard deviation.
- `matching_rate_per_repeat.csv`: full-match counts, total intent counts, and
  rates for every repeat.
- `transaction_count_mean.csv`: mean and standard deviation of transaction
  counts across five repeats.
- `reference_*.pdf/png`: reference outputs.
- `requirements.txt`: Python dependencies.

## Run

```bash
python3 -m pip install -r requirements.txt
python3 plot_pair_heatmaps.py
```

The script generates PDF and PNG versions of both heatmaps in this folder.

## Adjust appearance

Use a standard, non-desaturated colormap:

```bash
python3 plot_pair_heatmaps.py --cmap viridis --cmap-start 0 --cmap-end 1 --neutral-mix 0
```

Change color ranges and font size:

```bash
python3 plot_pair_heatmaps.py \
  --matching-vmin 0.8 --matching-vmax 1.0 \
  --count-vmin 6000 --count-vmax 12000 \
  --font-size 11
```

Add values inside cells:

```bash
python3 plot_pair_heatmaps.py --annotate
```

Run `python3 plot_pair_heatmaps.py --help` for all options.
