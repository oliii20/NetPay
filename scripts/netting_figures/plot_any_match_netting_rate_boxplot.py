#!/usr/bin/env python3
"""Any matched CTX count ratio boxplot across repeated netting runs."""

from __future__ import annotations

import argparse
from pathlib import Path

from common import (
    DEFAULT_OUT,
    DEFAULT_SUMMARY,
    plot_metric_boxplot,
    positive_int,
    read_rows,
    resolve_timestamp,
    select_rows_by_repeat,
    timestamped_stem,
    total_matched_netting_rate_values,
)


def main() -> None:
    parser = argparse.ArgumentParser(description="Plot any matched CTX ratio boxplot across repeated netting runs.")
    parser.add_argument("--summary", type=Path, default=DEFAULT_SUMMARY, help="Path to summary.csv.")
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT, help="Output directory.")
    parser.add_argument("--repeat", type=positive_int, default=None, help="Optional repeat index to filter.")
    parser.add_argument("--timestamp", default="", help="Timestamp suffix. Default: current time; use 'none' to disable.")
    args = parser.parse_args()

    rows = select_rows_by_repeat(read_rows(args.summary), args.repeat)
    values = total_matched_netting_rate_values(args.summary, rows)
    stem = timestamped_stem(args.out / "fig_any_match_netting_rate_boxplot", resolve_timestamp(args.timestamp))
    outputs = plot_metric_boxplot([("Netting", values)], "Any-match netting rate", stem)

    print(f"summary: {args.summary}")
    print("figures:")
    for path in outputs:
        print(f"  {path}")
    print(f"samples: {len(values)}")


if __name__ == "__main__":
    main()
