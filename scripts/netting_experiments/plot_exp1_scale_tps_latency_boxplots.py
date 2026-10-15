#!/usr/bin/env python3
"""Plot TPS and average-latency boxplots for the five exp1 scale repeats."""

from __future__ import annotations

import argparse
import csv
import math
from collections import defaultdict
from pathlib import Path
from typing import Iterable, Optional


DEFAULT_SUMMARIES = [
    Path("exp/netting-paper/repeated-exp1-scale/sessions/20261014-112256/summary.csv"),
    Path("exp/netting-paper/repeated-exp1-scale/sessions/repeat2-5-timeout3600/summary.csv"),
]
DEFAULT_OUT = Path("figures/netting/exp1-scale-repeated/fig_exp1_scale_tps_latency_boxplots")
SHARDS = [4, 8, 16, 32]
METHODS = [
    ("netting_static_relay", "Netting", "#D55E00"),
    ("static_relay", "Relay", "#0072B2"),
    ("static_broker", "Broker", "#009E73"),
    ("clpa_broker", "CLPA+Broker", "#CC79A7"),
]
METRICS = [
    ("throughput_tps", "TPS (tx/s)", "(a) Throughput"),
    ("avg_latency_s", "Average latency (s)", "(b) Average latency"),
]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--summary",
        dest="summaries",
        action="append",
        type=Path,
        help="summary.csv path; repeat this option for multiple sessions",
    )
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT, help="output path without extension")
    parser.add_argument("--dpi", type=int, default=300)
    parser.add_argument(
        "--allow-incomplete",
        action="store_true",
        help="plot available values instead of requiring five repeats per method/shard",
    )
    args = parser.parse_args()

    summaries = args.summaries or DEFAULT_SUMMARIES
    rows = load_rows(summaries)
    validate_rows(rows, expected_repeats=None if args.allow_incomplete else 5)
    outputs = plot(rows, args.out, args.dpi)
    print(f"summaries: {', '.join(str(path) for path in summaries)}")
    print(f"rows: {len(rows)}")
    print("figures: " + ", ".join(str(path) for path in outputs))
    return 0


def load_rows(paths: Iterable[Path]) -> list[dict[str, str]]:
    rows: list[dict[str, str]] = []
    seen: set[tuple[str, str, str]] = set()
    method_keys = {method for method, _, _ in METHODS}
    for path in paths:
        if not path.is_file():
            raise SystemExit(f"summary not found: {path}")
        with path.open(newline="", encoding="utf-8") as fp:
            for row in csv.DictReader(fp):
                if row.get("experiment") != "exp1_scale_repeated" or row.get("method") not in method_keys:
                    continue
                key = (row.get("seed", ""), row.get("shard_num", ""), row.get("method", ""))
                if key in seen:
                    raise SystemExit(f"duplicate seed/shard/method row: {key}")
                seen.add(key)
                rows.append(row)
    if not rows:
        raise SystemExit("no exp1_scale_repeated rows found")
    return rows


def validate_rows(rows: list[dict[str, str]], expected_repeats: Optional[int]) -> None:
    grouped = values_by_shard_method(rows, "throughput_tps")
    latency = values_by_shard_method(rows, "avg_latency_s")
    problems: list[str] = []
    for shard in SHARDS:
        for method, _, _ in METHODS:
            tps_count = len(grouped.get((shard, method), []))
            latency_count = len(latency.get((shard, method), []))
            if tps_count != latency_count:
                problems.append(f"{method}/{shard}: TPS n={tps_count}, latency n={latency_count}")
            if expected_repeats is not None and tps_count != expected_repeats:
                problems.append(f"{method}/{shard}: expected n={expected_repeats}, got n={tps_count}")
    if problems:
        raise SystemExit("incomplete or inconsistent data:\n  " + "\n  ".join(problems))


def values_by_shard_method(
    rows: list[dict[str, str]], metric: str
) -> dict[tuple[int, str], list[float]]:
    grouped: dict[tuple[int, str], list[float]] = defaultdict(list)
    for row in rows:
        try:
            shard = int(row["shard_num"])
            value = float(row[metric])
        except (KeyError, TypeError, ValueError):
            continue
        if shard in SHARDS and math.isfinite(value):
            grouped[(shard, row.get("method", ""))].append(value)
    return grouped


