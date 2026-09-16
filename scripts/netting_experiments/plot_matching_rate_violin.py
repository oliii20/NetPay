#!/usr/bin/env python3
"""Plot a violin chart for matching rates collected from repeated netting runs."""

from __future__ import annotations

import argparse
import csv
import math
import sys
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from statistics import mean

sys.path.insert(0, str(Path(__file__).resolve().parent))

from plot_experiments import DEFAULT_SUMMARY, f, read_rows, resolve_summary_path
from plot_five_matching_windows import batch_metrics_path
from plot_matching_windows import METRICS


DEFAULT_COUNT = 20
METRIC_KEYS = {
    "intent": ("MatchedIntentRatio", "matched_intent_ratio", "Matching rate"),
    "value": ("MatchedValueRatio", "matched_value_ratio", "Value matching rate"),
}


@dataclass(frozen=True)
class MatchingRateSample:
    rate: float
    run_id: str
    repeat: str
    window_id: str


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--summary", type=Path, default=DEFAULT_SUMMARY)
    parser.add_argument("--out", type=Path, default=Path("figures/netting/fig_matching_rate_violin"))
    parser.add_argument("--metric", choices=sorted(METRICS), default="intent")
    parser.add_argument("--level", choices=["window", "run"], default="window")
    parser.add_argument("--count", type=positive_int, default=DEFAULT_COUNT)
    parser.add_argument(
        "--run-ids",
        default="",
        help="optional comma-separated run_id list; otherwise the first --count netting runs are used",
    )
    parser.add_argument("--no-timestamp", action="store_true")
    args = parser.parse_args()

    summary_path = resolve_summary_path(args.summary)
    run_ids = parse_run_ids(args.run_ids)
    samples = collect_matching_rates(summary_path, args.metric, args.level, args.count, run_ids)
    if not samples:
        raise SystemExit(f"no matching-rate samples found in {summary_path}")

    timestamp_part = "" if args.no_timestamp else datetime.now().strftime("%Y%m%d-%H%M%S")
    output_stem = resolve_output_stem(args.out, timestamp_part)
    generated = plot_violin(samples, METRIC_KEYS[args.metric][2], output_stem)
    write_samples_csv(samples, output_stem.with_name(f"{output_stem.name}_samples.csv"))

    print(f"summary: {summary_path}")
    print(f"samples: {len(samples)}")
    print(f"mean {args.metric} matching rate: {mean(sample.rate for sample in samples):.6f}")
    print("figures: " + ", ".join(str(path) for path in generated))
    print(f"data: {output_stem.with_name(f'{output_stem.name}_samples.csv')}")
    return 0


def positive_int(raw: str) -> int:
    value = int(raw)
    if value <= 0:
        raise argparse.ArgumentTypeError("must be a positive integer")
    return value


def parse_run_ids(raw: str) -> list[str]:
    return [part.strip() for part in raw.split(",") if part.strip()]


def collect_matching_rates(
    summary_path: Path,
    metric: str,
    level: str,
    count: int,
    run_ids: list[str] | None = None,
) -> list[MatchingRateSample]:
    batch_metric_key, summary_metric_key, _ = METRIC_KEYS[metric]
    rows = read_rows(summary_path)
    selected = select_netting_rows(rows, count, run_ids)
    if level == "run":
        return [
            MatchingRateSample(
                rate=f(row, summary_metric_key),
                run_id=row.get("run_id", ""),
                repeat=row.get("repeat", ""),
                window_id="",
            )
            for row in selected
            if math.isfinite(f(row, summary_metric_key))
        ]

    samples: list[MatchingRateSample] = []
    for row in selected:
        run_id = row.get("run_id", "")
        repeat = row.get("repeat", "")
        batch_path = batch_metrics_path(summary_path, row)
        for idx, batch_row in enumerate(read_rows(batch_path), start=1):
            rate = f(batch_row, batch_metric_key)
            if not math.isfinite(rate):
                continue
            window_id = batch_row.get("WindowID") or str(idx)
            samples.append(MatchingRateSample(rate=rate, run_id=run_id, repeat=repeat, window_id=window_id))
    return samples


