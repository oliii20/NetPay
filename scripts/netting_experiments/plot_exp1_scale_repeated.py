#!/usr/bin/env python3
"""Plot repeated exp1 scale TPS/latency bars and boxplots."""

from __future__ import annotations

import argparse
import csv
import math
import statistics
from collections import defaultdict
from datetime import datetime
from pathlib import Path
from typing import Iterable, Optional


DEFAULT_SUMMARY = Path("exp/netting-paper/repeated-exp1-scale/latest/summary.csv")
DEFAULT_OUT = Path("figures/netting/exp1-scale-repeated")
METHOD_ORDER = [
    ("netting_static_relay", "Netting", "#D55E00"),
    ("static_relay", "Relay", "#0072B2"),
    ("static_broker", "Broker", "#009E73"),
    ("clpa_broker", "CLPA+Broker", "#CC79A7"),
]
METRICS = [
    ("throughput_tps", "TPS", "Throughput (tx/s)", "tps"),
    ("avg_latency_s", "Latency", "Latency (s)", "latency"),
]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--summary", type=Path, default=DEFAULT_SUMMARY)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT)
    parser.add_argument("--no-timestamp", action="store_true", help="overwrite canonical figure names")
    parser.add_argument("--dpi", type=int, default=300)
    args = parser.parse_args()

    summary_path = resolve_summary_path(args.summary)
    rows = read_rows(summary_path)
    rows = [row for row in rows if row.get("method") in method_keys()]
    if not rows:
        raise SystemExit(f"no scale repeated rows found in {summary_path}")

    args.out.mkdir(parents=True, exist_ok=True)
    timestamp = "" if args.no_timestamp else datetime.now().strftime("%Y%m%d-%H%M%S")
    generated: list[Path] = []
    for metric, title, ylabel, stem in METRICS:
        generated.extend(plot_grouped_bar(rows, metric, title, ylabel, timestamped_stem(args.out / f"fig_exp1_scale_{stem}_bar", timestamp), args.dpi))
        generated.extend(plot_boxplot(rows, metric, title, ylabel, timestamped_stem(args.out / f"fig_exp1_scale_{stem}_boxplot", timestamp), args.dpi))

    print("figures: " + ", ".join(str(path) for path in generated))
    return 0


def resolve_summary_path(path: Path) -> Path:
    if path.exists():
        return path
    latest_txt = path.parents[1] / "latest.txt" if len(path.parents) > 1 else None
    if latest_txt and latest_txt.exists():
        fallback = Path(latest_txt.read_text(encoding="utf-8").strip()) / "summary.csv"
        if fallback.exists():
            return fallback
    return path


def read_rows(path: Path) -> list[dict[str, str]]:
    if not path.exists():
        raise SystemExit(f"summary not found: {path}")
    with path.open(newline="", encoding="utf-8") as fp:
        return list(csv.DictReader(fp))


def method_keys() -> set[str]:
    return {method for method, _, _ in METHOD_ORDER}


def timestamped_stem(stem: Path, timestamp: str) -> Path:
    if not timestamp:
        return stem
    return stem.with_name(f"{stem.name}_{timestamp}")


def plot_grouped_bar(
    rows: list[dict[str, str]],
    metric: str,
    title: str,
    ylabel: str,
    out_stem: Path,
    dpi: int,
) -> list[Path]:
    import matplotlib.pyplot as plt

    setup_matplotlib(dpi)
    shards = shard_values(rows)
    stats = grouped_stats(rows, metric)
    x_positions = list(range(len(shards)))
    group_width = 0.78
    bar_width = group_width / len(METHOD_ORDER)

    fig, ax = plt.subplots(figsize=(6.4, 3.0))
    for method_idx, (method, label, color) in enumerate(METHOD_ORDER):
        offsets = [x - group_width / 2 + bar_width * (method_idx + 0.5) for x in x_positions]
        means = [stats.get((shard, method), {}).get("mean", math.nan) for shard in shards]
        errs = [stats.get((shard, method), {}).get("stderr", 0.0) for shard in shards]
        ax.bar(
            offsets,
            means,
            width=bar_width * 0.88,
            label=label,
            color=color,
            alpha=0.88,
            edgecolor="#222222",
            linewidth=0.45,
            yerr=errs,
            error_kw={"elinewidth": 0.9, "ecolor": "#222222", "capsize": 2.6, "capthick": 0.9},
        )

    ax.set_title(title, pad=5)
    ax.set_ylabel(ylabel)
    ax.set_xlabel("Shard count")
    ax.set_xticks(x_positions)
    ax.set_xticklabels([str(shard) for shard in shards])
    ax.legend(ncol=4, frameon=False, loc="upper center", bbox_to_anchor=(0.5, 1.18), columnspacing=1.0, handlelength=1.3)
    ax.margins(x=0.03)
    finish_axes(ax)
    fig.tight_layout(pad=0.55)
    return save_figure(fig, out_stem)


