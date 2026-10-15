#!/usr/bin/env python3
"""Plot original CTX counts beside stacked netting outcome counts."""

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


DEFAULT_OUT = DEFAULT_OUT_DIR / "fig_exp1_scale_ctx_counts"
COLORS = {
    "original": "#7F8C8D",
    "netted": "#009E73",
    "fallback": "#D55E00",
    "settlement": "#56B4E9",
}


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
    import numpy as np

    configure_matplotlib(dpi)
    original = means(samples, "original_ctx")
    netted = means(samples, "netted_ctx")
    fallback = means(samples, "fallback_ctx")
    settlement = means(samples, "settlement_tx")
    x = np.arange(len(SHARDS), dtype=float)
    width = 0.31

    fig, ax = plt.subplots(figsize=(6.5, 3.0))
    ax.bar(
        x - width * 0.58,
        original,
        width,
        label="Original CTX number",
        color=COLORS["original"],
        edgecolor="white",
        linewidth=0.6,
    )
    ax.bar(
        x + width * 0.58,
        netted,
        width,
        label="Netted CTX number",
        color=COLORS["netted"],
        edgecolor="white",
        linewidth=0.6,
    )
    ax.bar(
        x + width * 0.58,
        fallback,
        width,
        bottom=netted,
        label="Fallback CTX number",
        color=COLORS["fallback"],
        edgecolor="white",
        linewidth=0.6,
    )
    stacked_base = np.asarray(netted) + np.asarray(fallback)
    ax.bar(
        x + width * 0.58,
        settlement,
        width,
        bottom=stacked_base,
        label="Settlement tx number",
        color=COLORS["settlement"],
        edgecolor="white",
        linewidth=0.6,
    )
    ax.set_xlabel("Number of shards")
    ax.set_ylabel("Transaction count")
    ax.set_xticks(x)
    ax.set_xticklabels([str(shard) for shard in SHARDS])
    ax.ticklabel_format(axis="y", style="sci", scilimits=(0, 0), useMathText=True)
    ax.legend(loc="upper center", bbox_to_anchor=(0.5, 1.18), ncol=2, frameon=False)
    ax.yaxis.grid(True)
    ax.xaxis.grid(False)
    ax.tick_params(axis="x", length=0)
    ax.margins(x=0.06)
    fig.tight_layout(rect=(0, 0, 1, 0.89), pad=0.6)
    return save_figure(fig, out_stem)


def means(samples: list[ScaleSample], field: str) -> list[float]:
    return [
        statistics.mean(getattr(sample, field) for sample in samples if sample.shard_num == shard)
        for shard in SHARDS
    ]


if __name__ == "__main__":
    raise SystemExit(main())