def plot(rows: list[dict[str, str]], out_stem: Path, dpi: int) -> list[Path]:
    import matplotlib.pyplot as plt
    from matplotlib.patches import Patch

    setup_matplotlib(dpi)
    fig, axes = plt.subplots(1, 2, figsize=(7.15, 2.8))
    group_width = 0.78
    box_width = group_width / len(METHODS) * 0.72

    for ax, (metric, ylabel, title) in zip(axes, METRICS):
        grouped = values_by_shard_method(rows, metric)
        positions: list[float] = []
        values: list[list[float]] = []
        colors: list[str] = []
        for shard_idx, shard in enumerate(SHARDS):
            for method_idx, (method, _, color) in enumerate(METHODS):
                positions.append(
                    shard_idx - group_width / 2 + group_width * (method_idx + 0.5) / len(METHODS)
                )
                values.append(grouped[(shard, method)])
                colors.append(color)

        boxes = ax.boxplot(
            values,
            positions=positions,
            widths=box_width,
            patch_artist=True,
            showmeans=True,
            boxprops={"linewidth": 0.85, "edgecolor": "#333333"},
            medianprops={"linewidth": 1.15, "color": "#111111"},
            meanprops={
                "marker": "D",
                "markerfacecolor": "white",
                "markeredgecolor": "#222222",
                "markeredgewidth": 0.6,
                "markersize": 3.1,
            },
            whiskerprops={"linewidth": 0.8, "color": "#444444"},
            capprops={"linewidth": 0.8, "color": "#444444"},
            flierprops={"marker": "o", "markersize": 2.5, "markerfacecolor": "white"},
        )
        for patch, color in zip(boxes["boxes"], colors):
            patch.set_facecolor(color)
            patch.set_alpha(0.62)
        add_repeat_points(ax, positions, values, colors)
        ax.set_title(title, pad=4)
        ax.set_xlabel("Number of shards")
        ax.set_ylabel(ylabel)
        ax.set_xticks(range(len(SHARDS)))
        ax.set_xticklabels([str(shard) for shard in SHARDS])
        ax.set_xlim(-0.55, len(SHARDS) - 0.45)
        finish_axes(ax)

    handles = [Patch(facecolor=color, edgecolor="#333333", alpha=0.62, label=label) for _, label, color in METHODS]
    fig.legend(handles=handles, loc="upper center", bbox_to_anchor=(0.5, 1.025), ncol=4, frameon=False)
    fig.tight_layout(rect=(0, 0, 1, 0.90), w_pad=1.6)
    return save_figure(fig, out_stem)


def add_repeat_points(ax: object, positions: list[float], values: list[list[float]], colors: list[str]) -> None:
    offsets = [-0.024, -0.012, 0.0, 0.012, 0.024]
    for pos, samples, color in zip(positions, values, colors):
        for idx, value in enumerate(sorted(samples)):
            offset = offsets[idx] if len(samples) == len(offsets) else 0.0
            ax.scatter(
                pos + offset,
                value,
                s=7,
                color=color,
                edgecolors="white",
                linewidths=0.25,
                alpha=0.9,
                zorder=3,
            )


def setup_matplotlib(dpi: int) -> None:
    import matplotlib.pyplot as plt

    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "Times", "DejaVu Serif"],
            "font.size": 9,
            "axes.labelsize": 9.5,
            "axes.titlesize": 10,
            "legend.fontsize": 8.5,
            "xtick.labelsize": 8.5,
            "ytick.labelsize": 8.5,
            "figure.dpi": dpi,
            "savefig.dpi": dpi,
            "savefig.bbox": "tight",
            "axes.spines.top": False,
            "axes.spines.right": False,
            "axes.grid": True,
            "grid.alpha": 0.18,
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

    out_stem.parent.mkdir(parents=True, exist_ok=True)
    pdf_path = out_stem.with_suffix(".pdf")
    png_path = out_stem.with_suffix(".png")
    fig.savefig(pdf_path)
    fig.savefig(png_path)
    plt.close(fig)
    return [pdf_path, png_path]


if __name__ == "__main__":
    raise SystemExit(main())
