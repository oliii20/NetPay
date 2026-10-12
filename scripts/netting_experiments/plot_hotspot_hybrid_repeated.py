#!/usr/bin/env python3
"""Plot repeated hotspot-hybrid experiment figures from summary.csv.

This is a thin adapter around the existing netting plotting code. It reuses:
- run_exp1_repeated_boxplots.plot_metric_boxplot for TPS/latency boxplots.
- plot_matching_rate_violin.collect_matching_rates and plot_violin for matching-rate violins.
- plot_experiments.PNGFigure.stacked_bar_panel for CTX stage latency.
"""

from __future__ import annotations

import argparse
import csv
import math
import sys
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Iterable

sys.path.insert(0, str(Path(__file__).resolve().parent))

from plot_experiments import PALETTE, PNGFigure, f  # noqa: E402
from plot_matching_rate_violin import collect_matching_rates, plot_violin, write_samples_csv  # noqa: E402
from run_exp1_repeated_boxplots import plot_metric_boxplot  # noqa: E402


DEFAULT_SUMMARY = Path("exp/hotspot-hybrid/summary.csv")
DEFAULT_OUT = Path("figures/netting/hotspot_hybrid")

METHOD_LABELS = [
    ("static_relay", "Relay"),
    ("clpa_broker", "BrokerChain"),
    ("static_broker", "BrokerChain"),
    ("netting_static_relay", "Netting"),
]

MATCHING_LABELS = {
    "intent": "Netting rate",
    "value": "Value matching rate",
}

RAW_STAGES = [
    ("stage_clpa_s", "CLPA", "#9A8F5A"),
    ("stage_source_s", "Source", PALETTE["gray"]),
    ("stage_window_s", "Window", PALETTE["orange"]),
    ("stage_match_s", "Match", PALETTE["green"]),
    ("stage_beacon_s", "Beacon", PALETTE["purple"]),
    ("stage_coordination_s", "Coord.", PALETTE["yellow"]),
    ("stage_transfer_s", "Transfer", PALETTE["blue"]),
    ("stage_target_s", "Target", PALETTE["teal"]),
    ("stage_settlement_s", "Settle", PALETTE["red"]),
    ("stage_fallback_s", "Fallback", "#6B6B6B"),
]

