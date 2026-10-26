#!/usr/bin/env python3
"""Plot the five-repeat mean shard-pair full-matching-rate heatmap.

This script is self-contained: it reads only matching_rate_mean.csv from this
folder and does not require the original experiment sessions or repository.
"""

from __future__ import annotations

import argparse
import csv
from pathlib import Path


HERE = Path(__file__).resolve().parent


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, default=HERE / "matching_rate_mean.csv")
    parser.add_argument("--output", type=Path, default=HERE / "shard8_matching_rate_heatmap")
    parser.add_argument("--cmap", default="YlGnBu", help="Matplotlib colormap name")
    parser.add_argument("--vmin", type=float, default=0.70, help="Color-scale minimum")
    parser.add_argument("--vmax", type=float, default=1.00, help="Color-scale maximum")
    parser.add_argument("--cmap-start", type=float, default=0.02)
    parser.add_argument("--cmap-end", type=float, default=0.58)
    parser.add_argument(
        "--neutral-mix",
        type=float,
        default=0.70,
        help="Blend with light neutral gray: 0 keeps original colors, 1 is neutral",
    )
    parser.add_argument("--font-size", type=float, default=10.0)
    parser.add_argument("--width", type=float, default=4.1, help="Figure width in inches")
    parser.add_argument("--height", type=float, default=3.25, help="Figure height in inches")
    parser.add_argument("--dpi", type=int, default=300)
    parser.add_argument("--annotate", action="store_true", help="Print percentages in cells")
    return parser.parse_args()


def load_matrix(path: Path):
    import numpy as np

    records: list[tuple[int, int, float]] = []
    with path.open(newline="", encoding="utf-8") as fp:
        for row in csv.DictReader(fp):
            lower = int(row["LowerShard"])
            higher = int(row["HigherShard"])
            rate = float(row["MeanMatchingRate"])
            if not 0.0 <= rate <= 1.0:
                raise ValueError(f"matching rate outside [0, 1]: {rate}")
            records.append((lower, higher, rate))

    if not records:
        raise ValueError(f"no matching-rate records found in {path}")

    shard_count = max(max(lower, higher) for lower, higher, _ in records) + 1
    matrix = np.full((shard_count, shard_count), np.nan)
    for lower, higher, rate in records:
        matrix[lower, higher] = rate
        matrix[higher, lower] = rate
    return matrix


def make_colormap(name: str, start: float, end: float, neutral_mix: float):
    import matplotlib.pyplot as plt
    import numpy as np
    from matplotlib.colors import LinearSegmentedColormap

    if not 0.0 <= start < end <= 1.0:
        raise ValueError("cmap-start and cmap-end must satisfy 0 <= start < end <= 1")
    if not 0.0 <= neutral_mix <= 1.0:
        raise ValueError("neutral-mix must be within [0, 1]")

    colors = plt.get_cmap(name)(np.linspace(start, end, 256))
    neutral = np.array([0.96, 0.97, 0.98])
    colors[:, :3] = (1.0 - neutral_mix) * colors[:, :3] + neutral_mix * neutral
    cmap = LinearSegmentedColormap.from_list(f"{name}_custom", colors)
    cmap.set_bad("#ECEFF1")
    return cmap


def plot(args: argparse.Namespace) -> tuple[Path, Path]:
    import matplotlib

    matplotlib.use("Agg")
    import matplotlib.pyplot as plt
    import numpy as np
    from matplotlib.ticker import PercentFormatter

    if not args.vmin < args.vmax:
        raise ValueError("vmin must be smaller than vmax")

    matrix = load_matrix(args.input)
    masked = np.ma.masked_invalid(matrix)
    cmap = make_colormap(args.cmap, args.cmap_start, args.cmap_end, args.neutral_mix)

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

    fig, ax = plt.subplots(figsize=(args.width, args.height))
    image = ax.imshow(
        masked,
        cmap=cmap,
        vmin=args.vmin,
        vmax=args.vmax,
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
                if np.isfinite(value):
                    ax.text(
                        column,
                        row,
                        f"{value:.1%}",
                        ha="center",
                        va="center",
                        fontsize=max(5.5, args.font_size - 3.0),
                        color="#263238",
                    )

    colorbar = fig.colorbar(image, ax=ax, fraction=0.047, pad=0.025)
    colorbar.set_label("Average Matching Rate", rotation=90, labelpad=8)
    colorbar.set_ticks(np.linspace(args.vmin, args.vmax, 4))
    colorbar.ax.yaxis.set_major_formatter(PercentFormatter(xmax=1.0, decimals=0))
    colorbar.outline.set_linewidth(0.5)
    fig.tight_layout(pad=0.4)

    output = args.output.resolve()
    if output.suffix.lower() in {".png", ".pdf"}:
        output = output.with_suffix("")
    output.parent.mkdir(parents=True, exist_ok=True)
    pdf_path = output.with_suffix(".pdf")
    png_path = output.with_suffix(".png")
    fig.savefig(pdf_path)
    fig.savefig(png_path)
    plt.close(fig)
    return pdf_path, png_path


def main() -> int:
    args = parse_args()
    pdf_path, png_path = plot(args)
    print(f"generated: {pdf_path}")
    print(f"generated: {png_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
