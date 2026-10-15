#!/usr/bin/env python3
"""Plot overall fallback CTX ratios for the five exp1 scale repeats."""

from __future__ import annotations

import argparse
from pathlib import Path

from exp1_scale_metrics import (
    DEFAULT_OUT_DIR,
    DEFAULT_SUMMARIES,
    SHARDS,
    ScaleSample,
    configure_matplotlib,
    load_scale_samples,
    save_figure,
    validate_repeats,
    write_samples,
)


DEFAULT_OUT = DEFAULT_OUT_DIR / "fig_exp1_scale_fallback_rate_boxplot"
COLOR = "#D55E00"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--summary", dest="summaries", action="append", type=Path)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT, help="output path without extension")
    parser.add_argument("--dpi", type=int, default=300)
    parser.add_argument("--allow-incomplete", action="store_true")
    args = parser.parse_args()

    summaries = args.summaries or DEFAULT_SUMMARIES
    samples = load_scale_samples(summaries)
    validate_repeats(samples, None if args.allow_incomplete else 5)
    outputs = plot(samples, args.out, args.dpi)
    data_path = args.out.with_name(f"{args.out.name}_samples.csv")
    write_samples(samples, data_path)
    print("figures: " + ", ".join(str(path) for path in outputs))
    print(f"data: {data_path}")
    return 0


def plot(samples: list[ScaleSample], out_stem: Path, dpi: int) -> list[Path]:
    import matplotlib.pyplot as plt
    from matplotlib.ticker import PercentFormatter

    configure_matplotlib(dpi)
    values = [
        [sample.fallback_ctx / sample.original_ctx * 100.0 for sample in samples if sample.shard_num == shard]
        for shard in SHARDS
    ]
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
    for pos, group in zip(positions, values):
        for idx, value in enumerate(sorted(group)):
            offset = offsets[idx] if len(group) == len(offsets) else 0.0
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
    ax.set_ylabel("Fallback CTX ratio")
    ax.set_xticks(positions)
    ax.set_xticklabels([str(shard) for shard in SHARDS])
    ax.yaxis.set_major_formatter(PercentFormatter(xmax=100, decimals=0))
    ax.set_xlim(-0.55, len(SHARDS) - 0.45)
    ax.yaxis.grid(True)
    ax.xaxis.grid(False)
    ax.tick_params(axis="x", length=0)
    fig.tight_layout(pad=0.6)
    return save_figure(fig, out_stem)


if __name__ == "__main__":
    raise SystemExit(main())