STAGES = [
    ("display_source_s", "Source", PALETTE["gray"]),
    ("display_netting_s", "Netting", PALETTE["orange"]),
    ("stage_coordination_s", "Coord.", PALETTE["yellow"]),
    ("display_target_s", "Target", PALETTE["teal"]),
    ("display_settlement_s", "Settle", PALETTE["red"]),
]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--summary", type=Path, default=DEFAULT_SUMMARY)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT)
    parser.add_argument("--metric", choices=["intent", "value"], default="intent")
    parser.add_argument("--violin-level", choices=["run", "window"], default="run")
    parser.add_argument(
        "--tx-latency-violin",
        action="store_true",
        help="also plot per-transaction confirmation latency distributions for Relay/BrokerChain/Netting",
    )
    parser.add_argument(
        "--tx-latency-bins",
        type=positive_int,
        default=160,
        help="histogram bins used for the all-transaction latency violin",
    )
    parser.add_argument(
        "--tx-latency-repeat",
        type=positive_int,
        help="limit the transaction latency violin to one repeat, e.g. 1 for repeat_01",
    )
    parser.add_argument(
        "--timestamp",
        default="",
        help="suffix added to generated filenames; default uses current time, pass 'none' to disable",
    )
    parser.add_argument(
        "--violin-count",
        type=positive_int,
        default=1000000,
        help="max netting runs used by the violin plot; default keeps all rows",
    )
    args = parser.parse_args()

    rows = read_rows(args.summary)
    if not rows:
        raise SystemExit(f"no rows found in {args.summary}")
    args.out.mkdir(parents=True, exist_ok=True)
    timestamp = resolve_timestamp(args.timestamp)

    generated: list[Path] = []
    generated.extend(
        plot_metric_boxplot(
            metric_groups(rows, "throughput_tps"),
            "Throughput (tx/s)",
            timestamped_stem(args.out / "fig_hotspot_repeated_tps_boxplot", timestamp),
        )
    )
    generated.extend(
        plot_metric_boxplot(
            metric_groups(rows, "avg_latency_s"),
            "Avg. latency (s)",
            timestamped_stem(args.out / "fig_hotspot_repeated_latency_boxplot", timestamp),
        )
    )
    generated.extend(
        plot_metric_boxplot(
            [("Netting", total_netting_rate_values(args.summary, rows))],
            "Total netting rate",
            timestamped_stem(args.out / "fig_hotspot_repeated_total_netting_rate_boxplot", timestamp),
        )
    )
    generated.extend(
        plot_metric_boxplot(
            [("Netting", total_matched_netting_rate_values(args.summary, rows))],
            "Any-match netting rate",
            timestamped_stem(args.out / "fig_hotspot_repeated_any_match_netting_rate_boxplot", timestamp),
        )
    )

    samples = collect_matching_rates(args.summary, args.metric, args.violin_level, args.violin_count)
    if not samples:
        raise SystemExit(f"no netting matching-rate samples found in {args.summary}")
    violin_stem = timestamped_stem(args.out / netting_violin_name(args.metric), timestamp)
    generated.extend(plot_violin(samples, MATCHING_LABELS[args.metric], violin_stem))
    samples_path = violin_stem.with_name(f"{violin_stem.name}_samples.csv")
    write_samples_csv(samples, samples_path)

    stage_path = timestamped_path(args.out / "fig_hotspot_ctx_stage_latency.png", timestamp)
    plot_stage_latency(args.summary, rows, stage_path)
    generated.append(stage_path)

    tx_latency_stats_path: Path | None = None
    if args.tx_latency_violin:
        tx_latency_rows = tx_latency_selected_rows(rows, args.tx_latency_repeat)
        tx_latency_name = "fig_hotspot_all_tx_confirmation_latency_violin"
        if args.tx_latency_repeat is not None:
            tx_latency_name = f"{tx_latency_name}_repeat{args.tx_latency_repeat:02d}"
        tx_latency_stem = timestamped_stem(args.out / tx_latency_name, timestamp)
        generated.append(plot_tx_latency_violin(args.summary, tx_latency_rows, tx_latency_stem, args.tx_latency_bins))
        tx_latency_stats_path = tx_latency_stem.with_name(f"{tx_latency_stem.name}_stats.csv")
        write_tx_latency_stats(tx_latency_rows, args.summary, tx_latency_stats_path, args.tx_latency_bins)

    print(f"summary: {args.summary}")
    print("figures:")
    for path in generated:
        print(f"  {path}")
    print(f"data: {samples_path}")
    if tx_latency_stats_path is not None:
        print(f"tx_latency_stats: {tx_latency_stats_path}")
    return 0


def positive_int(raw: str) -> int:
    value = int(raw)
    if value <= 0:
        raise argparse.ArgumentTypeError("must be a positive integer")
    return value


def resolve_timestamp(raw: str) -> str:
    if raw.lower() == "none":
        return ""
    return raw or datetime.now().strftime("%Y%m%d-%H%M%S")


def timestamped_stem(path: Path, timestamp: str) -> Path:
    if not timestamp:
        return path
    return path.with_name(f"{path.name}_{timestamp}")


def timestamped_path(path: Path, timestamp: str) -> Path:
    if not timestamp:
        return path
    return path.with_name(f"{path.stem}_{timestamp}{path.suffix}")


def netting_violin_name(metric: str) -> str:
    if metric == "intent":
        return "fig_hotspot_window_netting_rate_violin"
    return f"fig_hotspot_netting_{metric}_matching_violin"


def read_rows(path: Path) -> list[dict[str, str]]:
    with path.open(newline="", encoding="utf-8") as fp:
        return list(csv.DictReader(fp))


def tx_latency_selected_rows(rows: list[dict[str, str]], repeat: int | None) -> list[dict[str, str]]:
    if repeat is None:
        return rows
    selected = [row for row in rows if int(float_or_zero(row.get("repeat"))) == repeat]
    if not selected:
        raise SystemExit(f"no rows found for repeat_{repeat:02d}")
    return selected


