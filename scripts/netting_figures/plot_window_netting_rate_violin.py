#!/usr/bin/env python3
"""Figure 4: per-window full netting-rate violin for one netting experiment."""

from __future__ import annotations

import argparse
from pathlib import Path

from common import (
    DEFAULT_OUT,
    DEFAULT_SUMMARY,
    collect_matching_rates,
    parse_run_ids,
    plot_violin,
    positive_int,
    read_rows,
    resolve_timestamp,
    select_netting_run_ids,
    timestamped_stem,
    write_samples_csv,
)


def main() -> None:
    parser = argparse.ArgumentParser(description="Plot per-window full netting-rate violin for one netting repeat/run.")
    parser.add_argument("--summary", type=Path, default=DEFAULT_SUMMARY, help="Path to summary.csv.")
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT, help="Output directory.")
    parser.add_argument("--repeat", type=positive_int, default=1, help="Repeat index to plot. Default: 1.")
    parser.add_argument("--run-ids", default="", help="Comma-separated netting run_id list. Overrides --repeat when set.")
    parser.add_argument("--timestamp", default="", help="Timestamp suffix. Default: current time; use 'none' to disable.")
    args = parser.parse_args()

    rows = read_rows(args.summary)
    run_ids = select_netting_run_ids(rows, None if args.run_ids else args.repeat, parse_run_ids(args.run_ids))
    samples = collect_matching_rates(args.summary, "intent", "window", len(run_ids), run_ids)
    if not samples:
        raise SystemExit("no per-window netting-rate samples found")

    label = "custom" if args.run_ids else f"repeat{args.repeat:02d}"
    stem = timestamped_stem(args.out / f"fig_window_netting_rate_violin_{label}", resolve_timestamp(args.timestamp))
    outputs = plot_violin(samples, "Netting rate", stem)
    samples_path = stem.with_name(f"{stem.name}_samples.csv")
    write_samples_csv(samples, samples_path)

    print(f"summary: {args.summary}")
    print("figures:")
    for path in outputs:
        print(f"  {path}")
    print(f"data: {samples_path}")


if __name__ == "__main__":
    main()
