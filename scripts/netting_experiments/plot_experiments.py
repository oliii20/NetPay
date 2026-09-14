#!/usr/bin/env python3
"""Create publication-style PNG figures from netting experiment summaries."""

from __future__ import annotations

import argparse
import csv
import math
import statistics
import struct
import zlib
from collections import defaultdict
from datetime import datetime
from pathlib import Path
from typing import Callable, Iterable


PALETTE = {
    "ours": "#C44E52",
    "blue": "#4C72B0",
    "orange": "#DD8452",
    "green": "#55A868",
    "purple": "#8172B3",
    "teal": "#64B5CD",
    "yellow": "#CCB974",
    "red": "#C44E52",
    "gray": "#8C8C8C",
    "light_gray": "#E6E6E6",
    "text": "#222222",
}

STAGE_FIELDS = [
    "stage_source_s",
    "stage_coordination_s",
    "stage_transfer_s",
    "stage_target_s",
    "stage_window_s",
    "stage_match_s",
    "stage_beacon_s",
    "stage_settlement_s",
    "stage_fallback_s",
]
DEFAULT_SUMMARY = Path(".exp/netting-paper/latest/summary.csv")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--summary", type=Path, default=DEFAULT_SUMMARY)
    parser.add_argument("--out", type=Path, default=Path("figures/netting"))
    parser.add_argument(
        "--no-timestamp",
        action="store_true",
        help="write canonical figure names and overwrite existing files",
    )
    parser.add_argument(
        "--allow-missing",
        action="store_true",
        help="deprecated; partial summaries are plotted automatically",
    )
    args = parser.parse_args()

    summary_path = resolve_summary_path(args.summary)
    rows = read_rows(summary_path)
    if not rows:
        raise SystemExit(f"no rows found in {summary_path}")
    rows = enrich_stage_latencies(rows, summary_path)
    args.out.mkdir(parents=True, exist_ok=True)

    generated: list[Path] = []
    skipped: list[str] = []
    seen_missing: set[str] = set()
    timestamp = "" if args.no_timestamp else datetime.now().strftime("%Y%m%d-%H%M%S")
    for experiment, filename, plot in plotters():
        path = timestamped_path(args.out / filename, timestamp)
        if exp_rows(rows, experiment):
            plot(rows, path)
            generated.append(path)
            continue
        if args.no_timestamp:
            cleanup_stale_figure(path)
        if experiment not in seen_missing:
            skipped.append(experiment)
            seen_missing.add(experiment)

    if not generated:
        raise SystemExit(f"no supported experiment groups found in {summary_path}")
    print(f"figures: {args.out}")
    print("generated: " + ", ".join(path.name for path in generated))
    if skipped:
        print("skipped missing groups: " + ", ".join(skipped))
    return 0


def resolve_summary_path(path: Path) -> Path:
    if path.exists():
        return path
    if path == DEFAULT_SUMMARY:
        latest_txt = path.parents[1] / "latest.txt"
        if latest_txt.exists():
            latest_dir = Path(latest_txt.read_text(encoding="utf-8").strip())
            fallback = latest_dir / "summary.csv"
            if fallback.exists():
                return fallback
        legacy = path.parents[1] / "summary.csv"
        if legacy.exists():
            return legacy
    return path


def timestamped_path(path: Path, timestamp: str) -> Path:
    if not timestamp:
        return path
    return path.with_name(f"{path.stem}_{timestamp}{path.suffix}")


def plotters() -> list[tuple[str, str, Callable[[list[dict[str, str]], Path], None]]]:
    return [
        ("exp1_baseline", "fig1_baseline_comparison.png", plot_baseline),
        ("exp1_baseline", "fig1_stage_latency_breakdown.png", plot_baseline_stage_latency),
        ("exp2_balance", "fig2_netting_balance.png", plot_balance),
        ("exp3_batch_size", "fig3_batch_size.png", plot_batch_size),
        ("exp4_window_duration", "fig4_window_duration.png", plot_window),
        ("exp5_matcher_ablation", "fig5_matcher_ablation.png", plot_ablation),
        ("exp6_scale", "fig6_scale.png", plot_scale),
        ("exp7_async_latency", "fig7_async_latency.png", plot_async),
    ]


def cleanup_stale_figure(path: Path) -> None:
    if path.exists():
        path.unlink()


def read_rows(path: Path) -> list[dict[str, str]]:
    with path.open(newline="", encoding="utf-8") as fp:
        return list(csv.DictReader(fp))


def enrich_stage_latencies(rows: list[dict[str, str]], summary_path: Path) -> list[dict[str, str]]:
    runs_dir = summary_path.resolve().parent / "runs"
    for row in rows:
        if all(field in row for field in STAGE_FIELDS):
            continue
        run_id = row.get("run_id", "")
        run_dir = runs_dir / run_id
        if not run_id or not run_dir.exists():
            continue
        row.update(stage_latencies_from_run(row, run_dir))
    return rows