def metric_groups(rows: list[dict[str, str]], metric: str) -> list[tuple[str, list[float]]]:
    grouped: dict[str, list[float]] = {}
    for method, label in METHOD_LABELS:
        values = grouped.setdefault(label, [])
        values.extend(
            f(row, metric)
            for row in rows
            if row.get("method") == method and math.isfinite(f(row, metric)) and f(row, metric) > 0
        )
    ordered: list[tuple[str, list[float]]] = []
    seen: set[str] = set()
    for _method, label in METHOD_LABELS:
        if label in seen or not grouped.get(label):
            continue
        ordered.append((label, grouped[label]))
        seen.add(label)
    return ordered


def total_netting_rate_values(summary_path: Path, rows: list[dict[str, str]]) -> list[float]:
    return total_batch_ratio_values(summary_path, rows, "FullMatchedIntentCount", "FullMatchedIntentRatio")


def total_matched_netting_rate_values(summary_path: Path, rows: list[dict[str, str]]) -> list[float]:
    return total_batch_ratio_values(summary_path, rows, "MatchedIntentCount", "MatchedIntentRatio")


def total_batch_ratio_values(
    summary_path: Path,
    rows: list[dict[str, str]],
    count_field: str,
    ratio_field: str,
) -> list[float]:
    values: list[float] = []
    for row in rows:
        if row.get("method") != "netting_static_relay" or f(row, "batch_count") <= 0:
            continue
        batch_rows = read_rows(resolve_batch_metrics_path(summary_path, row))
        matched = 0.0
        total = 0.0
        for batch in batch_rows:
            intent_count = f(batch, "IntentCount")
            total += intent_count
            if batch.get(count_field, "") != "":
                matched += f(batch, count_field)
            else:
                matched += f(batch, ratio_field) * intent_count
        all_ctx_count = f(row, "tx_number") or f(row, "tx_committed") or total
        if all_ctx_count > 0:
            values.append(matched / all_ctx_count)
    if not values:
        raise SystemExit("no netting batch metrics found for total netting rate boxplot")
    return values


def resolve_batch_metrics_path(summary_path: Path, row: dict[str, str]) -> Path:
    run_id = row.get("run_id", "")
    runs_dir = summary_path.resolve().parent / "runs"
    repeat = int(float_or_zero(row.get("repeat"))) if row.get("repeat") else 0
    if repeat > 0:
        repeated = runs_dir / f"repeat_{repeat:02d}" / run_id / "results" / "netting_batch_metrics.csv"
        if repeated.exists():
            return repeated
    return runs_dir / run_id / "results" / "netting_batch_metrics.csv"


def plot_stage_latency(summary_path: Path, rows: list[dict[str, str]], path: Path) -> None:
    groups = stage_groups(summary_path, rows)
    if not groups:
        raise SystemExit("no stage latency columns found")

    labels = [label for label, _stack in groups]
    stacks = [stack for _label, stack in groups]
    chart = PNGFigure(1320, 520)
    chart.title("CTX stage latency comparison", 24, 30)
    chart.stacked_bar_panel(
        80,
        82,
        850,
        315,
        labels,
        stacks,
        [label for _field, label, _color in STAGES],
        [color for _field, _label, color in STAGES],
        "Avg stage latency (s)",
        980,
        82,
        legend_top_to_bottom=True,
    )
    path.parent.mkdir(parents=True, exist_ok=True)
    chart.save(path)


@dataclass
class LatencyHistogram:
    label: str
    color: str
    count: int
    total: float
    max_latency: float
    bins: list[int]

    @property
    def mean(self) -> float:
        return self.total / self.count if self.count else 0.0


def plot_tx_latency_violin(summary_path: Path, rows: list[dict[str, str]], output_stem: Path, bin_count: int) -> Path:
    max_latency = max_tx_latency(summary_path, rows)
    if max_latency <= 0:
        raise SystemExit("no per-transaction confirmation latencies found")
    histograms = tx_latency_histograms(summary_path, rows, max_latency, bin_count)
    if not any(hist.count for hist in histograms):
        raise SystemExit("no per-transaction confirmation latencies found")
    svg_path = output_stem.with_suffix(".svg")
    write_tx_latency_violin_svg(histograms, svg_path, max_latency)
    return svg_path