def plot_boxplot(
    rows: list[dict[str, str]],
    metric: str,
    title: str,
    ylabel: str,
    out_stem: Path,
    dpi: int,
) -> list[Path]:
    import matplotlib.pyplot as plt

    setup_matplotlib(dpi)
    shards = shard_values(rows)
    grouped = values_by_shard_method(rows, metric)
    positions: list[float] = []
    values: list[list[float]] = []
    colors: list[str] = []
    group_width = 0.76
    box_width = group_width / len(METHOD_ORDER) * 0.72

    for shard_idx, shard in enumerate(shards):
        for method_idx, (method, _, color) in enumerate(METHOD_ORDER):
            vals = grouped.get((shard, method), [])
            if not vals:
                continue
            pos = shard_idx - group_width / 2 + group_width * (method_idx + 0.5) / len(METHOD_ORDER)
            positions.append(pos)
            values.append(vals)
            colors.append(color)

    fig, ax = plt.subplots(figsize=(6.4, 3.0))
    bp = ax.boxplot(
        values,
        positions=positions,
        widths=box_width,
        patch_artist=True,
        showmeans=True,
        meanline=False,
        boxprops={"linewidth": 0.9, "edgecolor": "#222222"},
        medianprops={"linewidth": 1.2, "color": "#111111"},
        meanprops={"marker": "D", "markerfacecolor": "white", "markeredgecolor": "#222222", "markersize": 3.5},
        whiskerprops={"linewidth": 0.9, "color": "#333333"},
        capprops={"linewidth": 0.9, "color": "#333333"},
        flierprops={"marker": "o", "markerfacecolor": "white", "markeredgecolor": "#555555", "markersize": 2.6, "alpha": 0.75},
    )
    for patch, color in zip(bp["boxes"], colors):
        patch.set_facecolor(color)
        patch.set_alpha(0.34)

    for pos, vals, color in zip(positions, values, colors):
        for offset, value in zip(centered_offsets(len(vals), 0.035), vals):
            ax.scatter(pos + offset, value, s=10, color=color, edgecolors="white", linewidths=0.25, alpha=0.82, zorder=3)

    handles = [plt.Line2D([0], [0], color=color, lw=6, alpha=0.55) for _, _, color in METHOD_ORDER]
    labels = [label for _, label, _ in METHOD_ORDER]
    ax.legend(handles, labels, ncol=4, frameon=False, loc="upper center", bbox_to_anchor=(0.5, 1.18), columnspacing=1.0, handlelength=1.3)
    ax.set_title(title, pad=5)
    ax.set_ylabel(ylabel)
    ax.set_xlabel("Shard count")
    ax.set_xticks(list(range(len(shards))))
    ax.set_xticklabels([str(shard) for shard in shards])
    ax.set_xlim(-0.55, len(shards) - 0.45)
    finish_axes(ax)
    fig.tight_layout(pad=0.55)
    return save_figure(fig, out_stem)


def setup_matplotlib(dpi: int) -> None:
    import matplotlib.pyplot as plt

    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "Times", "DejaVu Serif"],
            "font.size": 9.5,
            "axes.labelsize": 10,
            "axes.titlesize": 10.5,
            "xtick.labelsize": 9,
            "ytick.labelsize": 9,
            "legend.fontsize": 8.5,
            "figure.dpi": dpi,
            "savefig.dpi": dpi,
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


def finish_axes(ax: object) -> None:
    ax.yaxis.grid(True)
    ax.xaxis.grid(False)
    ax.tick_params(axis="x", length=0)


def save_figure(fig: object, out_stem: Path) -> list[Path]:
    import matplotlib.pyplot as plt

    pdf_path = out_stem.with_suffix(".pdf")
    png_path = out_stem.with_suffix(".png")
    fig.savefig(pdf_path)
    fig.savefig(png_path)
    plt.close(fig)
    return [pdf_path, png_path]


def shard_values(rows: Iterable[dict[str, str]]) -> list[int]:
    return sorted({int(float(row["shard_num"])) for row in rows if row.get("shard_num")})


def values_by_shard_method(rows: list[dict[str, str]], metric: str) -> dict[tuple[int, str], list[float]]:
    grouped: dict[tuple[int, str], list[float]] = defaultdict(list)
    for row in rows:
        value = float_or_none(row.get(metric, ""))
        if value is None or not math.isfinite(value):
            continue
        shard = int(float(row.get("shard_num", "0")))
        method = row.get("method", "")
        if method in method_keys():
            grouped[(shard, method)].append(value)
    return grouped


def grouped_stats(rows: list[dict[str, str]], metric: str) -> dict[tuple[int, str], dict[str, float]]:
    grouped = values_by_shard_method(rows, metric)
    stats: dict[tuple[int, str], dict[str, float]] = {}
    for key, values in grouped.items():
        stderr = statistics.stdev(values) / math.sqrt(len(values)) if len(values) > 1 else 0.0
        stats[key] = {"mean": statistics.mean(values), "stderr": stderr}
    return stats


def centered_offsets(count: int, width: float) -> list[float]:
    if count <= 1:
        return [0.0] * count
    step = width / max(1, count - 1)
    start = -width / 2
    return [start + idx * step for idx in range(count)]


def float_or_none(value: Optional[str]) -> Optional[float]:
    try:
        if value in {None, ""}:
            return None
        return float(value)
    except (TypeError, ValueError):
        return None


if __name__ == "__main__":
    raise SystemExit(main())
