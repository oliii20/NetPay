#!/usr/bin/env python3
"""Create publication-style SVG figures from netting experiment summaries."""

from __future__ import annotations

import argparse
import csv
import math
import statistics
from collections import defaultdict
from pathlib import Path
from typing import Iterable


PALETTE = {
    "ours": "#C44E52",
    "blue": "#4C72B0",
    "orange": "#DD8452",
    "green": "#55A868",
    "purple": "#8172B3",
    "gray": "#8C8C8C",
    "light_gray": "#E6E6E6",
    "text": "#222222",
}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--summary", type=Path, default=Path(".exp/netting-paper/summary.csv"))
    parser.add_argument("--out", type=Path, default=Path("figures/netting"))
    parser.add_argument(
        "--allow-missing",
        action="store_true",
        help="allow debug plots when summary.csv does not contain all seven experiment groups",
    )
    args = parser.parse_args()

    rows = read_rows(args.summary)
    if not rows:
        raise SystemExit(f"no rows found in {args.summary}")
    require_experiments(rows, allow_missing=args.allow_missing)
    args.out.mkdir(parents=True, exist_ok=True)

    plot_baseline(rows, args.out / "fig1_baseline_comparison.svg")
    plot_balance(rows, args.out / "fig2_netting_balance.svg")
    plot_batch_size(rows, args.out / "fig3_batch_size.svg")
    plot_window(rows, args.out / "fig4_window_duration.svg")
    plot_ablation(rows, args.out / "fig5_matcher_ablation.svg")
    plot_scale(rows, args.out / "fig6_scale.svg")
    plot_async(rows, args.out / "fig7_async_latency.svg")
    print(f"figures: {args.out}")
    return 0


def require_experiments(rows: list[dict[str, str]], allow_missing: bool) -> None:
    if allow_missing:
        return
    expected = {
        "exp1_baseline",
        "exp2_balance",
        "exp3_batch_size",
        "exp4_window_duration",
        "exp5_matcher_ablation",
        "exp6_scale",
        "exp7_async_latency",
    }
    found = {row.get("experiment", "") for row in rows}
    missing = sorted(expected - found)
    if missing:
        raise SystemExit(
            "summary.csv is incomplete; missing experiment groups: "
            + ", ".join(missing)
            + ". Re-run run_experiments.py for all groups, or pass --allow-missing for debug plots."
        )


def read_rows(path: Path) -> list[dict[str, str]]:
    with path.open(newline="", encoding="utf-8") as fp:
        return list(csv.DictReader(fp))


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


def plot_baseline(rows: list[dict[str, str]], path: Path) -> None:
    data = exp_rows(rows, "exp1_baseline")
    methods = ["static_relay", "static_broker", "netting_static_relay"]
    labels = ["Relay", "Broker", "Netting"]
    panels = [
        ("throughput_tps", "Throughput (tx/s)", False),
        ("avg_latency_s", "Avg. latency (s)", False),
        ("cross_messages_per_tx", "Cross-shard msgs / tx", False),
    ]
    chart = SVGFigure(980, 310)
    chart.title("Baseline comparison", 18, 22)
    for idx, (metric, ylabel, _) in enumerate(panels):
        vals = [mean(f(row, metric) for row in data if row.get("method") == method) for method in methods]
        chart.bar_panel(30 + idx * 320, 52, 285, 220, labels, vals, ylabel, highlight=2)
    chart.save(path)


def plot_balance(rows: list[dict[str, str]], path: Path) -> None:
    data = exp_rows(rows, "exp2_balance")
    chart = SVGFigure(640, 310)
    chart.title("Netting benefit under bidirectional traffic", 18, 22)
    series = {
        "Matched value": aggregate(data, "value", "matched_value_ratio")["value"],
        "Fallback value": aggregate(data, "value", "fallback_value_ratio")["value"],
    }
    chart.line_panel(55, 52, 540, 220, series, "Reverse traffic ratio", "Value ratio", y_max=1.0)
    chart.save(path)