def write_tx_latency_stats(rows: list[dict[str, str]], summary_path: Path, path: Path, bin_count: int) -> None:
    max_latency = max_tx_latency(summary_path, rows)
    histograms = tx_latency_histograms(summary_path, rows, max_latency, bin_count) if max_latency > 0 else []
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=["method", "tx_count", "mean_s", "p50_s", "p95_s", "max_s"])
        writer.writeheader()
        for hist in histograms:
            writer.writerow(
                {
                    "method": hist.label,
                    "tx_count": hist.count,
                    "mean_s": f"{hist.mean:.6f}",
                    "p50_s": f"{histogram_quantile(hist.bins, max_latency, 0.50):.6f}",
                    "p95_s": f"{histogram_quantile(hist.bins, max_latency, 0.95):.6f}",
                    "max_s": f"{hist.max_latency:.6f}",
                }
            )


def max_tx_latency(summary_path: Path, rows: list[dict[str, str]]) -> float:
    max_latency = 0.0
    for row in rows:
        for latency in iter_tx_latencies(summary_path, row):
            if math.isfinite(latency) and latency > max_latency:
                max_latency = latency
    return max_latency


def tx_latency_histograms(
    summary_path: Path,
    rows: list[dict[str, str]],
    max_latency: float,
    bin_count: int,
) -> list[LatencyHistogram]:
    hist_by_label = {
        "Relay": LatencyHistogram("Relay", PALETTE["blue"], 0, 0.0, 0.0, [0] * bin_count),
        "BrokerChain": LatencyHistogram("BrokerChain", PALETTE["yellow"], 0, 0.0, 0.0, [0] * bin_count),
        "Netting": LatencyHistogram("Netting", PALETTE["ours"], 0, 0.0, 0.0, [0] * bin_count),
    }
    for row in rows:
        label = method_label(row.get("method", ""))
        if not label:
            continue
        hist = hist_by_label[label]
        for latency in iter_tx_latencies(summary_path, row):
            if not math.isfinite(latency) or latency <= 0:
                continue
            idx = min(bin_count - 1, int(latency / max_latency * bin_count))
            hist.bins[idx] += 1
            hist.count += 1
            hist.total += latency
            hist.max_latency = max(hist.max_latency, latency)
    return [hist_by_label["Relay"], hist_by_label["BrokerChain"], hist_by_label["Netting"]]


def iter_tx_latencies(summary_path: Path, row: dict[str, str]) -> Iterable[float]:
    method = row.get("method", "")
    results_dir = resolve_run_dir(summary_path, row) / "results"
    if method == "netting_static_relay":
        yield from iter_netting_latencies(results_dir)
        return
    if method == "static_relay":
        yield from iter_detail_latencies(results_dir / "relay_stats_detail_tx_info.csv")
        return
    if method in {"clpa_broker", "static_broker"}:
        yield from iter_detail_latencies(results_dir / "broker_stats_detail_tx_info.csv")


def iter_netting_latencies(results_dir: Path) -> Iterable[float]:
    intent_path = results_dir / "netting_intent_metrics.csv"
    if intent_path.exists():
        with intent_path.open(newline="", encoding="utf-8") as fp:
            for row in csv.DictReader(fp):
                latency = float_or_zero(row.get("EndToEndLatencyNs")) / 1e9
                if latency > 0:
                    yield latency
    yield from iter_detail_latencies(results_dir / "relay_stats_detail_tx_info.csv")


def iter_detail_latencies(path: Path) -> Iterable[float]:
    if not path.exists():
        return
    with path.open(newline="", encoding="utf-8") as fp:
        for row in csv.DictReader(fp):
            end = row.get("Tx finally commit time", "")
            if not end:
                continue
            latency = parse_duration_s(row.get("Tx create time", ""), end)
            if latency > 0:
                yield latency


