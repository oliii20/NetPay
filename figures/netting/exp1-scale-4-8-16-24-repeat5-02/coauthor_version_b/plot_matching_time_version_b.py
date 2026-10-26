#!/usr/bin/env python3
"""Reproduce version B of the matching-time figure from the bundled CSV."""

from __future__ import annotations

import argparse
import csv
import statistics
from pathlib import Path


SHARDS = (4, 8, 16, 24)
REPEATS = (1, 2, 3, 4, 5)
BLUE = "#0072B2"
ORANGE = "#D55E00"


def main() -> int:
    script_dir = Path(__file__).resolve().parent
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data", type=Path, default=script_dir / "matching_time_data.csv")
    parser.add_argument("--out-dir", type=Path, default=script_dir)
    parser.add_argument("--dpi", type=int, default=300)
    args = parser.parse_args()

    samples = load_samples(args.data)
    validate_samples(samples)
    args.out_dir.mkdir(parents=True, exist_ok=True)
    outputs = plot(samples, args.out_dir / "matching_time_version_b", args.dpi)
    print("Generated: " + ", ".join(str(path) for path in outputs))
    return 0


def load_samples(path: Path) -> list[dict[str, int | float]]:
    with path.open(newline="", encoding="utf-8") as fp:
        return [
            {
                "repeat": int(row["repeat"]),
                "shard_num": int(row["shard_num"]),
                "match_time_ms": float(row["match_time_ms"]),
            }
            for row in csv.DictReader(fp)
        ]


def validate_samples(samples: list[dict[str, int | float]]) -> None:
    actual = {(int(row["repeat"]), int(row["shard_num"])) for row in samples}
    expected = {(repeat, shard) for repeat in REPEATS for shard in SHARDS}
    if actual != expected or len(samples) != len(expected):
        raise ValueError(
            f"expected exactly five repeats for shards {SHARDS}; "
            f"missing={sorted(expected - actual)}, extra={sorted(actual - expected)}"
        )


def configure_matplotlib(dpi: int) -> None:
    import matplotlib.pyplot as plt

    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "Times", "DejaVu Serif"],
            "font.size": 9,
            "axes.labelsize": 9.5,
            "legend.fontsize": 8,
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


def plot(
    samples: list[dict[str, int | float]], out_stem: Path, dpi: int
) -> list[Path]:
    import matplotlib.pyplot as plt

    configure_matplotlib(dpi)
    grouped = [
        [
            float(row["match_time_ms"])
            for row in sorted(samples, key=lambda item: int(item["repeat"]))
            if int(row["shard_num"]) == shard
        ]
        for shard in SHARDS
    ]
    means = [statistics.mean(values) for values in grouped]
    jitter = (-0.48, -0.24, 0.0, 0.24, 0.48)

    fig, ax = plt.subplots(figsize=(3.7, 2.8))
    ax.boxplot(
        grouped,
        positions=SHARDS,
        widths=1.55,
        patch_artist=True,
        showfliers=False,
        medianprops={"color": "#264653", "linewidth": 1.5},
        boxprops={"facecolor": "#DCEEF7", "edgecolor": BLUE, "linewidth": 1.2},
        whiskerprops={"color": BLUE, "linewidth": 1.1},
        capprops={"color": BLUE, "linewidth": 1.1},
    )

    individual_runs = None
    for shard, values in zip(SHARDS, grouped):
        points = ax.scatter(
            [shard + offset for offset in jitter],
            values,
            s=21,
            facecolor="white",
            edgecolor=BLUE,
            linewidth=0.9,
            alpha=0.8,
            zorder=3,
        )
        if individual_runs is None:
            individual_runs = points

    mean_line = ax.plot(
        SHARDS,
        means,
        color=ORANGE,
        linewidth=1.8,
        marker="D",
        markersize=4.8,
        markerfacecolor="white",
        markeredgewidth=1.1,
        zorder=4,
    )[0]
    for shard, mean in zip(SHARDS, means):
        ax.annotate(
            f"{mean:.1f}",
            (shard, mean),
            xytext=(0, 7),
            textcoords="offset points",
            ha="center",
            va="bottom",
            fontsize=7.2,
            color="#3F4A54",
            zorder=5,
        )

    ax.set_xlabel("Number of shards")
    ax.set_ylabel("Matching execution time (ms)")
    ax.set_xticks(SHARDS)
    ax.set_xlim(2.5, 25.5)
    ax.set_ylim(bottom=0)
    ax.yaxis.grid(True)
    ax.xaxis.grid(False)
    ax.tick_params(axis="x", length=0)
    ax.legend(
        [mean_line, individual_runs],
        ["Mean", "Individual runs"],
        loc="upper left",
        frameon=False,
        handlelength=2.2,
    )
    fig.tight_layout(pad=0.6)

    outputs = [out_stem.with_suffix(suffix) for suffix in (".pdf", ".png", ".svg")]
    for output in outputs:
        fig.savefig(output)
    plt.close(fig)
    return outputs


if __name__ == "__main__":
    raise SystemExit(main())