def stage_latencies_from_run(row: dict[str, str], run_dir: Path) -> dict[str, float]:
    method = row.get("method", "")
    results_dir = run_dir / "results"
    if method == "static_relay":
        return relay_stage_latencies(read_rows_if_exists(results_dir / "relay_stats_detail_tx_info.csv"))
    if method == "static_broker":
        return broker_stage_latencies(read_rows_if_exists(results_dir / "broker_stats_detail_tx_info.csv"))
    return netting_stage_latencies(
        read_rows_if_exists(results_dir / "netting_batch_metrics.csv"),
        read_rows_if_exists(results_dir / "netting_intent_metrics.csv"),
    )


def read_rows_if_exists(path: Path) -> list[dict[str, str]]:
    if not path.exists():
        return []
    return read_rows(path)


def exp_rows(rows: list[dict[str, str]], name: str) -> list[dict[str, str]]:
    return [row for row in rows if row.get("experiment") == name]


def f(row: dict[str, str], key: str) -> float:
    try:
        return float(row.get(key, "0") or 0)
    except ValueError:
        return 0.0


def aggregate(
    rows: Iterable[dict[str, str]],
    x_key: str,
    y_key: str,
    series_key: str | None = None,
) -> dict[str, list[tuple[float, float, float]]]:
    grouped: dict[tuple[str, float], list[float]] = defaultdict(list)
    for row in rows:
        series = row.get(series_key, "value") if series_key else "value"
        grouped[(series, f(row, x_key))].append(f(row, y_key))
    result: dict[str, list[tuple[float, float, float]]] = defaultdict(list)
    for (series, x), values in grouped.items():
        mean = statistics.mean(values)
        stderr = statistics.stdev(values) / math.sqrt(len(values)) if len(values) > 1 else 0.0
        result[series].append((x, mean, stderr))
    for values in result.values():
        values.sort(key=lambda item: item[0])
    return result


def empty_stage_latencies() -> dict[str, float]:
    return {field: 0.0 for field in STAGE_FIELDS}


def netting_stage_latencies(batches: list[dict[str, str]], intents: list[dict[str, str]]) -> dict[str, float]:
    completed = [row for row in intents if f(row, "EndToEndLatencyNs") > 0]
    completed_count = max(1, len(completed))
    stages = empty_stage_latencies()
    batch_by_id = {row.get("BatchID", ""): row for row in batches}
    for row in completed:
        batch = batch_by_id.get(row.get("BatchID", ""))
        window_s = f(batch, "WindowOpenDurationNs") / 1e9 if batch else 0.0
        match_s = f(batch, "MatchTimeNs") / 1e9 if batch else 0.0
        beacon_s = f(batch, "BeaconConsensusLatencyNs") / 1e9 if batch else 0.0
        settlement_total_s = f(row, "SettlementLatencyNs") / 1e9
        capital_lock_s = f(row, "CapitalLockDurationNs") / 1e9
        post_source_s = capital_lock_s if capital_lock_s > 0 else settlement_total_s
        stages["stage_source_s"] += f(row, "ReservationLatencyNs") / 1e9
        stages["stage_window_s"] += window_s
        stages["stage_match_s"] += match_s
        stages["stage_beacon_s"] += beacon_s
        if row.get("UsedFallback") == "true":
            fallback_s = f(row, "FallbackLatencyNs") / 1e9
            if fallback_s <= 0:
                fallback_s = max(0.0, post_source_s - window_s - match_s - beacon_s)
            stages["stage_fallback_s"] += fallback_s
        else:
            stages["stage_settlement_s"] += max(0.0, settlement_total_s - window_s - match_s - beacon_s)
    for key in stages:
        stages[key] /= completed_count
    return stages


def relay_stage_latencies(detail: list[dict[str, str]]) -> dict[str, float]:
    stages = empty_stage_latencies()
    cross = [
        row
        for row in detail
        if row.get("Is cross-shard tx or not") == "true"
        and row.get("Relay1 tx commit time")
        and row.get("Relay2 tx commit time")
    ]
    total = max(1, len(cross))
    for row in cross:
        relay2_create_time = row.get("Relay2 tx create time") or row.get("Relay2 block propose time", "")
        stages["stage_source_s"] += parse_duration_from_times(
            row.get("Tx create time", ""), row.get("Relay1 tx commit time", "")
        )
        stages["stage_transfer_s"] += parse_duration_from_times(
            row.get("Relay1 tx commit time", ""), relay2_create_time
        )
        stages["stage_target_s"] += parse_duration_from_times(
            relay2_create_time, row.get("Relay2 tx commit time", "")
        )
    for key in stages:
        stages[key] /= total
    return stages


