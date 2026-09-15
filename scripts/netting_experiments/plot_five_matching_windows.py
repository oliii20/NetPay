#!/usr/bin/env python3
"""Plot matching-rate curves for five netting runs from a summary file."""

from __future__ import annotations

import argparse
import csv
import sys
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from plot_experiments import DEFAULT_SUMMARY, PALETTE, PNGFigure, f, read_rows, resolve_summary_path
from plot_matching_windows import BATCH_METRICS_FILE, METRICS, WindowRate, weighted_average, window_rates, window_ticks


DEFAULT_COUNT = 5


@dataclass(frozen=True)
class SelectedRun:
    run_id: str
    label: str
    batch_metrics: Path


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--summary", type=Path, default=DEFAULT_SUMMARY)
    parser.add_argument("--out", type=Path, default=Path("figures/netting/matching_windows"))
    parser.add_argument("--metric", choices=sorted(METRICS), default="intent")
    parser.add_argument("--count", type=positive_int, default=DEFAULT_COUNT)
    parser.add_argument(
        "--run-ids",
        default="",
        help="optional comma-separated run_id list; otherwise the first --count netting runs are used",
    )
    parser.add_argument("--no-timestamp", action="store_true")
    args = parser.parse_args()

    summary_path = resolve_summary_path(args.summary)
    selected = select_runs(summary_path, args.count, parse_run_ids(args.run_ids))
    metric_key, metric_label = METRICS[args.metric]
    timestamp_part = "" if args.no_timestamp else datetime.now().strftime("%Y%m%d-%H%M%S")

    out_dir = args.out
    out_dir.mkdir(parents=True, exist_ok=True)
    generated: list[Path] = []
    for idx, selected_run in enumerate(selected, start=1):
        rows = read_rows(selected_run.batch_metrics)
        windows = window_rates(rows, metric_key)
        if not windows:
            print(f"skip {selected_run.run_id}: no usable {metric_key} rows", file=sys.stderr)
            continue
        average = weighted_average(windows)
        output_path = output_file(out_dir, idx, selected_run.label, args.metric, timestamp_part)
        plot_window_line(windows, output_path, metric_label, selected_run.label, average)
        generated.append(output_path)

    if not generated:
        raise SystemExit(f"no matching-window figures generated from {summary_path}")
    print(f"summary: {summary_path}")
    print("figures:")
    for path in generated:
        print(f"  {path}")
    return 0


def positive_int(raw: str) -> int:
    value = int(raw)
    if value <= 0:
        raise argparse.ArgumentTypeError("must be a positive integer")
    return value


def parse_run_ids(raw: str) -> list[str]:
    return [part.strip() for part in raw.split(",") if part.strip()]


def select_runs(summary_path: Path, count: int, run_ids: list[str] | None = None) -> list[SelectedRun]:
    rows = read_rows(summary_path)
    wanted = set(run_ids or [])
    candidates = [
        row
        for row in rows
        if row.get("method", "").startswith("netting")
        and f(row, "batch_count") > 0
        and (not wanted or row.get("run_id", "") in wanted)
    ]
    if not candidates:
        suffix = f" for {sorted(wanted)}" if wanted else ""
        raise SystemExit(f"no netting runs with batch_count > 0 found in {summary_path}{suffix}")
    if wanted:
        found = {row.get("run_id", "") for row in candidates}
        missing = wanted - found
        if missing:
            raise SystemExit(f"run_id not found or has no batches: {sorted(missing)}")
    return [
        SelectedRun(
            run_id=row.get("run_id", ""),
            label=run_label(row, idx),
            batch_metrics=batch_metrics_path(summary_path, row),
        )
        for idx, row in enumerate(candidates[:count], start=1)
    ]


def batch_metrics_path(summary_path: Path, row: dict[str, str]) -> Path:
    run_id = row.get("run_id", "")
    repeat = row.get("repeat", "")
    runs_dir = summary_path.resolve().parent / "runs"
    if repeat:
        path = runs_dir / f"repeat_{int(f(row, 'repeat')):02d}" / run_id / "results" / BATCH_METRICS_FILE
    else:
        path = runs_dir / run_id / "results" / BATCH_METRICS_FILE
    if not path.exists():
        raise SystemExit(f"batch metrics not found for {run_id}: {path}")
    return path


def run_label(row: dict[str, str], idx: int) -> str:
    repeat = row.get("repeat", "")
    seed = row.get("seed", "")
    if repeat:
        return f"repeat_{int(f(row, 'repeat')):02d}"
    if seed:
        return f"run_{idx:02d}_seed{seed}"
    return f"run_{idx:02d}"


def output_file(out_dir: Path, idx: int, label: str, metric: str, timestamp_part: str) -> Path:
    safe_label = clean_label(label)
    stem = f"fig_window_matching_{idx:02d}_{safe_label}_{metric}"
    if timestamp_part:
        stem = f"{stem}_{timestamp_part}"
    return out_dir / f"{stem}.png"


def clean_label(value: str) -> str:
    cleaned = "".join(ch if ch.isalnum() or ch in "._-" else "_" for ch in value)
    return cleaned.strip("._-") or "run"


def plot_window_line(
    windows: list[WindowRate],
    path: Path,
    metric_label: str,
    source_label: str,
    average: float,
) -> None:
    points = [(float(item.window_id), item.rate, 0.0) for item in windows]
    chart = PNGFigure(980, 430)
    chart.title("Matching rate by window", 22, 30)
    chart.draw_text(f"RUN: {source_label}", 22, 46, scale=1, color=PALETTE["gray"])
    chart.draw_text(f"WEIGHTED AVG: {average:.3f}", 725, 46, scale=1, color=PALETTE["red"])
    chart.line_panel(
        70,
        86,
        820,
        270,
        {"Window": points},
        "Window ID",
        metric_label,
        y_max=1.0,
        reference_y=average,
        reference_label="Weighted avg",
        x_tick_values=window_ticks(windows),
    )
    chart.draw_text(f"N={len(windows)}", 494, 392, scale=1, anchor="center", color=PALETTE["gray"])
    chart.save(path)


if __name__ == "__main__":
    raise SystemExit(main())