def resolve_run_dir(summary_path: Path, row: dict[str, str]) -> Path:
    runs_dir = summary_path.resolve().parent / "runs"
    run_id = row.get("run_id", "")
    repeat = int(float_or_zero(row.get("repeat"))) if row.get("repeat") else 0
    if repeat > 0:
        repeated = runs_dir / f"repeat_{repeat:02d}" / run_id
        if repeated.exists():
            return repeated
    return runs_dir / run_id


def method_label(method: str) -> str:
    if method == "static_relay":
        return "Relay"
    if method in {"clpa_broker", "static_broker"}:
        return "BrokerChain"
    if method == "netting_static_relay":
        return "Netting"
    return ""


def parse_duration_s(start: str, end: str) -> float:
    start_ts = parse_timestamp_s(start)
    end_ts = parse_timestamp_s(end)
    if start_ts <= 0 or end_ts <= 0:
        return 0.0
    return max(0.0, end_ts - start_ts)


def parse_timestamp_s(value: str) -> float:
    if not value:
        return 0.0
    try:
        return datetime.fromisoformat(value).timestamp()
    except ValueError:
        return 0.0


def float_or_zero(value: object) -> float:
    try:
        return float(value or 0)
    except (TypeError, ValueError):
        return 0.0


def write_tx_latency_violin_svg(histograms: list[LatencyHistogram], path: Path, max_latency: float) -> None:
    width, height = 520, 340
    left, right, top, bottom = 76, 28, 26, 58
    plot_w = width - left - right
    plot_h = height - top - bottom
    centers = [left + plot_w * (idx + 0.5) / len(histograms) for idx in range(len(histograms))]
    max_half_w = plot_w / len(histograms) * 0.32
    smoothed_bins = {hist.label: smooth_bins(hist.bins, sigma=3.2) for hist in histograms}
    max_bin = max((max(values) for values in smoothed_bins.values() if values), default=1.0)
    q_marks = [(0.5, "#222222", 2.1), (0.95, "#666666", 1.3)]

    def sy(value: float) -> float:
        return top + (1.0 - min(max(value, 0.0), max_latency) / max_latency) * plot_h

    parts = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">',
        '<rect width="100%" height="100%" fill="white"/>',
        '<g font-family="Times New Roman, Times, serif" font-size="10" fill="#222222">',
        f'<text x="{width / 2:.1f}" y="16" text-anchor="middle" font-size="14">All transaction confirmation latency distribution</text>',
    ]
    for tick in nice_ticks(max_latency, 5):
        y = sy(tick)
        parts.append(f'<line x1="{left}" y1="{y:.2f}" x2="{left + plot_w}" y2="{y:.2f}" stroke="#d9d9d9" stroke-width="0.65"/>')
        parts.append(f'<text x="{left - 8}" y="{y + 3:.2f}" text-anchor="end">{tick:.0f}</text>')
    parts.append(f'<line x1="{left}" y1="{top}" x2="{left}" y2="{top + plot_h}" stroke="#222222" stroke-width="0.9"/>')
    parts.append(f'<line x1="{left}" y1="{top + plot_h}" x2="{left + plot_w}" y2="{top + plot_h}" stroke="#222222" stroke-width="0.9"/>')

    for center_x, hist in zip(centers, histograms):
        if hist.count <= 0:
            continue
        density = smoothed_bins[hist.label]
        right_points: list[tuple[float, float]] = []
        left_points: list[tuple[float, float]] = []
        for idx, count in enumerate(density):
            y_value = max_latency * (idx + 0.5) / len(hist.bins)
            half_w = max_half_w * math.sqrt(count / max_bin) if count > 0 else 0.0
            right_points.append((center_x + half_w, sy(y_value)))
            left_points.append((center_x - half_w, sy(y_value)))
        path_d = closed_smooth_violin_path(right_points, left_points)
        parts.append(f'<path d="{path_d}" fill="{hist.color}" fill-opacity="0.42" stroke="#8F8F8F" stroke-width="1.45"/>')
        for q, color, stroke_width in q_marks:
            q_value = histogram_quantile(hist.bins, max_latency, q)
            q_y = sy(q_value)
            parts.append(
                f'<line x1="{center_x - max_half_w * 0.58:.2f}" y1="{q_y:.2f}" '
                f'x2="{center_x + max_half_w * 0.58:.2f}" y2="{q_y:.2f}" '
                f'stroke="{color}" stroke-width="{stroke_width}"/>'
            )
        parts.append(f'<text x="{center_x:.2f}" y="{top + plot_h + 18}" text-anchor="middle">{hist.label}</text>')
        parts.append(
            f'<text x="{center_x:.2f}" y="{top + plot_h + 34}" text-anchor="middle" font-size="8">'
            f'n={compact_count(hist.count)}</text>'
        )
    parts.append(f'<text transform="translate(18 {top + plot_h / 2:.2f}) rotate(-90)" text-anchor="middle">Confirmation latency (s)</text>')
    parts.append('<text x="{:.1f}" y="{}" text-anchor="middle" font-size="8">thick line: p50, thin line: p95</text>'.format(width / 2, height - 8))
    parts.append("</g></svg>")
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("\n".join(parts) + "\n", encoding="utf-8")