def plot_batch_size(rows: list[dict[str, str]], path: Path) -> None:
    data = exp_rows(rows, "exp3_batch_size")
    chart = SVGFigure(980, 550)
    chart.title("BatchSize sensitivity", 18, 22)
    panels = [
        ("throughput_tps", "Throughput (tx/s)"),
        ("beacon_bytes_per_intent", "Beacon bytes / intent"),
        ("capital_lock_s", "Capital lock (s)"),
        ("avg_latency_s", "Avg. latency (s)"),
    ]
    for idx, (metric, ylabel) in enumerate(panels):
        x = 55 + (idx % 2) * 470
        y = 52 + (idx // 2) * 240
        chart.line_panel(x, y, 390, 180, {"Netting": aggregate(data, "value", metric)["value"]}, "BatchSize", ylabel)
    chart.save(path)


def plot_window(rows: list[dict[str, str]], path: Path) -> None:
    data = exp_rows(rows, "exp4_window_duration")
    chart = SVGFigure(860, 310)
    chart.title("Window duration trade-off", 18, 22)
    chart.line_panel(
        55,
        52,
        340,
        220,
        {"Matched value": aggregate(data, "value", "matched_value_ratio")["value"]},
        "MaxWindowDuration (ms)",
        "Matched value ratio",
        y_max=1.0,
    )
    chart.line_panel(
        480,
        52,
        320,
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
    chart = SVGFigure(980, 310)
    chart.title("Matcher ablation", 18, 22)
    panels = [
        ("matched_value_ratio", "Matched value ratio", 2),
        ("fallback_value_ratio", "Fallback value ratio", 2),
        ("split_allocations", "Split allocations", 2),
    ]
    for idx, (metric, ylabel, highlight) in enumerate(panels):
        vals = [mean(f(row, metric) for row in data if row.get("matcher_mode") == mode) for mode in modes]
        chart.bar_panel(30 + idx * 320, 52, 285, 220, labels, vals, ylabel, highlight=highlight)
    chart.save(path)


def plot_scale(rows: list[dict[str, str]], path: Path) -> None:
    data = exp_rows(rows, "exp6_scale")
    chart = SVGFigure(980, 550)
    chart.title("Scale sensitivity", 18, 22)
    panels = [
        ("throughput_tps", "Throughput (tx/s)"),
        ("beacon_bytes_per_intent", "Beacon bytes / intent"),
        ("match_time_ms", "Solver match time (ms)"),
        ("proof_time_ms", "Proof verification time (ms)"),
    ]
    for idx, (metric, ylabel) in enumerate(panels):
        x = 55 + (idx % 2) * 470
        y = 52 + (idx // 2) * 240
        chart.line_panel(x, y, 390, 180, {"Netting": aggregate(data, "shard_num", metric)["value"]}, "Shard count", ylabel)
    chart.save(path)


def plot_async(rows: list[dict[str, str]], path: Path) -> None:
    data = exp_rows(rows, "exp7_async_latency")
    chart = SVGFigure(980, 310)
    chart.title("Asynchrony and network latency", 18, 22)
    panels = [
        ("avg_latency_s", "Avg. latency (s)"),
        ("watermark_skew", "Vector-cut skew (blocks)"),
        ("fallback_value_ratio", "Fallback value ratio"),
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
            y_max=1.0 if "ratio" in metric else None,
        )
    chart.save(path)


def mean(values: Iterable[float]) -> float:
    vals = [v for v in values if math.isfinite(v)]
    return sum(vals) / len(vals) if vals else 0.0


class SVGFigure:
    def __init__(self, width: int, height: int):
        self.width = width
        self.height = height
        self.parts: list[str] = [
            f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">',
            '<rect width="100%" height="100%" fill="white"/>',
            "<style>",
            "text{font-family:'Times New Roman',DejaVu Serif,serif;fill:#222}",
            ".axis{stroke:#333;stroke-width:1}",
            ".grid{stroke:#D0D0D0;stroke-width:0.7;opacity:0.45}",
            ".tick{font-size:11px}",
            ".label{font-size:13px;font-weight:600}",
            ".legend{font-size:12px}",
            "</style>",
        ]

    def title(self, text: str, x: int, y: int) -> None:
        self.parts.append(f'<text x="{x}" y="{y}" font-size="18" font-weight="700">{escape(text)}</text>')

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
    ) -> None:
        max_v = max(values + [1e-9]) * 1.18
        self.axes(x, y, w, h, ylabel, "", max_v)
        bar_w = w / max(1, len(values)) * 0.58
        for idx, value in enumerate(values):
            cx = x + (idx + 0.5) * w / len(values)
            bh = h * value / max_v
            color = PALETTE["ours"] if idx == highlight else [PALETTE["gray"], PALETTE["blue"], PALETTE["green"]][idx % 3]
            self.parts.append(
                f'<rect x="{cx - bar_w / 2:.1f}" y="{y + h - bh:.1f}" width="{bar_w:.1f}" height="{bh:.1f}" fill="{color}"/>'
            )
            self.parts.append(
                f'<text class="tick" x="{cx:.1f}" y="{y + h + 18}" text-anchor="middle">{escape(labels[idx])}</text>'
            )
            self.parts.append(
                f'<text class="tick" x="{cx:.1f}" y="{y + h - bh - 5:.1f}" text-anchor="middle">{fmt(value)}</text>'
            )

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
                    self.parts.append(f'<line x1="{sx:.1f}" y1="{ey1:.1f}" x2="{sx:.1f}" y2="{ey2:.1f}" stroke="{color}" stroke-width="1"/>')
                self.parts.append(f'<circle cx="{sx:.1f}" cy="{sy:.1f}" r="3.4" fill="{color}" stroke="white" stroke-width="0.8"/>')
            if len(coords) >= 2:
                self.parts.append(f'<polyline points="{" ".join(coords)}" fill="none" stroke="{color}" stroke-width="2"/>')
            lx = x + 8 + sidx * 120
            ly = y + 15
            self.parts.append(f'<line x1="{lx}" y1="{ly}" x2="{lx + 18}" y2="{ly}" stroke="{color}" stroke-width="2"/>')
            self.parts.append(f'<text class="legend" x="{lx + 23}" y="{ly + 4}">{escape(name)}</text>')
        self.x_ticks(x, y, w, h, x_min, x_max)

    def axes(self, x: int, y: int, w: int, h: int, ylabel: str, xlabel: str, y_max: float) -> None:
        for i in range(5):
            gy = y + h - h * i / 4
            self.parts.append(f'<line class="grid" x1="{x}" y1="{gy:.1f}" x2="{x + w}" y2="{gy:.1f}"/>')
            value = y_max * i / 4
            self.parts.append(f'<text class="tick" x="{x - 8}" y="{gy + 4:.1f}" text-anchor="end">{fmt(value)}</text>')
        self.parts.append(f'<line class="axis" x1="{x}" y1="{y + h}" x2="{x + w}" y2="{y + h}"/>')
        self.parts.append(f'<line class="axis" x1="{x}" y1="{y}" x2="{x}" y2="{y + h}"/>')
        self.parts.append(f'<text class="label" x="{x + w / 2:.1f}" y="{y + h + 38}" text-anchor="middle">{escape(xlabel)}</text>')
        self.parts.append(
            f'<text class="label" transform="translate({x - 42},{y + h / 2:.1f}) rotate(-90)" text-anchor="middle">{escape(ylabel)}</text>'
        )

    def x_ticks(self, x: int, y: int, w: int, h: int, x_min: float, x_max: float) -> None:
        for i in range(5):
            val = x_min + (x_max - x_min) * i / 4
            sx = x + w * i / 4
            self.parts.append(f'<line class="grid" x1="{sx:.1f}" y1="{y}" x2="{sx:.1f}" y2="{y + h}"/>')
            self.parts.append(f'<text class="tick" x="{sx:.1f}" y="{y + h + 18}" text-anchor="middle">{fmt(val)}</text>')

    def save(self, path: Path) -> None:
        self.parts.append("</svg>")
        path.write_text("\n".join(self.parts), encoding="utf-8")


def fmt(value: float) -> str:
    if abs(value) >= 100:
        return f"{value:.0f}"
    if abs(value) >= 10:
        return f"{value:.1f}"
    return f"{value:.2f}"


def escape(text: str) -> str:
    return text.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


if __name__ == "__main__":
    raise SystemExit(main())
