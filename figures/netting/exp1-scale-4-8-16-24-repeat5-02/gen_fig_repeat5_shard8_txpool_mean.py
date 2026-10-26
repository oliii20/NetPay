#!/usr/bin/env python3
"""Plot mean per-shard txpool size across five repeated 8-shard runs."""

from __future__ import annotations

import argparse
import csv
import math
from pathlib import Path

import gen_fig_repeat01_shard8_txpool as base


DEFAULT_OUT = Path(__file__).resolve().parent
SEEDS = (1, 2, 3, 4, 5)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sessions-root", type=Path, default=base.DEFAULT_SESSION.parent)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT)
    parser.add_argument("--sample-seconds", type=float, default=0.5)
    parser.add_argument("--dpi", type=int, default=300)
    return parser.parse_args()


def discover_run(sessions_root: Path, method: str, seed: int) -> Path:
    run_name = f"exp1_scale_repeated_{method}_shard_num_8_seed{seed}"
    candidates = [
        path
        for path in sessions_root.glob(f"*/runs/repeat_*/{run_name}")
        if (path / "block_record" / "shard=0_node=0" / "txpool_record.csv").exists()
    ]
    if len(candidates) != 1:
        raise SystemExit(
            f"expected one complete run for method={method}, seed={seed}; "
            f"found {len(candidates)}: {candidates}"
        )
    return candidates[0]


def load_repeat_curves(
    sessions_root: Path, sample_seconds: float
) -> dict[tuple[str, int], tuple[list[float], list[float]]]:
    curves = {}
    for method, _, _, _ in base.METHODS:
        for seed in SEEDS:
            run_dir = discover_run(sessions_root, method, seed)
            times, system_sizes = base.load_system_curve(run_dir, sample_seconds)
            curves[(method, seed)] = (times, [size / 8.0 for size in system_sizes])
    return curves


def summarize_repeats(
    curves: dict[tuple[str, int], tuple[list[float], list[float]]]
) -> dict[str, list[dict[str, float]]]:
    stats = {}
    for method, _, _, _ in base.METHODS:
        method_curves = [curves[(method, seed)] for seed in SEEDS]
        common_points = min(len(times) for times, _ in method_curves)
        rows = []
        for idx in range(common_points):
            values = [sizes[idx] for _, sizes in method_curves]
            mean = sum(values) / len(values)
            variance = sum((value - mean) ** 2 for value in values) / len(values)
            rows.append(
                {
                    "time_s": method_curves[0][0][idx],
                    "mean": mean,
                    "std": math.sqrt(variance),
                }
            )
        stats[method] = rows
    return stats


def write_csv(path: Path, stats: dict[str, list[dict[str, float]]]) -> None:
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.writer(fp)
        writer.writerow(
            [
                "Method",
                "SamplingTimeSeconds",
                "RepeatCount",
                "MeanTxPoolSizePerShard",
                "StdTxPoolSizePerShard",
            ]
        )
        for method, _, _, _ in base.METHODS:
            for row in stats[method]:
                writer.writerow(
                    [
                        method,
                        f'{row["time_s"]:.3f}',
                        len(SEEDS),
                        f'{row["mean"]:.3f}',
                        f'{row["std"]:.3f}',
                    ]
                )


def plot(path_stem: Path, stats: dict[str, list[dict[str, float]]], dpi: int) -> None:
    import matplotlib

    matplotlib.use("Agg")
    import matplotlib.pyplot as plt

    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "Times", "DejaVu Serif"],
            "font.size": 10,
            "axes.labelsize": 10.5,
            "legend.fontsize": 9,
            "figure.dpi": dpi,
            "savefig.dpi": dpi,
            "savefig.bbox": "tight",
            "axes.spines.top": False,
            "axes.spines.right": False,
            "axes.grid": True,
            "grid.alpha": 0.2,
            "grid.linestyle": "-",
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
        }
    )

    fig, ax = plt.subplots(figsize=(6.4, 3.2))
    for method, label, color, linestyle in base.METHODS:
        rows = stats[method]
        times = [row["time_s"] for row in rows]
        means = [row["mean"] for row in rows]
        stds = [row["std"] for row in rows]
        lower = [max(0.0, mean - std) for mean, std in zip(means, stds)]
        upper = [mean + std for mean, std in zip(means, stds)]
        ax.fill_between(
            times,
            lower,
            upper,
            facecolor=color,
            edgecolor=color,
            alpha=0.26,
            linewidth=0.7,
            zorder=1,
        )
        ax.plot(times, lower, color=color, linewidth=0.65, alpha=0.55, zorder=2)
        ax.plot(times, upper, color=color, linewidth=0.65, alpha=0.55, zorder=2)
        ax.plot(
            times,
            means,
            label=label,
            color=color,
            linestyle=linestyle,
            linewidth=2.0,
            zorder=3,
        )

    ax.set_xlabel("Sampling time (s)")
    ax.set_ylabel("Mean txpool size per shard")
    ax.set_ylim(bottom=0)
    ax.margins(x=0.01)
    ax.xaxis.grid(False)
    ax.legend(ncol=4, frameon=False, loc="upper center", bbox_to_anchor=(0.5, 1.16))
    ax.text(
        0.985,
        0.965,
        "Shaded bands: ±1 SD (n=5)",
        transform=ax.transAxes,
        ha="right",
        va="top",
        fontsize=8,
        color="#555555",
        bbox={"facecolor": "white", "edgecolor": "none", "alpha": 0.82, "pad": 2.0},
    )
    fig.tight_layout(pad=0.55)
    fig.savefig(path_stem.with_suffix(".pdf"))
    fig.savefig(path_stem.with_suffix(".png"))
    plt.close(fig)


def main() -> int:
    args = parse_args()
    if args.sample_seconds <= 0:
        raise SystemExit("sample-seconds must be positive")
    curves = load_repeat_curves(args.sessions_root, args.sample_seconds)
    stats = summarize_repeats(curves)
    args.out.mkdir(parents=True, exist_ok=True)
    stem = args.out / "fig_repeat5_shard8_txpool_mean"
    write_csv(stem.with_suffix(".csv"), stats)
    plot(stem, stats, args.dpi)
    print(f"generated: {stem.with_suffix('.pdf')}")
    print(f"generated: {stem.with_suffix('.png')}")
    print(f"generated: {stem.with_suffix('.csv')}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