def smooth_bins(bins: list[int], sigma: float) -> list[float]:
    if not bins:
        return []
    radius = max(1, int(math.ceil(sigma * 3)))
    kernel = [math.exp(-0.5 * (offset / sigma) ** 2) for offset in range(-radius, radius + 1)]
    total = sum(kernel)
    kernel = [value / total for value in kernel]
    smoothed: list[float] = []
    for idx in range(len(bins)):
        value = 0.0
        for offset, weight in zip(range(-radius, radius + 1), kernel):
            source = min(len(bins) - 1, max(0, idx + offset))
            value += bins[source] * weight
        smoothed.append(value)
    return smoothed


def closed_smooth_violin_path(
    right_points: list[tuple[float, float]],
    left_points: list[tuple[float, float]],
) -> str:
    if not right_points or not left_points:
        return ""
    left_reversed = list(reversed(left_points))
    return (
        smooth_path_start(right_points)
        + f" L {left_reversed[0][0]:.2f} {left_reversed[0][1]:.2f}"
        + smooth_path_segments(left_reversed)
        + " Z"
    )


def smooth_path_start(points: list[tuple[float, float]]) -> str:
    return f"M {points[0][0]:.2f} {points[0][1]:.2f}" + smooth_path_segments(points)


def smooth_path_segments(points: list[tuple[float, float]]) -> str:
    if len(points) < 2:
        return ""
    segments: list[str] = []
    for idx in range(len(points) - 1):
        p0 = points[idx - 1] if idx > 0 else points[idx]
        p1 = points[idx]
        p2 = points[idx + 1]
        p3 = points[idx + 2] if idx + 2 < len(points) else p2
        c1 = (p1[0] + (p2[0] - p0[0]) / 6.0, p1[1] + (p2[1] - p0[1]) / 6.0)
        c2 = (p2[0] - (p3[0] - p1[0]) / 6.0, p2[1] - (p3[1] - p1[1]) / 6.0)
        segments.append(
            f" C {c1[0]:.2f} {c1[1]:.2f}, {c2[0]:.2f} {c2[1]:.2f}, {p2[0]:.2f} {p2[1]:.2f}"
        )
    return "".join(segments)


def histogram_quantile(bins: list[int], max_latency: float, q: float) -> float:
    total = sum(bins)
    if total <= 0:
        return 0.0
    target = total * q
    cumulative = 0
    for idx, count in enumerate(bins):
        cumulative += count
        if cumulative >= target:
            return max_latency * (idx + 0.5) / len(bins)
    return max_latency


def nice_ticks(max_value: float, count: int) -> list[float]:
    if max_value <= 0:
        return [0.0]
    raw_step = max_value / max(1, count - 1)
    magnitude = 10 ** math.floor(math.log10(raw_step))
    step = min((1, 2, 5, 10), key=lambda value: abs(value * magnitude - raw_step)) * magnitude
    top = math.ceil(max_value / step) * step
    return [idx * top / max(1, count - 1) for idx in range(count)]


