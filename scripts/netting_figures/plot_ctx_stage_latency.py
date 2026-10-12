#!/usr/bin/env python3
"""Figure 6: CTX stage latency breakdown for one experiment."""

from __future__ import annotations

import argparse
from pathlib import Path

from common import (
    DEFAULT_OUT,
    DEFAULT_SUMMARY,
    parse_run_ids,
    plot_stage_latency,
    positive_int,
    read_rows,
    resolve_timestamp,
    select_rows_by_repeat,
    timestamped_path,
)


def main() -> None:
    parser = argparse.ArgumentParser(description="Plot CTX stage latency breakdown for one repeat/run.")
    parser.add_argument("--summary", type=Path, default=DEFAULT_SUMMARY, help="Path to summary.csv.")
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT, help="Output directory.")
    parser.add_argument("--repeat", type=positive_int, default=1, help="Repeat index to plot. Default: 1.")
    parser.add_argument("--run-ids", default="", help="Comma-separated run_id list. Overrides --repeat when set.")
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
        label = "custom"
    else:
        selected = select_rows_by_repeat(rows, args.repeat)
        label = f"repeat{args.repeat:02d}"

    output = timestamped_path(args.out / f"fig_ctx_stage_latency_{label}.png", resolve_timestamp(args.timestamp))
    plot_stage_latency(args.summary, selected, output)

    print(f"summary: {args.summary}")
    print("figures:")
    print(f"  {output}")


if __name__ == "__main__":
    main()
