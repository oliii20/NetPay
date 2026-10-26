#!/usr/bin/env python3
"""Draw matching-rate and transaction-count heatmaps from packaged CSV data."""

from __future__ import annotations

import argparse
import csv
from pathlib import Path


HERE = Path(__file__).resolve().parent


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--matching-rate-input", type=Path, default=HERE / "matching_rate_mean.csv")
    parser.add_argument("--transaction-count-input", type=Path, default=HERE / "transaction_count_mean.csv")
    parser.add_argument("--output-dir", type=Path, default=HERE)
    parser.add_argument("--cmap", default="YlGnBu")
    parser.add_argument("--cmap-start", type=float, default=0.02)
    parser.add_argument("--cmap-end", type=float, default=0.58)
    parser.add_argument(
        "--neutral-mix",
        type=float,
        default=0.70,
        help="Blend with light gray: 0 keeps original colors; 1 is neutral gray",
    )
    parser.add_argument("--matching-vmin", type=float, default=0.70)
    parser.add_argument("--matching-vmax", type=float, default=1.00)
    parser.add_argument("--count-vmin", type=float, default=6000.0)
    parser.add_argument("--count-vmax", type=float, default=12000.0)
    parser.add_argument("--font-size", type=float, default=10.0)
    parser.add_argument("--width", type=float, default=4.1)
    parser.add_argument("--height", type=float, default=3.25)
    parser.add_argument("--dpi", type=int, default=300)
    parser.add_argument("--annotate", action="store_true")
    return parser.parse_args()


def load_symmetric_matrix(path: Path, value_column: str):
    import numpy as np

    records: list[tuple[int, int, float]] = []
    with path.open(newline="", encoding="utf-8") as fp:
        for row in csv.DictReader(fp):
            records.append(
                (int(row["LowerShard"]), int(row["HigherShard"]), float(row[value_column]))
            )
    if not records:
        raise ValueError(f"no records found in {path}")

    shard_count = max(max(lower, higher) for lower, higher, _ in records) + 1
    matrix = np.full((shard_count, shard_count), np.nan)
    for lower, higher, value in records:
        matrix[lower, higher] = value
        matrix[higher, lower] = value
    return matrix


def make_colormap(args: argparse.Namespace):
    import matplotlib.pyplot as plt
    import numpy as np
    from matplotlib.colors import LinearSegmentedColormap

    if not 0 <= args.cmap_start < args.cmap_end <= 1:
        raise ValueError("require 0 <= cmap-start < cmap-end <= 1")
    if not 0 <= args.neutral_mix <= 1:
        raise ValueError("neutral-mix must be within [0, 1]")

    colors = plt.get_cmap(args.cmap)(np.linspace(args.cmap_start, args.cmap_end, 256))
    neutral = np.array([0.96, 0.97, 0.98])
    colors[:, :3] = (
        (1.0 - args.neutral_mix) * colors[:, :3]
        + args.neutral_mix * neutral
    )
    cmap = LinearSegmentedColormap.from_list(f"{args.cmap}_custom", colors)
    cmap.set_bad("#ECEFF1")
    return cmap


def draw_heatmap(
    matrix,
    output_stem: Path,
    colorbar_label: str,
    vmin: float,
    vmax: float,
    is_percent: bool,
    args: argparse.Namespace,
) -> None:
    import matplotlib.pyplot as plt
    import numpy as np
    from matplotlib.ticker import PercentFormatter, StrMethodFormatter

    if not vmin < vmax:
        raise ValueError("color-scale minimum must be smaller than maximum")

    masked = np.ma.masked_invalid(matrix)
    fig, ax = plt.subplots(figsize=(args.width, args.height))
    image = ax.imshow(
        masked,
        cmap=make_colormap(args),
        vmin=vmin,
        vmax=vmax,
        interpolation="nearest",
        aspect="equal",
    )
    shard_count = matrix.shape[0]
    ax.set_xticks(range(shard_count))
    ax.set_yticks(range(shard_count))
    ax.set_xlabel("Shard #")
    ax.set_ylabel("Shard #")
    ax.tick_params(axis="both", which="both", length=0)
    for spine in ax.spines.values():
        spine.set_visible(False)

    if args.annotate:
        for row in range(shard_count):
            for column in range(shard_count):
                value = matrix[row, column]
                if not np.isfinite(value):
                    continue
                label = f"{value:.1%}" if is_percent else f"{value:,.0f}"
                ax.text(
                    column,
                    row,
                    label,
                    ha="center",
                    va="center",
                    fontsize=max(5.5, args.font_size - 3.0),
                    color="#263238",
                )

    colorbar = fig.colorbar(image, ax=ax, fraction=0.047, pad=0.025)
    colorbar.set_label(colorbar_label, rotation=90, labelpad=8)
    colorbar.set_ticks(np.linspace(vmin, vmax, 4))
    if is_percent:
        colorbar.ax.yaxis.set_major_formatter(PercentFormatter(xmax=1.0, decimals=0))
    else:
        colorbar.ax.yaxis.set_major_formatter(StrMethodFormatter("{x:,.0f}"))
    colorbar.outline.set_linewidth(0.5)
    fig.tight_layout(pad=0.4)
    fig.savefig(output_stem.with_suffix(".pdf"))
    fig.savefig(output_stem.with_suffix(".png"))
    plt.close(fig)


def main() -> int:
    args = parse_args()
    import matplotlib

    matplotlib.use("Agg")
    import matplotlib.pyplot as plt

    args.output_dir.mkdir(parents=True, exist_ok=True)
    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "Times", "DejaVu Serif"],
            "font.size": args.font_size,
            "axes.labelsize": args.font_size + 0.5,
            "figure.dpi": args.dpi,
            "savefig.dpi": args.dpi,
            "savefig.bbox": "tight",
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
        }
    )

    matching = load_symmetric_matrix(args.matching_rate_input, "MeanMatchingRate")
    counts = load_symmetric_matrix(args.transaction_count_input, "MeanTransactionCount")
    draw_heatmap(
        matching,
        args.output_dir / "shard8_pair_matching_rate_heatmap",
        "Average Matching Rate",
        args.matching_vmin,
        args.matching_vmax,
        True,
        args,
    )
    draw_heatmap(
        counts,
        args.output_dir / "shard8_pair_transaction_count_heatmap",
        "Average Transaction Count",
        args.count_vmin,
        args.count_vmax,
        False,
        args,
    )
    print(f"generated heatmaps in: {args.output_dir.resolve()}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