def compact_count(count: int) -> str:
    if count >= 1_000_000:
        return f"{count / 1_000_000:.1f}M"
    if count >= 1_000:
        return f"{count / 1_000:.0f}K"
    return str(count)


@dataclass
class StageTotals:
    count: int
    totals: dict[str, float]


def stage_groups(summary_path: Path, rows: list[dict[str, str]]) -> list[tuple[str, list[float]]]:
    groups: list[tuple[str, list[float]]] = []
    seen: set[str] = set()
    for method, label in METHOD_LABELS:
        if label in seen:
            continue
        method_rows = [row for row in rows if row.get("method") == method]
        if not method_rows:
            continue
        stacks = [all_tx_stage_stack(summary_path, row) for row in method_rows]
        display_stacks = [display_stage_stack(stack) for stack in stacks]
        stack = [mean(stack[field] for stack in display_stacks) for field, _label, _color in STAGES]
        if sum(stack) <= 0:
            continue
        groups.append((label, stack))
        seen.add(label)
    return groups


def display_stage_stack(stack: dict[str, float]) -> dict[str, float]:
    return {
        "display_source_s": stack.get("stage_clpa_s", 0.0) + stack.get("stage_source_s", 0.0),
        "display_netting_s": (
            stack.get("stage_window_s", 0.0)
            + stack.get("stage_match_s", 0.0)
            + stack.get("stage_beacon_s", 0.0)
        ),
        "stage_coordination_s": stack.get("stage_coordination_s", 0.0),
        "display_target_s": stack.get("stage_transfer_s", 0.0) + stack.get("stage_target_s", 0.0),
        "display_settlement_s": stack.get("stage_settlement_s", 0.0) + stack.get("stage_fallback_s", 0.0),
    }


def all_tx_stage_stack(summary_path: Path, row: dict[str, str]) -> dict[str, float]:
    method = row.get("method", "")
    results_dir = resolve_run_dir(summary_path, row) / "results"
    totals = empty_stage_totals()
    count = 0
    if method == "netting_static_relay":
        detail = relay_detail_stage_totals(results_dir / "relay_stats_detail_tx_info.csv")
        intents = netting_intent_stage_totals(results_dir)
        totals = merge_stage_totals(totals, detail.totals)
        totals = merge_stage_totals(totals, intents.totals)
        count = detail.count + intents.count
    elif method == "static_relay":
        detail = relay_detail_stage_totals(results_dir / "relay_stats_detail_tx_info.csv")
        totals = merge_stage_totals(totals, detail.totals)
        count = detail.count
    elif method in {"clpa_broker", "static_broker"}:
        detail = broker_detail_stage_totals(results_dir / "broker_stats_detail_tx_info.csv")
        totals = merge_stage_totals(totals, detail.totals)
        count = detail.count
        totals["stage_clpa_s"] += f(row, "stage_clpa_s") * count

    if count <= 0:
        return empty_stage_totals()
    return {key: value / count for key, value in totals.items()}


def relay_detail_stage_totals(path: Path) -> StageTotals:
    totals = empty_stage_totals()
    count = 0
    for row in read_rows_if_exists(path):
        if not row.get("Tx finally commit time"):
            continue
        count += 1
        if row.get("Is cross-shard tx or not") == "true":
            add_relay_cross_stages(totals, row)
        else:
            add_inner_shard_stages(totals, row)
    return StageTotals(count, totals)


def broker_detail_stage_totals(path: Path) -> StageTotals:
    totals = empty_stage_totals()
    count = 0
    for row in read_rows_if_exists(path):
        if not row.get("Tx finally commit time"):
            continue
        count += 1
        mechanism = row.get("Mechanism", "")
        if mechanism == "Broker":
            add_broker_cross_stages(totals, row)
        elif mechanism == "FallbackToRelay":
            totals["stage_fallback_s"] += parse_duration_s(row.get("Tx create time", ""), row.get("Tx finally commit time", ""))
        else:
            add_inner_shard_stages(totals, row)
    return StageTotals(count, totals)


