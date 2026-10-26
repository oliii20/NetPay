#!/usr/bin/env python3
"""Plot the five-repeat mean transaction count for every undirected shard pair."""

from __future__ import annotations

import argparse
import csv
import math
from collections import defaultdict
from pathlib import Path


HERE = Path(__file__).resolve().parent
DEFAULT_INPUT = HERE / "fig_repeat5_shard8_pair_matching_rate_heatmap_per_repeat.csv"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", type=Path, default=DEFAULT_INPUT)
    parser.add_argument("--out", type=Path, default=HERE)
    parser.add_argument("--dpi", type=int, default=300)
    return parser.parse_args()


def aggregate_counts(path: Path) -> dict[tuple[int, int], dict[str, float]]:
    samples: dict[tuple[int, int], list[int]] = defaultdict(list)
    with path.open(newline="", encoding="utf-8") as fp:
        for row in csv.DictReader(fp):
            pair = (int(row["LowerShard"]), int(row["HigherShard"]))
            samples[pair].append(int(row["TotalIntents"]))

    if not samples:
        raise SystemExit(f"no shard-pair counts found in {path}")

    aggregate = {}
    repeat_counts = {len(values) for values in samples.values()}
    if len(repeat_counts) != 1:
        raise SystemExit(f"inconsistent repeat counts across shard pairs: {repeat_counts}")
    for pair, values in samples.items():
        mean = sum(values) / len(values)
        variance = sum((value - mean) ** 2 for value in values) / len(values)
        aggregate[pair] = {
            "repeat_count": float(len(values)),
            "mean": mean,
            "std": math.sqrt(variance),
        }
    return aggregate


def write_csv(path: Path, aggregate: dict[tuple[int, int], dict[str, float]]) -> None:
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.writer(fp)
        writer.writerow(
            [
                "LowerShard",
                "HigherShard",
                "RepeatCount",
                "MeanTransactionCount",
                "StdTransactionCount",
            ]
        )
        for pair in sorted(aggregate):
            row = aggregate[pair]
            writer.writerow(
                [
                    pair[0],
                    pair[1],
                    int(row["repeat_count"]),
                    f'{row["mean"]:.3f}',
                    f'{row["std"]:.3f}',
                ]
            )


def plot_heatmap(
    stem: Path,
    aggregate: dict[tuple[int, int], dict[str, float]],
    dpi: int,
) -> None:
    import matplotlib

    matplotlib.use("Agg")
    import matplotlib.pyplot as plt
    import numpy as np
    from matplotlib.colors import LinearSegmentedColormap
    from matplotlib.ticker import StrMethodFormatter

    shard_count = max(max(pair) for pair in aggregate) + 1
    matrix = np.full((shard_count, shard_count), np.nan)
    for (lower, higher), values in aggregate.items():
        matrix[lower, higher] = values["mean"]
        matrix[higher, lower] = values["mean"]
    masked = np.ma.masked_invalid(matrix)

    vmin = 6000.0
    vmax = math.ceil(float(masked.max()) / 1000.0) * 1000.0

    base_cmap = plt.get_cmap("YlGnBu")
    colors = base_cmap(np.linspace(0.02, 0.58, 256))
    neutral = np.array([0.96, 0.97, 0.98])
    colors[:, :3] = 0.30 * colors[:, :3] + 0.70 * neutral
    cmap = LinearSegmentedColormap.from_list("YlGnBu_pastel", colors)
    cmap.set_bad("#ECEFF1")

    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "Times", "DejaVu Serif"],
            "font.size": 10,
            "axes.labelsize": 10.5,
            "figure.dpi": dpi,
            "savefig.dpi": dpi,
            "savefig.bbox": "tight",
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
        }
    )

    fig, ax = plt.subplots(figsize=(4.1, 3.25))
    image = ax.imshow(
        masked,
        cmap=cmap,
        vmin=vmin,
        vmax=vmax,
        interpolation="nearest",
        aspect="equal",
    )
    ax.set_xticks(range(shard_count))
    ax.set_yticks(range(shard_count))
    ax.set_xlabel("Shard #")
    ax.set_ylabel("Shard #")
    ax.tick_params(axis="both", which="both", length=0)
    for spine in ax.spines.values():
        spine.set_visible(False)

    colorbar = fig.colorbar(image, ax=ax, fraction=0.047, pad=0.025)
    colorbar.set_label("Average Transaction Count", rotation=90, labelpad=8)
    colorbar.set_ticks(np.linspace(vmin, vmax, 4))
    colorbar.ax.yaxis.set_major_formatter(StrMethodFormatter("{x:,.0f}"))
    colorbar.outline.set_linewidth(0.5)
    fig.tight_layout(pad=0.4)
    fig.savefig(stem.with_suffix(".pdf"))
    fig.savefig(stem.with_suffix(".png"))
    plt.close(fig)


def main() -> int:
    args = parse_args()
    aggregate = aggregate_counts(args.input)
    args.out.mkdir(parents=True, exist_ok=True)
    stem = args.out / "fig_repeat5_shard8_pair_transaction_count_heatmap"
    write_csv(stem.with_suffix(".csv"), aggregate)
    plot_heatmap(stem, aggregate, args.dpi)
    print(f"generated: {stem.with_suffix('.pdf')}")
    print(f"generated: {stem.with_suffix('.png')}")
    print(f"generated: {stem.with_suffix('.csv')}")
    print(
        "count range: "
        f"{min(row['mean'] for row in aggregate.values()):.3f}--"
        f"{max(row['mean'] for row in aggregate.values()):.3f}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
