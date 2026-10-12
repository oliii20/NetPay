#!/usr/bin/env python3
"""Figure 3: per-transaction confirmation latency violin for one experiment."""

from __future__ import annotations

import argparse
from pathlib import Path

from common import (
    DEFAULT_OUT,
    DEFAULT_SUMMARY,
    parse_run_ids,
    plot_tx_latency_violin,
    positive_int,
    read_rows,
    resolve_timestamp,
    select_rows_by_repeat,
    timestamped_stem,
    write_tx_latency_stats,
)


def main() -> None:
    parser = argparse.ArgumentParser(description="Plot smooth per-tx confirmation latency violin for one repeat/run.")
    parser.add_argument("--summary", type=Path, default=DEFAULT_SUMMARY, help="Path to summary.csv.")
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT, help="Output directory.")
    parser.add_argument("--repeat", type=positive_int, default=1, help="Repeat index to plot. Default: 1.")
    parser.add_argument("--run-ids", default="", help="Comma-separated run_id list. Overrides --repeat when set.")
    parser.add_argument("--bins", type=positive_int, default=240, help="Histogram bins used for smooth violin.")
    parser.add_argument("--timestamp", default="", help="Timestamp suffix. Default: current time; use 'none' to disable.")
    args = parser.parse_args()

    rows = read_rows(args.summary)
    run_ids = parse_run_ids(args.run_ids)
    if run_ids:
        wanted = set(run_ids)
        selected = [row for row in rows if row.get("run_id", "") in wanted]
        missing = wanted - {row.get("run_id", "") for row in selected}
        if missing:
            raise SystemExit(f"run_id not found: {sorted(missing)}")
    else:
        selected = select_rows_by_repeat(rows, args.repeat)

    label = "custom" if run_ids else f"repeat{args.repeat:02d}"
    stem = timestamped_stem(args.out / f"fig_tx_confirmation_latency_violin_{label}", resolve_timestamp(args.timestamp))
    svg_path = plot_tx_latency_violin(args.summary, selected, stem, args.bins)
    stats_path = stem.with_name(f"{stem.name}_stats.csv")
    write_tx_latency_stats(selected, args.summary, stats_path, args.bins)

    print(f"summary: {args.summary}")
    print("figures:")
    print(f"  {svg_path}")
    print(f"data: {stats_path}")


if __name__ == "__main__":
    main()
