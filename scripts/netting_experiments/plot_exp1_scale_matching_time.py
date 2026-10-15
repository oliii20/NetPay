#!/usr/bin/env python3
"""Plot matching-algorithm execution time across shard counts."""

from __future__ import annotations

import argparse
import statistics
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


DEFAULT_OUT = DEFAULT_OUT_DIR / "fig_exp1_scale_matching_time"
COLOR = "#0072B2"


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

    configure_matplotlib(dpi)
    groups = [
        sorted((sample for sample in samples if sample.shard_num == shard), key=lambda sample: sample.seed)
        for shard in SHARDS
    ]
    means = [statistics.mean(sample.match_time_ms for sample in group) for group in groups]
    deviations = [statistics.stdev(sample.match_time_ms for sample in group) if len(group) > 1 else 0.0 for group in groups]

    fig, ax = plt.subplots(figsize=(3.65, 2.75))
    seeds = sorted({sample.seed for sample in samples})
    for seed in seeds:
        values = [next(sample.match_time_ms for sample in group if sample.seed == seed) for group in groups]
        ax.plot(SHARDS, values, color=COLOR, linewidth=0.7, alpha=0.18, marker="o", markersize=2.8)
    ax.errorbar(
        SHARDS,
        means,
        yerr=deviations,
        color=COLOR,
        linewidth=1.8,
        marker="o",
        markersize=4.5,
        markerfacecolor="white",
        markeredgewidth=1.2,
        capsize=3,
        elinewidth=1.0,
        label="Mean ± SD",
        zorder=4,
    )
    ax.set_xlabel("Number of shards")
    ax.set_ylabel("Matching execution time (ms)")
    ax.set_xticks(SHARDS)
    ax.legend(loc="upper left", frameon=False)
    ax.yaxis.grid(True)
    ax.xaxis.grid(False)
    ax.tick_params(axis="x", length=0)
    fig.tight_layout(pad=0.6)
    return save_figure(fig, out_stem)


if __name__ == "__main__":
    raise SystemExit(main())