def broker_stage_latencies(detail: list[dict[str, str]]) -> dict[str, float]:
    stages = empty_stage_latencies()
    cross = [
        row
        for row in detail
        if row.get("Mechanism") == "Broker"
        and row.get("Broker1 tx commit time")
        and row.get("Broker2 tx commit time")
    ]
    fallback = [
        row
        for row in detail
        if row.get("Mechanism") == "FallbackToRelay" and row.get("Tx finally commit time")
    ]
    total = max(1, len(cross) + len(fallback))
    for row in cross:
        stages["stage_source_s"] += parse_duration_from_times(
            row.get("Tx create time", ""), row.get("Broker1 tx commit time", "")
        )
        stages["stage_coordination_s"] += parse_duration_from_times(
            row.get("Broker1 tx commit time", ""), row.get("Broker2 tx create time", "")
        )
        stages["stage_target_s"] += parse_duration_from_times(
            row.get("Broker2 tx create time", ""), row.get("Broker2 tx commit time", "")
        )
    for row in fallback:
        stages["stage_fallback_s"] += parse_duration_from_times(
            row.get("Tx create time", ""), row.get("Tx finally commit time", "")
        )
    for key in stages:
        stages[key] /= total
    return stages


def plot_baseline(rows: list[dict[str, str]], path: Path) -> None:
    data = exp_rows(rows, "exp1_baseline")
    methods = ["static_relay", "static_broker", "netting_static_relay"]
    labels = ["Relay", "Broker", "Netting"]
    panels = [
        ("throughput_tps", "Throughput (tx/s)", False),
        ("avg_latency_s", "Avg. latency (s)", False),
        ("cross_messages_per_tx", "Cross-shard msgs / tx", False),
        ("matched_intent_ratio", "Matching rate", True),
    ]
    chart = PNGFigure(980, 550)
    chart.title("Baseline comparison", 18, 22)
    for idx, (metric, ylabel, is_ratio) in enumerate(panels):
        x = 55 + (idx % 2) * 470
        y = 52 + (idx // 2) * 240
        vals = [mean(f(row, metric) for row in data if row.get("method") == method) for method in methods]
        chart.bar_panel(x, y, 390, 180, labels, vals, ylabel, highlight=2, y_max=1.0 if is_ratio else None)
    chart.save(path)


def plot_baseline_stage_latency(rows: list[dict[str, str]], path: Path) -> None:
    data = exp_rows(rows, "exp1_baseline")
    methods = ["static_relay", "static_broker", "netting_static_relay"]
    labels = ["Relay", "Broker", "Netting"]
    stages = [
        ("stage_source_s", "Source", PALETTE["gray"]),
        ("stage_coordination_s", "Coord", PALETTE["yellow"]),
        ("stage_transfer_s", "Transfer", PALETTE["blue"]),
        ("stage_target_s", "Target", PALETTE["teal"]),
        ("stage_window_s", "Window", PALETTE["orange"]),
        ("stage_match_s", "Match", PALETTE["green"]),
        ("stage_beacon_s", "Beacon", PALETTE["purple"]),
        ("stage_settlement_s", "Settle", PALETTE["red"]),
        ("stage_fallback_s", "Fallback", "#6B6B6B"),
    ]
    stacks = [
        [mean(f(row, field) for row in data if row.get("method") == method) for field, _, _ in stages]
        for method in methods
    ]
    chart = PNGFigure(1240, 460)
    chart.title("Stage latency breakdown", 18, 22)
    chart.stacked_bar_panel(
        70,
        70,
        800,
        280,
        labels,
        stacks,
        [label for _, label, _ in stages],
        [color for _, _, color in stages],
        "Stage latency (s)",
        910,
        80,
    )
    chart.save(path)


def plot_balance(rows: list[dict[str, str]], path: Path) -> None:
    data = exp_rows(rows, "exp2_balance")
    chart = PNGFigure(640, 310)
    chart.title("Netting benefit under bidirectional traffic", 18, 22)
    series = {
        "Matching rate": aggregate(data, "value", "matched_intent_ratio")["value"],
        "Matched value": aggregate(data, "value", "matched_value_ratio")["value"],
        "Fallback value": aggregate(data, "value", "fallback_value_ratio")["value"],
    }
    chart.line_panel(55, 52, 540, 220, series, "Reverse traffic ratio", "Ratio", y_max=1.0)
    chart.save(path)


def plot_batch_size(rows: list[dict[str, str]], path: Path) -> None:
    data = exp_rows(rows, "exp3_batch_size")
    chart = PNGFigure(980, 550)
    chart.title("BatchSize compatibility", 18, 22)
    panels = [
        ("matched_intent_ratio", "Matching rate", True),
        ("throughput_tps", "Throughput (tx/s)"),
        ("beacon_bytes_per_intent", "Beacon bytes / intent"),
        ("avg_latency_s", "Avg. latency (s)"),
    ]
    for idx, panel in enumerate(panels):
        metric, ylabel = panel[0], panel[1]
        is_ratio = len(panel) > 2 and panel[2]
        x = 55 + (idx % 2) * 470
        y = 52 + (idx // 2) * 240
        chart.line_panel(
            x,
            y,
            390,
            180,
            {"Netting": aggregate(data, "value", metric)["value"]},
            "BatchSize",
            ylabel,
            y_max=1.0 if is_ratio else None,
        )
    chart.save(path)


def plot_window(rows: list[dict[str, str]], path: Path) -> None:
    data = exp_rows(rows, "exp4_window_duration")
    chart = PNGFigure(980, 310)
    chart.title("Window duration trade-off", 18, 22)
    chart.line_panel(
        50,
        52,
        260,
        220,
        {"Netting": aggregate(data, "value", "matched_intent_ratio")["value"]},
        "MaxWindowDuration (ms)",
        "Matching rate",
        y_max=1.0,
    )
    chart.line_panel(
        360,
        52,
        260,
        220,
        {"Netting": aggregate(data, "value", "matched_value_ratio")["value"]},
        "MaxWindowDuration (ms)",
        "Matched value ratio",
        y_max=1.0,
    )
    chart.line_panel(
        670,
        52,
        260,
        220,
        {"Latency": aggregate(data, "value", "avg_latency_s")["value"]},
        "MaxWindowDuration (ms)",
        "Avg. latency (s)",
    )
    chart.save(path)


def plot_ablation(rows: list[dict[str, str]], path: Path) -> None:
    data = exp_rows(rows, "exp5_matcher_ablation")
    modes = ["exact_only", "best_fit", "full"]
    labels = ["Exact", "+BestFit", "+Split"]
    chart = PNGFigure(980, 310)
    chart.title("Matcher ablation", 18, 22)
    panels = [
        ("matched_intent_ratio", "Matching rate", 2, True),
        ("matched_value_ratio", "Matched value ratio", 2),
        ("split_allocations", "Split allocations", 2),
    ]
    for idx, panel in enumerate(panels):
        metric, ylabel, highlight = panel[0], panel[1], panel[2]
        is_ratio = len(panel) > 3 and panel[3]
        vals = [mean(f(row, metric) for row in data if row.get("matcher_mode") == mode) for mode in modes]
        chart.bar_panel(
            30 + idx * 320,
            52,
            285,
            220,
            labels,
            vals,
            ylabel,
            highlight=highlight,
            y_max=1.0 if is_ratio else None,
        )
    chart.save(path)


def plot_scale(rows: list[dict[str, str]], path: Path) -> None:
    data = exp_rows(rows, "exp6_scale")
    chart = PNGFigure(980, 550)
    chart.title("Scale sensitivity", 18, 22)
    panels = [
        ("matched_intent_ratio", "Matching rate", True),
        ("throughput_tps", "Throughput (tx/s)"),
        ("match_time_ms", "Solver match time (ms)"),
        ("proof_time_ms", "Proof verification time (ms)"),
    ]
    for idx, panel in enumerate(panels):
        metric, ylabel = panel[0], panel[1]
        is_ratio = len(panel) > 2 and panel[2]
        x = 55 + (idx % 2) * 470
        y = 52 + (idx // 2) * 240
        chart.line_panel(
            x,
            y,
            390,
            180,
            {"Netting": aggregate(data, "shard_num", metric)["value"]},
            "Shard count",
            ylabel,
            y_max=1.0 if is_ratio else None,
        )
    chart.save(path)


def plot_async(rows: list[dict[str, str]], path: Path) -> None:
    data = exp_rows(rows, "exp7_async_latency")
    chart = PNGFigure(980, 310)
    chart.title("Asynchrony and network latency", 18, 22)
    panels = [
        ("matched_intent_ratio", "Matching rate"),
        ("avg_latency_s", "Avg. latency (s)"),
        ("watermark_skew", "Vector-cut skew (blocks)"),
    ]
    for idx, (metric, ylabel) in enumerate(panels):
        chart.line_panel(
            50 + idx * 310,
            52,
            260,
            220,
            {"Netting": aggregate(data, "network_latency_ms", metric)["value"]},
            "Network latency (ms)",
            ylabel,
            y_max=1.0 if metric == "matched_intent_ratio" else None,
        )
    chart.save(path)


def mean(values: Iterable[float]) -> float:
    vals = [v for v in values if math.isfinite(v)]
    return sum(vals) / len(vals) if vals else 0.0


def parse_duration_from_times(start: str, end: str) -> float:
    start_ts = parse_timestamp(start)
    end_ts = parse_timestamp(end)
    if start_ts <= 0 or end_ts <= 0:
        return 0.0
    return max(0.0, end_ts - start_ts)


def parse_timestamp(value: str) -> float:
    if not value:
        return 0.0
    from datetime import datetime

    try:
        return datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()
    except ValueError:
        return 0.0


FONT_5X7 = {
    " ": ("00000", "00000", "00000", "00000", "00000", "00000", "00000"),
    "!": ("00100", "00100", "00100", "00100", "00100", "00000", "00100"),
    "%": ("11001", "11010", "00100", "01000", "10110", "00110", "00000"),
    "&": ("01100", "10010", "10100", "01000", "10101", "10010", "01101"),
    "(": ("00010", "00100", "01000", "01000", "01000", "00100", "00010"),
    ")": ("01000", "00100", "00010", "00010", "00010", "00100", "01000"),
    "+": ("00000", "00100", "00100", "11111", "00100", "00100", "00000"),
    ",": ("00000", "00000", "00000", "00000", "00110", "00100", "01000"),
    "-": ("00000", "00000", "00000", "11111", "00000", "00000", "00000"),
    ".": ("00000", "00000", "00000", "00000", "00000", "00110", "00110"),
    "/": ("00001", "00010", "00100", "01000", "10000", "00000", "00000"),
    "0": ("01110", "10001", "10011", "10101", "11001", "10001", "01110"),
    "1": ("00100", "01100", "00100", "00100", "00100", "00100", "01110"),
    "2": ("01110", "10001", "00001", "00010", "00100", "01000", "11111"),
    "3": ("11110", "00001", "00001", "01110", "00001", "00001", "11110"),
    "4": ("00010", "00110", "01010", "10010", "11111", "00010", "00010"),
    "5": ("11111", "10000", "10000", "11110", "00001", "00001", "11110"),
    "6": ("01110", "10000", "10000", "11110", "10001", "10001", "01110"),
    "7": ("11111", "00001", "00010", "00100", "01000", "01000", "01000"),
    "8": ("01110", "10001", "10001", "01110", "10001", "10001", "01110"),
    "9": ("01110", "10001", "10001", "01111", "00001", "00001", "01110"),
    ":": ("00000", "00110", "00110", "00000", "00110", "00110", "00000"),
    "?": ("01110", "10001", "00001", "00010", "00100", "00000", "00100"),
    "A": ("01110", "10001", "10001", "11111", "10001", "10001", "10001"),
    "B": ("11110", "10001", "10001", "11110", "10001", "10001", "11110"),
    "C": ("01111", "10000", "10000", "10000", "10000", "10000", "01111"),
    "D": ("11110", "10001", "10001", "10001", "10001", "10001", "11110"),
    "E": ("11111", "10000", "10000", "11110", "10000", "10000", "11111"),
    "F": ("11111", "10000", "10000", "11110", "10000", "10000", "10000"),
    "G": ("01111", "10000", "10000", "10011", "10001", "10001", "01111"),
    "H": ("10001", "10001", "10001", "11111", "10001", "10001", "10001"),
    "I": ("01110", "00100", "00100", "00100", "00100", "00100", "01110"),
    "J": ("00111", "00010", "00010", "00010", "10010", "10010", "01100"),
    "K": ("10001", "10010", "10100", "11000", "10100", "10010", "10001"),
    "L": ("10000", "10000", "10000", "10000", "10000", "10000", "11111"),
    "M": ("10001", "11011", "10101", "10101", "10001", "10001", "10001"),
    "N": ("10001", "11001", "10101", "10011", "10001", "10001", "10001"),
    "O": ("01110", "10001", "10001", "10001", "10001", "10001", "01110"),
    "P": ("11110", "10001", "10001", "11110", "10000", "10000", "10000"),
    "Q": ("01110", "10001", "10001", "10001", "10101", "10010", "01101"),
    "R": ("11110", "10001", "10001", "11110", "10100", "10010", "10001"),
    "S": ("01111", "10000", "10000", "01110", "00001", "00001", "11110"),
    "T": ("11111", "00100", "00100", "00100", "00100", "00100", "00100"),
    "U": ("10001", "10001", "10001", "10001", "10001", "10001", "01110"),
    "V": ("10001", "10001", "10001", "10001", "10001", "01010", "00100"),
    "W": ("10001", "10001", "10001", "10101", "10101", "10101", "01010"),
    "X": ("10001", "10001", "01010", "00100", "01010", "10001", "10001"),
    "Y": ("10001", "10001", "01010", "00100", "00100", "00100", "00100"),
    "Z": ("11111", "00001", "00010", "00100", "01000", "10000", "11111"),
}


class PNGFigure:
    def __init__(self, width: int, height: int):
        self.width = width
        self.height = height
        self.pixels = bytearray([255, 255, 255] * width * height)

    def title(self, text: str, x: int, y: int) -> None:
        self.draw_text(text, x, y - 16, scale=2, color=PALETTE["text"])

    def bar_panel(
        self,
        x: int,
        y: int,
        w: int,
        h: int,
        labels: list[str],
        values: list[float],
        ylabel: str,
        highlight: int = -1,
        y_max: float | None = None,
    ) -> None:
        max_v = y_max if y_max is not None else max(values + [1e-9]) * 1.18
        self.axes(x, y, w, h, ylabel, "", max_v)
        bar_w = w / max(1, len(values)) * 0.58
        for idx, value in enumerate(values):
            cx = x + (idx + 0.5) * w / len(values)
            bh = h * value / max_v
            color = PALETTE["ours"] if idx == highlight else [PALETTE["gray"], PALETTE["blue"], PALETTE["green"]][idx % 3]
            self.rect(cx - bar_w / 2, y + h - bh, bar_w, bh, color)
            self.draw_text(labels[idx], cx, y + h + 10, scale=1, anchor="center")
            self.draw_text(fmt(value), cx, y + h - bh - 13, scale=1, anchor="center")

    def stacked_bar_panel(
        self,
        x: int,
        y: int,
        w: int,
        h: int,
        labels: list[str],
        stacks: list[list[float]],
        stack_labels: list[str],
        colors: list[str],
        ylabel: str,
        legend_x: int,
        legend_y: int,
    ) -> None:
        totals = [sum(stack) for stack in stacks]
        max_v = max(totals + [1e-9]) * 1.18
        self.axes(x, y, w, h, ylabel, "", max_v)
        bar_w = w / max(1, len(stacks)) * 0.46
        for idx, stack in enumerate(stacks):
            cx = x + (idx + 0.5) * w / len(stacks)
            bottom = y + h
            for value, color in zip(stack, colors):
                sh = h * value / max_v
                self.rect(cx - bar_w / 2, bottom - sh, bar_w, sh, color)
                bottom -= sh
            self.draw_text(labels[idx], cx, y + h + 10, scale=1, anchor="center")
            self.draw_text(fmt(totals[idx]), cx, bottom - 13, scale=1, anchor="center")
        for idx, (label, color) in enumerate(zip(stack_labels, colors)):
            ly = legend_y + idx * 27
            self.rect(legend_x, ly, 18, 12, color)
            self.draw_text(label, legend_x + 28, ly - 1, scale=1)

    def line_panel(
        self,
        x: int,
        y: int,
        w: int,
        h: int,
        series: dict[str, list[tuple[float, float, float]]],
        xlabel: str,
        ylabel: str,
        y_max: float | None = None,
        reference_y: float | None = None,
        reference_label: str = "",
        x_tick_values: list[float] | None = None,
    ) -> None:
        all_points = [point for values in series.values() for point in values]
        if not all_points:
            self.axes(x, y, w, h, ylabel, xlabel, 1.0)
            return
        xs = [p[0] for p in all_points]
        ys = [p[1] + p[2] for p in all_points]
        x_min, x_max = min(xs), max(xs)
        if x_min == x_max:
            x_min -= 1.0
            x_max += 1.0
        max_v = y_max if y_max is not None else max(ys + [1e-9]) * 1.18
        self.axes(x, y, w, h, ylabel, xlabel, max_v)
        if reference_y is not None:
            ref = min(max(reference_y, 0.0), max_v)
            ref_y = y + h - ref / max_v * h
            self.dashed_line(x, ref_y, x + w, ref_y, PALETTE["red"], width=2)
            if reference_label:
                self.draw_text(reference_label, x + w - 4, ref_y - 16, scale=1, anchor="right", color=PALETTE["red"])
        colors = [PALETTE["ours"], PALETTE["blue"], PALETTE["green"], PALETTE["orange"]]
        for sidx, (name, values) in enumerate(series.items()):
            color = colors[sidx % len(colors)]
            coords: list[str] = []
            for px, py, err in values:
                sx = x + (px - x_min) / (x_max - x_min) * w
                sy = y + h - py / max_v * h
                coords.append(f"{sx:.1f},{sy:.1f}")
                if err > 0:
                    ey1 = y + h - (py - err) / max_v * h
                    ey2 = y + h - (py + err) / max_v * h
                    self.line(sx, ey1, sx, ey2, color, width=1)
                self.circle(sx, sy, 4, color)
                self.circle(sx, sy, 2, color)
            if len(coords) >= 2:
                for a, b in zip(values, values[1:]):
                    ax = x + (a[0] - x_min) / (x_max - x_min) * w
                    ay = y + h - a[1] / max_v * h
                    bx = x + (b[0] - x_min) / (x_max - x_min) * w
                    by = y + h - b[1] / max_v * h
                    self.line(ax, ay, bx, by, color, width=2)
            lx = x + 8 + sidx * 120
            ly = y + 15
            self.line(lx, ly, lx + 18, ly, color, width=2)
            self.draw_text(name, lx + 23, ly - 4, scale=1)
        self.x_ticks(x, y, w, h, x_min, x_max, x_tick_values)

    def box_panel(
        self,
        x: int,
        y: int,
        w: int,
        h: int,
        values: list[float],
        ylabel: str,
        xlabel: str,
        y_max: float | None = None,
        reference_y: float | None = None,
        reference_label: str = "",
    ) -> None:
        finite = sorted(v for v in values if math.isfinite(v))
        max_v = y_max if y_max is not None else max(finite + [1e-9]) * 1.18
        self.axes(x, y, w, h, ylabel, xlabel, max_v)
        if not finite:
            return

        def sy(value: float) -> float:
            return y + h - min(max(value, 0.0), max_v) / max_v * h

        low = finite[0]
        q1 = percentile_from_sorted(finite, 25)
        median = percentile_from_sorted(finite, 50)
        q3 = percentile_from_sorted(finite, 75)
        high = finite[-1]
        cx = x + w / 2
        box_w = min(96, w * 0.42)

        if reference_y is not None:
            ref = sy(reference_y)
            self.dashed_line(x, ref, x + w, ref, PALETTE["red"], width=2)
            if reference_label:
                self.draw_text(reference_label, x + w - 4, ref - 16, scale=1, anchor="right", color=PALETTE["red"])

        self.line(cx, sy(low), cx, sy(high), PALETTE["gray"], width=2)
        self.line(cx - box_w * 0.25, sy(low), cx + box_w * 0.25, sy(low), PALETTE["gray"], width=2)
        self.line(cx - box_w * 0.25, sy(high), cx + box_w * 0.25, sy(high), PALETTE["gray"], width=2)
        self.rect(cx - box_w / 2, sy(q3), box_w, max(1, sy(q1) - sy(q3)), "#D8E7F5")
        self.line(cx - box_w / 2, sy(q1), cx + box_w / 2, sy(q1), PALETTE["blue"], width=1)
        self.line(cx - box_w / 2, sy(q3), cx + box_w / 2, sy(q3), PALETTE["blue"], width=1)
        self.line(cx - box_w / 2, sy(median), cx + box_w / 2, sy(median), PALETTE["ours"], width=2)

        for idx, value in enumerate(finite):
            jitter = ((idx * 37) % 29 - 14) * min(1.0, box_w / 96)
            self.circle(cx + box_w * 0.62 + jitter, sy(value), 2, "#A7A7A7")

    def axes(self, x: int, y: int, w: int, h: int, ylabel: str, xlabel: str, y_max: float) -> None:
        for i in range(5):
            gy = y + h - h * i / 4
            self.line(x, gy, x + w, gy, PALETTE["light_gray"], width=1)
            value = y_max * i / 4
            self.draw_text(fmt(value), x - 8, gy - 4, scale=1, anchor="right")
        self.line(x, y + h, x + w, y + h, "#333333", width=1)
        self.line(x, y, x, y + h, "#333333", width=1)
        self.draw_text(xlabel, x + w / 2, y + h + 30, scale=1, anchor="center")
        self.draw_text(ylabel, x - 48, y + h / 2, scale=1, anchor="center", rotate_left=True)

    def x_ticks(
        self,
        x: int,
        y: int,
        w: int,
        h: int,
        x_min: float,
        x_max: float,
        tick_values: list[float] | None = None,
    ) -> None:
        ticks = tick_values or [x_min + (x_max - x_min) * i / 4 for i in range(5)]
        for val in ticks:
            sx = x + (val - x_min) / (x_max - x_min) * w
            self.line(sx, y, sx, y + h, PALETTE["light_gray"], width=1)
            self.draw_text(fmt(val), sx, y + h + 10, scale=1, anchor="center")

    def save(self, path: Path) -> None:
        path.write_bytes(encode_png(self.width, self.height, self.pixels))

    def rect(self, x: float, y: float, w: float, h: float, color: str) -> None:
        rgb = parse_hex(color)
        x0 = max(0, int(round(x)))
        y0 = max(0, int(round(y)))
        x1 = min(self.width, int(round(x + w)))
        y1 = min(self.height, int(round(y + h)))
        for py in range(y0, y1):
            start = (py * self.width + x0) * 3
            end = (py * self.width + x1) * 3
            self.pixels[start:end] = bytes(rgb) * (x1 - x0)

    def line(self, x1: float, y1: float, x2: float, y2: float, color: str, width: int = 1) -> None:
        rgb = parse_hex(color)
        x1i, y1i = int(round(x1)), int(round(y1))
        x2i, y2i = int(round(x2)), int(round(y2))
        dx = abs(x2i - x1i)
        dy = -abs(y2i - y1i)
        sx = 1 if x1i < x2i else -1
        sy = 1 if y1i < y2i else -1
        err = dx + dy
        x, y = x1i, y1i
        radius = max(0, width // 2)
        while True:
            self.dot(x, y, radius, rgb)
            if x == x2i and y == y2i:
                break
            e2 = 2 * err
            if e2 >= dy:
                err += dy
                x += sx
            if e2 <= dx:
                err += dx
                y += sy

    def dashed_line(self, x1: float, y1: float, x2: float, y2: float, color: str, width: int = 1) -> None:
        segments = 18
        dx = (x2 - x1) / segments
        dy = (y2 - y1) / segments
        for idx in range(0, segments, 2):
            self.line(x1 + dx * idx, y1 + dy * idx, x1 + dx * (idx + 1), y1 + dy * (idx + 1), color, width)

    def circle(self, cx: float, cy: float, radius: int, color: str) -> None:
        rgb = parse_hex(color)
        cxi, cyi = int(round(cx)), int(round(cy))
        rr = radius * radius
        for py in range(cyi - radius, cyi + radius + 1):
            for px in range(cxi - radius, cxi + radius + 1):
                if (px - cxi) ** 2 + (py - cyi) ** 2 <= rr:
                    self.set_pixel(px, py, rgb)

    def dot(self, x: int, y: int, radius: int, rgb: tuple[int, int, int]) -> None:
        if radius <= 0:
            self.set_pixel(x, y, rgb)
            return
        for py in range(y - radius, y + radius + 1):
            for px in range(x - radius, x + radius + 1):
                self.set_pixel(px, py, rgb)

    def draw_text(
        self,
        text: str,
        x: float,
        y: float,
        scale: int = 1,
        color: str = PALETTE["text"],
        anchor: str = "left",
        rotate_left: bool = False,
    ) -> None:
        glyphs = raster_text(text, scale)
        if rotate_left:
            glyphs = rotate_counterclockwise(glyphs)
        tw = len(glyphs[0]) if glyphs else 0
        th = len(glyphs)
        px = int(round(x))
        py = int(round(y))
        if anchor == "center":
            px -= tw // 2
            py -= th // 2
        elif anchor == "right":
            px -= tw
        rgb = parse_hex(color)
        for gy, row in enumerate(glyphs):
            for gx, on in enumerate(row):
                if on:
                    self.set_pixel(px + gx, py + gy, rgb)

    def set_pixel(self, x: int, y: int, rgb: tuple[int, int, int]) -> None:
        if x < 0 or y < 0 or x >= self.width or y >= self.height:
            return
        offset = (y * self.width + x) * 3
        self.pixels[offset : offset + 3] = bytes(rgb)


def raster_text(text: str, scale: int) -> list[list[bool]]:
    text = text.upper()
    rows = 7 * scale
    cols = max(1, sum((6 if ch != " " else 4) * scale for ch in text))
    bitmap = [[False] * cols for _ in range(rows)]
    cursor = 0
    for char in text:
        glyph = FONT_5X7.get(char, FONT_5X7["?"])
        for gy, row in enumerate(glyph):
            for gx, bit in enumerate(row):
                if bit != "1":
                    continue
                for sy in range(scale):
                    for sx in range(scale):
                        bitmap[gy * scale + sy][cursor + gx * scale + sx] = True
        cursor += (6 if char != " " else 4) * scale
    return bitmap


def rotate_counterclockwise(bitmap: list[list[bool]]) -> list[list[bool]]:
    if not bitmap:
        return bitmap
    return [[row[x] for row in bitmap] for x in range(len(bitmap[0]) - 1, -1, -1)]


def parse_hex(color: str) -> tuple[int, int, int]:
    color = color.lstrip("#")
    return int(color[0:2], 16), int(color[2:4], 16), int(color[4:6], 16)


def encode_png(width: int, height: int, pixels: bytearray) -> bytes:
    raw = bytearray()
    stride = width * 3
    for y in range(height):
        raw.append(0)
        raw.extend(pixels[y * stride : (y + 1) * stride])

    def chunk(kind: bytes, data: bytes) -> bytes:
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)

    return b"\x89PNG\r\n\x1a\n" + chunk(
        b"IHDR",
        struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0),
    ) + chunk(b"IDAT", zlib.compress(bytes(raw), level=9)) + chunk(b"IEND", b"")


def fmt(value: float) -> str:
    if abs(value) >= 100:
        return f"{value:.0f}"
    if abs(value) >= 10:
        return f"{value:.1f}"
    return f"{value:.2f}"


def percentile_from_sorted(values: list[float], percent: float) -> float:
    if not values:
        return 0.0
    if len(values) == 1:
        return values[0]
    rank = (len(values) - 1) * percent / 100.0
    lo = math.floor(rank)
    hi = math.ceil(rank)
    if lo == hi:
        return values[int(rank)]
    weight = rank - lo
    return values[lo] * (1.0 - weight) + values[hi] * weight


if __name__ == "__main__":
    raise SystemExit(main())
