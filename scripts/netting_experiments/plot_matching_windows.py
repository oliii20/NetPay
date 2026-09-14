#!/usr/bin/env python3
"""Plot per-window netting matching rates for one experiment run."""

from __future__ import annotations

import argparse
import csv
import math
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path

from plot_experiments import (
    DEFAULT_SUMMARY,
    PALETTE,
    PNGFigure,
    f,
    read_rows,
    resolve_summary_path,
    timestamped_path,
)


BATCH_METRICS_FILE = "netting_batch_metrics.csv"
METRICS = {
    "intent": ("MatchedIntentRatio", "Intent matching rate"),
    "value": ("MatchedValueRatio", "Value matching rate"),
}


@dataclass(frozen=True)
class WindowRate:
    window_id: int
    rate: float
    intent_count: float


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--run-dir",
        type=Path,
        help="experiment run directory, e.g. .exp/netting-paper/runs/<run_id>",
    )
    parser.add_argument(
        "--batch-metrics",
        type=Path,
        help="direct path to netting_batch_metrics.csv; overrides --run-dir and --summary",
    )
    parser.add_argument(
        "--summary",
        type=Path,
        default=DEFAULT_SUMMARY,
        help="summary.csv used to locate --run-id when --run-dir is omitted",
    )
    parser.add_argument(
        "--run-id",
        default="",
        help="run_id from summary.csv; defaults to the last netting run with batches",
    )
    parser.add_argument("--metric", choices=sorted(METRICS), default="intent")
    parser.add_argument("--out", type=Path, default=Path("figures/netting"))
    parser.add_argument(
        "--no-timestamp",
        action="store_true",
        help="write a canonical file name and overwrite it",
    )
    args = parser.parse_args()

    batch_path, source_label = resolve_batch_metrics(args.batch_metrics, args.run_dir, args.summary, args.run_id)
    rows = read_rows(batch_path)
    metric_key, metric_label = METRICS[args.metric]
    windows = window_rates(rows, metric_key)
    if not windows:
        raise SystemExit(f"no usable {metric_key} rows found in {batch_path}")

    average = weighted_average(windows)
    output_path = resolve_output_path(args.out, source_label, args.metric, "" if args.no_timestamp else timestamp())
    output_path.parent.mkdir(parents=True, exist_ok=True)
    plot_matching_windows(windows, output_path, metric_label, source_label, average)

    print(f"batch metrics: {batch_path}")
    print(f"windows: {len(windows)}")
    print(f"weighted average {args.metric} matching rate: {average:.6f}")
    print(f"figure: {output_path}")
    return 0


def resolve_batch_metrics(
    batch_metrics: Path | None,
    run_dir: Path | None,
    summary: Path,
    run_id: str,
) -> tuple[Path, str]:
    if batch_metrics is not None:
        return batch_metrics, clean_label(batch_metrics.parent.parent.name or batch_metrics.stem)
    if run_dir is not None:
        return run_dir / "results" / BATCH_METRICS_FILE, clean_label(run_dir.name)

    summary_path = resolve_summary_path(summary)
    rows = read_rows(summary_path)
    selected = select_run(rows, run_id)
    runs_dir = summary_path.resolve().parent / "runs"
    selected_id = selected.get("run_id", "")
    return runs_dir / selected_id / "results" / BATCH_METRICS_FILE, clean_label(selected_id)


def select_run(rows: list[dict[str, str]], run_id: str) -> dict[str, str]:
    candidates = [
        row
        for row in rows
        if row.get("method", "").startswith("netting")
        and (not run_id or row.get("run_id") == run_id)
        and f(row, "batch_count") > 0
    ]
    if not candidates:
        suffix = f" for run_id={run_id}" if run_id else ""
        raise SystemExit(f"no netting run with batch_count > 0 found in summary{suffix}")
    return candidates[-1]


def window_rates(rows: list[dict[str, str]], metric_key: str) -> list[WindowRate]:
    windows: list[WindowRate] = []
    for idx, row in enumerate(rows, start=1):
        value = f(row, metric_key)
        if not math.isfinite(value):
            continue
        window_id = int(f(row, "WindowID")) if f(row, "WindowID") > 0 else idx
        windows.append(WindowRate(window_id=window_id, rate=value, intent_count=f(row, "IntentCount")))
    windows.sort(key=lambda item: item.window_id)
    return windows


def weighted_average(windows: list[WindowRate]) -> float:
    total_weight = sum(max(0.0, item.intent_count) for item in windows)
    if total_weight <= 0:
        return sum(item.rate for item in windows) / len(windows)
    return sum(item.rate * max(0.0, item.intent_count) for item in windows) / total_weight


def resolve_output_path(out: Path, source_label: str, metric: str, timestamp_part: str) -> Path:
    if out.suffix.lower() == ".png":
        return timestamped_path(out, timestamp_part)
    filename = f"fig_window_matching_{source_label}_{metric}.png"
    return timestamped_path(out / filename, timestamp_part)


def plot_matching_windows(
    windows: list[WindowRate],
    path: Path,
    metric_label: str,
    source_label: str,
    average: float,
) -> None:
    points = [(float(item.window_id), item.rate, 0.0) for item in windows]
    values = [item.rate for item in windows]
    chart = PNGFigure(1180, 500)
    chart.title("Matching rate by window", 22, 30)
    chart.draw_text(f"RUN: {source_label}", 22, 46, scale=1, color=PALETTE["gray"])
    chart.draw_text(f"WEIGHTED AVG: {average:.3f}", 925, 46, scale=1, color=PALETTE["red"])
    chart.line_panel(
        70,
        86,
        740,
        310,
        {"Window": points},
        "Window ID",
        metric_label,
        y_max=1.0,
        reference_y=average,
        reference_label="Weighted avg",
        x_tick_values=window_ticks(windows),
    )
    chart.box_panel(
        905,
        86,
        205,
        310,
        values,
        metric_label,
        "All windows",
        y_max=1.0,
        reference_y=average,
    )
    chart.draw_text(f"N={len(windows)}", 996, 438, scale=1, anchor="center", color=PALETTE["gray"])
    chart.save(path)


def clean_label(value: str) -> str:
    cleaned = "".join(ch if ch.isalnum() or ch in "._-" else "_" for ch in value)
    return cleaned.strip("._-") or "run"


def window_ticks(windows: list[WindowRate]) -> list[float]:
    ids = [item.window_id for item in windows]
    if len(ids) <= 6:
        return [float(item) for item in ids]
    first, last = min(ids), max(ids)
    if first == last:
        return [float(first)]
    raw = [first, first + (last - first) // 4, first + (last - first) // 2, first + 3 * (last - first) // 4, last]
    ticks: list[int] = []
    for value in raw:
        if value not in ticks:
            ticks.append(value)
    return [float(item) for item in ticks]


def timestamp() -> str:
    return datetime.now().strftime("%Y%m%d-%H%M%S")


if __name__ == "__main__":
    raise SystemExit(main())
