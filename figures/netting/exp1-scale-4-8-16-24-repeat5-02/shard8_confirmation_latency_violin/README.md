# 8-shard confirmation-time violin figure

This folder is self-contained. The plotting script reads the complete
transaction-level CSV in this folder; access to the original experiment
session is not required.

## Files

- `confirmation_time_per_transaction.csv`: long-format table containing all
  300,000 transaction IDs and per-transaction mean confirmation times for each
  method (1,200,000 rows total). Each value is the mean of that same transaction
  in `repeat_01` and `repeat_02`. Netting intent IDs are kept in their native
  namespace rather than being falsely joined to baseline transaction hashes.
- `confirmation_time_density.csv`: cached smoothed distribution density
  calculated from the 300,000 transaction-level means.
- `confirmation_time_stats.csv`: exact transaction count, mean, p50, p95, and
  maximum confirmation time, plus exact quartiles and 1.5×IQR whiskers.
- `confirmation_time_scatter.csv`: cached fixed-seed representative sample;
  the standalone script regenerates this sample from the complete CSV.
- `source_runs.csv`: experiment repeats used to calculate the distributions.
- `plot_confirmation_time_violin.py`: standalone plotting script.
- `reference.*`: the current reference rendering in PDF, PNG, and SVG formats.

The standalone script calculates the density, scatter sample, quartiles,
1.5×IQR whiskers, and p95 directly from `confirmation_time_per_transaction.csv`.
The density uses a Gaussian smoothing bandwidth of 0.8 seconds.

## Replot

```bash
python3 -m pip install -r requirements.txt
python3 plot_confirmation_time_violin.py
```

Edit the constants in the `STYLE` section at the top of the script to change
colors, fonts, figure dimensions, violin width, scatter size/transparency,
boxplot styling, annotation position, or output filename. The violin density
and boxplot statistics represent all 300,000 distinct transactions per method.
The scatter layer uses 500 real values sampled without replacement from those
300,000 points to keep the figure readable. The script writes PDF, PNG, and
SVG files into this folder.