def select_netting_rows(
    rows: list[dict[str, str]],
    count: int,
    run_ids: list[str] | None = None,
) -> list[dict[str, str]]:
    wanted = set(run_ids or [])
    selected = [
        row
        for row in rows
        if row.get("method", "").startswith("netting")
        and f(row, "batch_count") > 0
        and (not wanted or row.get("run_id", "") in wanted)
    ]
    if wanted:
        found = {row.get("run_id", "") for row in selected}
        missing = wanted - found
        if missing:
            raise SystemExit(f"run_id not found or has no batches: {sorted(missing)}")
    return selected[:count]


def resolve_output_stem(out: Path, timestamp_part: str) -> Path:
    if out.suffix:
        stem = out.with_suffix("")
    else:
        stem = out
    if timestamp_part:
        stem = stem.with_name(f"{stem.name}_{timestamp_part}")
    stem.parent.mkdir(parents=True, exist_ok=True)
    return stem


def write_samples_csv(samples: list[MatchingRateSample], path: Path) -> None:
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=["rate", "run_id", "repeat", "window_id"])
        writer.writeheader()
        for sample in samples:
            writer.writerow(
                {
                    "rate": f"{sample.rate:.6f}",
                    "run_id": sample.run_id,
                    "repeat": sample.repeat,
                    "window_id": sample.window_id,
                }
            )


def plot_violin(samples: list[MatchingRateSample], ylabel: str, output_stem: Path) -> list[Path]:
    values = [sample.rate for sample in samples]
    try:
        return plot_violin_matplotlib(values, ylabel, output_stem)
    except ModuleNotFoundError:
        return plot_violin_svg(values, ylabel, output_stem)


def plot_violin_matplotlib(values: list[float], ylabel: str, output_stem: Path) -> list[Path]:
    import matplotlib.pyplot as plt

    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "Times", "DejaVu Serif"],
            "font.size": 10,
            "axes.labelsize": 10,
            "xtick.labelsize": 9,
            "ytick.labelsize": 9,
            "figure.dpi": 300,
            "savefig.dpi": 300,
            "savefig.bbox": "tight",
            "axes.spines.top": False,
            "axes.spines.right": False,
            "axes.grid": True,
            "grid.alpha": 0.22,
            "grid.linestyle": "-",
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
        }
    )
    fig, ax = plt.subplots(figsize=(2.4, 2.65))
    parts = ax.violinplot(
        [values],
        positions=[1],
        widths=0.72,
        showmeans=False,
        showmedians=False,
        showextrema=False,
    )
    for body in parts["bodies"]:
        body.set_facecolor("#D55E00")
        body.set_edgecolor("#222222")
        body.set_alpha(0.36)
        body.set_linewidth(1.0)

    q1, median, q3 = quantile(values, 0.25), quantile(values, 0.5), quantile(values, 0.75)
    ax.scatter(
        [1.0 + offset for offset in centered_offsets(len(values), 0.18)],
        values,
        s=10,
        color="#D55E00",
        edgecolors="white",
        linewidths=0.3,
        alpha=0.72,
        zorder=3,
    )
    ax.vlines(1, q1, q3, color="#222222", linewidth=3.0, zorder=4)
    ax.scatter([1], [median], marker="o", s=24, color="white", edgecolors="#222222", linewidths=0.9, zorder=5)
    ax.set_xlim(0.45, 1.55)
    ax.set_ylim(0, 1)
    ax.set_xticks([1])
    ax.set_xticklabels(["Netting"])
    ax.set_ylabel(ylabel)
    ax.yaxis.grid(True)
    ax.xaxis.grid(False)
    ax.tick_params(axis="x", length=0)
    fig.tight_layout(pad=0.55)

    pdf_path = output_stem.with_suffix(".pdf")
    png_path = output_stem.with_suffix(".png")
    fig.savefig(pdf_path)
    fig.savefig(png_path)
    plt.close(fig)
    return [pdf_path, png_path]


