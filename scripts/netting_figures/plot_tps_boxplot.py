#!/usr/bin/env python3
"""Figure 1: repeated-run TPS boxplot."""

from __future__ import annotations

import argparse
from pathlib import Path

from common import DEFAULT_OUT, DEFAULT_SUMMARY, metric_groups, plot_metric_boxplot, read_rows, resolve_timestamp, timestamped_stem


def main() -> None:
    parser = argparse.ArgumentParser(description="Plot TPS boxplot for repeated hotspot-hybrid experiments.")
    parser.add_argument("--summary", type=Path, default=DEFAULT_SUMMARY, help="Path to summary.csv.")
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT, help="Output directory.")
    parser.add_argument("--timestamp", default="", help="Timestamp suffix. Default: current time; use 'none' to disable.")
    args = parser.parse_args()

    rows = read_rows(args.summary)
    groups = metric_groups(rows, "throughput_tps")
    if not groups:
        raise SystemExit("no throughput_tps values found")

    stem = timestamped_stem(args.out / "fig_tps_boxplot", resolve_timestamp(args.timestamp))
    outputs = plot_metric_boxplot(groups, "Throughput (tx/s)", stem)

    print(f"summary: {args.summary}")
    print("figures:")
    for path in outputs:
        print(f"  {path}")


if __name__ == "__main__":
    main()