def netting_intent_stage_totals(results_dir: Path) -> StageTotals:
    intent_path = results_dir / "netting_intent_metrics.csv"
    batch_by_id = {
        row.get("BatchID", ""): row
        for row in read_rows_if_exists(results_dir / "netting_batch_metrics.csv")
    }
    totals = empty_stage_totals()
    count = 0
    for row in read_rows_if_exists(intent_path):
        e2e_s = float_or_zero(row.get("EndToEndLatencyNs")) / 1e9
        if e2e_s <= 0:
            continue
        count += 1
        batch = batch_by_id.get(row.get("BatchID", ""))
        source_s = float_or_zero(row.get("ReservationLatencyNs")) / 1e9
        window_s = float_or_zero(batch.get("WindowOpenDurationNs") if batch else 0) / 1e9
        match_s = float_or_zero(batch.get("MatchTimeNs") if batch else 0) / 1e9
        beacon_s = float_or_zero(batch.get("BeaconConsensusLatencyNs") if batch else 0) / 1e9
        post_beacon_s = max(0.0, e2e_s - source_s - window_s - match_s - beacon_s)
        totals["stage_source_s"] += source_s
        totals["stage_window_s"] += window_s
        totals["stage_match_s"] += match_s
        totals["stage_beacon_s"] += beacon_s
        if row.get("UsedFallback") == "true":
            totals["stage_fallback_s"] += post_beacon_s
        else:
            totals["stage_settlement_s"] += post_beacon_s
    return StageTotals(count, totals)


def add_inner_shard_stages(totals: dict[str, float], row: dict[str, str]) -> None:
    created = row.get("Tx create time", "")
    committed = row.get("Tx finally commit time", "")
    totals["stage_source_s"] += parse_duration_s(created, committed)


def add_relay_cross_stages(totals: dict[str, float], row: dict[str, str]) -> None:
    created = row.get("Tx create time", "")
    relay1_propose = row.get("Relay1 block propose time", "")
    relay1_commit = row.get("Relay1 tx commit time", "")
    relay2_create = row.get("Relay2 tx create time") or row.get("Relay2 block propose time", "")
    relay2_commit = row.get("Relay2 tx commit time") or row.get("Tx finally commit time", "")
    if not relay1_commit or not relay2_create:
        totals["stage_source_s"] += parse_duration_s(created, row.get("Tx finally commit time", ""))
        return
    totals["stage_source_s"] += parse_duration_s(created, relay1_commit)
    totals["stage_transfer_s"] += parse_duration_s(relay1_commit, relay2_create)
    totals["stage_target_s"] += parse_duration_s(relay2_create, relay2_commit)


def add_broker_cross_stages(totals: dict[str, float], row: dict[str, str]) -> None:
    created = row.get("Tx create time", "")
    broker1_create = row.get("Broker1 tx create time", "")
    broker1_commit = row.get("Broker1 tx commit time", "")
    broker2_create = row.get("Broker2 tx create time", "")
    broker2_commit = row.get("Broker2 tx commit time") or row.get("Tx finally commit time", "")
    if not broker1_commit or not broker2_create:
        totals["stage_source_s"] += parse_duration_s(created, row.get("Tx finally commit time", ""))
        return
    totals["stage_source_s"] += parse_duration_s(created, broker1_commit)
    totals["stage_coordination_s"] += parse_duration_s(broker1_commit, broker2_create)
    totals["stage_target_s"] += parse_duration_s(broker2_create, broker2_commit)


def empty_stage_totals() -> dict[str, float]:
    return {field: 0.0 for field, _label, _color in RAW_STAGES}


def merge_stage_totals(left: dict[str, float], right: dict[str, float]) -> dict[str, float]:
    for key, value in right.items():
        left[key] = left.get(key, 0.0) + value
    return left


def read_rows_if_exists(path: Path) -> list[dict[str, str]]:
    if not path.exists():
        return []
    return read_rows(path)


def mean(values: Iterable[float]) -> float:
    vals = [value for value in values if math.isfinite(value)]
    return sum(vals) / len(vals) if vals else 0.0


if __name__ == "__main__":
    raise SystemExit(main())