def plot_violin_svg(values: list[float], ylabel: str, output_stem: Path) -> list[Path]:
    svg_path = output_stem.with_suffix(".svg")
    width, height = 250, 270
    left, right, top, bottom = 58, 28, 14, 42
    plot_w = width - left - right
    plot_h = height - top - bottom
    center_x = left + plot_w / 2.0
    max_half_w = 44.0
    density = kde_density(values, 80, 0.0, 1.0)
    max_density = max((d for _, d in density), default=1.0)
    q1, median, q3 = quantile(values, 0.25), quantile(values, 0.5), quantile(values, 0.75)

    def sy(value: float) -> float:
        return top + (1.0 - value) * plot_h

    right_points = [(center_x + max_half_w * d / max_density, sy(y)) for y, d in density]
    left_points = [(center_x - max_half_w * d / max_density, sy(y)) for y, d in reversed(density)]
    path_d = "M " + " L ".join(f"{x:.2f} {y:.2f}" for x, y in [*right_points, *left_points]) + " Z"
    points = [
        (center_x + offset, sy(value))
        for offset, value in zip(centered_offsets(len(values), 0.28 * max_half_w), values)
    ]

    parts = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">',
        '<rect width="100%" height="100%" fill="white"/>',
        '<g font-family="Times New Roman, Times, serif" font-size="9" fill="#222222">',
    ]
    for tick in [0.0, 0.25, 0.5, 0.75, 1.0]:
        y = sy(tick)
        parts.append(f'<line x1="{left}" y1="{y:.2f}" x2="{left + plot_w}" y2="{y:.2f}" stroke="#d9d9d9" stroke-width="0.55"/>')
        parts.append(f'<text x="{left - 8}" y="{y + 3:.2f}" text-anchor="end">{tick:.2f}</text>')
    parts.append(f'<line x1="{left}" y1="{top}" x2="{left}" y2="{top + plot_h}" stroke="#222222" stroke-width="0.9"/>')
    parts.append(f'<line x1="{left}" y1="{top + plot_h}" x2="{left + plot_w}" y2="{top + plot_h}" stroke="#222222" stroke-width="0.9"/>')
    parts.append(f'<path d="{path_d}" fill="#D55E00" fill-opacity="0.36" stroke="#222222" stroke-width="1"/>')
    parts.append(f'<line x1="{center_x:.2f}" y1="{sy(q1):.2f}" x2="{center_x:.2f}" y2="{sy(q3):.2f}" stroke="#222222" stroke-width="3"/>')
    parts.append(f'<circle cx="{center_x:.2f}" cy="{sy(median):.2f}" r="4" fill="white" stroke="#222222" stroke-width="0.9"/>')
    for x, y in points:
        parts.append(f'<circle cx="{x:.2f}" cy="{y:.2f}" r="1.65" fill="#D55E00" fill-opacity="0.72" stroke="white" stroke-width="0.35"/>')
    parts.append(f'<text x="{center_x:.2f}" y="{height - 16}" text-anchor="middle">Netting</text>')
    parts.append(
        f'<text transform="translate(15 {height / 2:.2f}) rotate(-90)" text-anchor="middle" font-size="10">{ylabel}</text>'
    )
    parts.append("</g></svg>")
    svg_path.write_text("\n".join(parts) + "\n", encoding="utf-8")
    return [svg_path]


def kde_density(values: list[float], points: int, lo: float, hi: float) -> list[tuple[float, float]]:
    vals = [min(hi, max(lo, value)) for value in values]
    if not vals:
        return []
    std = math.sqrt(sum((value - mean(vals)) ** 2 for value in vals) / max(1, len(vals) - 1))
    bandwidth = max(0.035, 1.06 * std * len(vals) ** (-1 / 5)) if len(vals) > 1 else 0.05
    density: list[tuple[float, float]] = []
    for idx in range(points):
        y = lo + (hi - lo) * idx / max(1, points - 1)
        d = sum(math.exp(-0.5 * ((y - value) / bandwidth) ** 2) for value in vals)
        d /= max(1e-9, len(vals) * bandwidth * math.sqrt(2 * math.pi))
        density.append((y, d))
    return density


def quantile(values: list[float], q: float) -> float:
    vals = sorted(values)
    if not vals:
        return 0.0
    idx = (len(vals) - 1) * q
    lo = math.floor(idx)
    hi = math.ceil(idx)
    if lo == hi:
        return vals[int(idx)]
    return vals[lo] * (hi - idx) + vals[hi] * (idx - lo)


def centered_offsets(count: int, width: float) -> list[float]:
    if count <= 1:
        return [0.0] * count
    step = width / max(1, count - 1)
    start = -width / 2.0
    return [start + idx * step for idx in range(count)]


if __name__ == "__main__":
    raise SystemExit(main())
