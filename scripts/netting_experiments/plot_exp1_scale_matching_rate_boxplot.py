#!/usr/bin/env python3
"""Plot transaction-count matching-rate boxplots for five exp1 scale repeats."""

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
DEFAULT_OUT = Path("figures/netting/exp1-scale-repeated/fig_exp1_scale_matching_rate_boxplot")
SHARDS = [4, 8, 16, 32]
METHOD = "netting_static_relay"
COLOR = "#D55E00"


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
        help="plot available values instead of requiring five repeats per shard count",
    )
    args = parser.parse_args()

    summaries = args.summaries or DEFAULT_SUMMARIES
    grouped = load_matching_rates(summaries)
    validate_rates(grouped, expected_repeats=None if args.allow_incomplete else 5)
    outputs = plot(grouped, args.out, args.dpi)
    print(f"summaries: {', '.join(str(path) for path in summaries)}")
    print("samples: " + ", ".join(f"{shard} shards={len(grouped[shard])}" for shard in SHARDS))
    print("figures: " + ", ".join(str(path) for path in outputs))
    return 0


def load_matching_rates(paths: Iterable[Path]) -> dict[int, list[float]]:
    grouped: dict[int, list[float]] = defaultdict(list)
    seen: set[tuple[str, int]] = set()
    for path in paths:
        if not path.is_file():
            raise SystemExit(f"summary not found: {path}")
        with path.open(newline="", encoding="utf-8") as fp:
            for row in csv.DictReader(fp):
                if row.get("experiment") != "exp1_scale_repeated" or row.get("method") != METHOD:
                    continue
                try:
                    shard = int(row["shard_num"])
                    # matched_intent_ratio = count(MatchedAmount > 0) / IntentCount.
                    rate = float(row["matched_intent_ratio"])
                except (KeyError, TypeError, ValueError):
                    continue
                if shard not in SHARDS or not math.isfinite(rate):
                    continue
                key = (row.get("seed", ""), shard)
                if key in seen:
                    raise SystemExit(f"duplicate netting seed/shard row: {key}")
                seen.add(key)
                grouped[shard].append(rate * 100.0)
    return grouped


def validate_rates(grouped: dict[int, list[float]], expected_repeats: Optional[int]) -> None:
    problems: list[str] = []
    for shard in SHARDS:
        count = len(grouped.get(shard, []))
        if count == 0:
            problems.append(f"{shard} shards: no samples")
        elif expected_repeats is not None and count != expected_repeats:
            problems.append(f"{shard} shards: expected n={expected_repeats}, got n={count}")
    if problems:
        raise SystemExit("incomplete matching-rate data:\n  " + "\n  ".join(problems))


def plot(grouped: dict[int, list[float]], out_stem: Path, dpi: int) -> list[Path]:
    import matplotlib.pyplot as plt
    from matplotlib.ticker import PercentFormatter

    setup_matplotlib(dpi)
    values = [grouped[shard] for shard in SHARDS]
    positions = list(range(len(SHARDS)))
    fig, ax = plt.subplots(figsize=(3.55, 2.75))
    boxes = ax.boxplot(
        values,
        positions=positions,
        widths=0.48,
        patch_artist=True,
        showmeans=True,
        boxprops={"linewidth": 0.9, "edgecolor": "#333333"},
        medianprops={"linewidth": 1.25, "color": "#111111"},
        meanprops={
            "marker": "D",
            "markerfacecolor": "white",
            "markeredgecolor": "#222222",
            "markeredgewidth": 0.65,
            "markersize": 3.6,
        },
        whiskerprops={"linewidth": 0.85, "color": "#444444"},
        capprops={"linewidth": 0.85, "color": "#444444"},
        flierprops={"marker": "o", "markersize": 2.8, "markerfacecolor": "white"},
    )
    for patch in boxes["boxes"]:
        patch.set_facecolor(COLOR)
        patch.set_alpha(0.62)

    offsets = [-0.055, -0.0275, 0.0, 0.0275, 0.055]
    for pos, samples in zip(positions, values):
        for idx, value in enumerate(sorted(samples)):
            offset = offsets[idx] if len(samples) == len(offsets) else 0.0
            ax.scatter(
                pos + offset,
                value,
                s=10,
                color=COLOR,
                edgecolors="white",
                linewidths=0.3,
                alpha=0.9,
                zorder=3,
            )

    ax.set_xlabel("Number of shards")
    ax.set_ylabel("Transaction-count matching rate")
    ax.set_xticks(positions)
    ax.set_xticklabels([str(shard) for shard in SHARDS])
    ax.yaxis.set_major_formatter(PercentFormatter(xmax=100, decimals=0))
    ax.set_xlim(-0.55, len(SHARDS) - 0.45)
    ax.yaxis.grid(True)
    ax.xaxis.grid(False)
    ax.tick_params(axis="x", length=0)
    fig.tight_layout(pad=0.6)
    return save_figure(fig, out_stem)


def setup_matplotlib(dpi: int) -> None:
    import matplotlib.pyplot as plt

    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "Times", "DejaVu Serif"],
            "font.size": 9,
            "axes.labelsize": 9.5,
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
