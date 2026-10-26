#!/usr/bin/env python3
"""Plot matching-algorithm execution time for five repeated scale runs."""

from __future__ import annotations

import argparse
import csv
import math
import statistics
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[3]
DEFAULT_SESSIONS = REPO_ROOT / "exp/netting-paper/repeated-exp1-scale-4-8-16-24/sessions"
DEFAULT_OUT = Path(__file__).resolve().parent
SHARDS = (4, 8, 16, 24)
SEEDS = (1, 2, 3, 4, 5)
LINE_COLOR = "#0072B2"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sessions", type=Path, default=DEFAULT_SESSIONS)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT)
    parser.add_argument("--dpi", type=int, default=300)
    args = parser.parse_args()

    samples = load_samples(args.sessions)
    validate_complete(samples)
    args.out.mkdir(parents=True, exist_ok=True)

    data_path = args.out / "fig_exp1_scale_matching_time_samples.csv"
    stats_path = args.out / "fig_exp1_scale_matching_time_stats.csv"
    write_samples(samples, data_path)
    stats = summarize(samples)
    write_stats(stats, stats_path)
    outputs = plot_trend(
        samples,
        stats,
        args.out / "fig_exp1_scale_matching_time_a_trend",
        args.dpi,
    )
    outputs.extend(
        plot_distribution(
            samples,
            stats,
            args.out / "fig_exp1_scale_matching_time_b_distribution",
            args.dpi,
        )
    )

    print(f"samples: {len(samples)}")
    print(f"data: {data_path}")
    print(f"stats: {stats_path}")
    print("figures: " + ", ".join(str(path) for path in outputs))
    return 0


def load_samples(sessions: Path) -> list[dict[str, int | float | str]]:
    samples: list[dict[str, int | float | str]] = []
    seen: set[tuple[int, int]] = set()
    for path in sorted(sessions.glob("*/summary.csv")):
        with path.open(newline="", encoding="utf-8") as fp:
            for row in csv.DictReader(fp):
                if (
                    row.get("experiment") != "exp1_scale_repeated"
                    or row.get("method") != "netting_static_relay"
                    or row.get("netting_enabled", "").lower() != "true"
                ):
                    continue
                seed = int(row["seed"])
                shard_num = int(row["shard_num"])
                if seed not in SEEDS or shard_num not in SHARDS:
                    continue
                key = (seed, shard_num)
                if key in seen:
                    raise ValueError(f"duplicate seed/shard sample: {key}")
                seen.add(key)
                match_time_ms = float(row["match_time_ms"])
                if not math.isfinite(match_time_ms) or match_time_ms < 0:
                    raise ValueError(f"invalid match_time_ms for {key}: {row['match_time_ms']!r}")
                samples.append(
                    {
                        "seed": seed,
                        "shard_num": shard_num,
                        "match_time_ms": match_time_ms,
                        "source": str(path.resolve()),
                    }
                )
    if not samples:
        raise FileNotFoundError(f"no repeated netting summaries found under {sessions}")
    return sorted(samples, key=lambda sample: (int(sample["seed"]), int(sample["shard_num"])))


def validate_complete(samples: list[dict[str, int | float | str]]) -> None:
    actual = {(int(sample["seed"]), int(sample["shard_num"])) for sample in samples}
    expected = {(seed, shard) for seed in SEEDS for shard in SHARDS}
    missing = sorted(expected - actual)
    extra = sorted(actual - expected)
    if missing or extra:
        raise ValueError(f"incomplete scale data: missing={missing}, extra={extra}")


def write_samples(samples: list[dict[str, int | float | str]], path: Path) -> None:
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=("seed", "shard_num", "match_time_ms", "source"))
        writer.writeheader()
        writer.writerows(samples)


def summarize(samples: list[dict[str, int | float | str]]) -> list[dict[str, int | float]]:
    stats: list[dict[str, int | float]] = []
    for shard in SHARDS:
        values = [
            float(sample["match_time_ms"])
            for sample in samples
            if int(sample["shard_num"]) == shard
        ]
        stats.append(
            {
                "shard_num": shard,
                "n": len(values),
                "mean_ms": statistics.mean(values),
                "sample_sd_ms": statistics.stdev(values),
                "min_ms": min(values),
                "max_ms": max(values),
            }
        )
    return stats


def write_stats(stats: list[dict[str, int | float]], path: Path) -> None:
    fields = ("shard_num", "n", "mean_ms", "sample_sd_ms", "min_ms", "max_ms")
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=fields)
        writer.writeheader()
        for row in stats:
            writer.writerow(
                {
                    field: f"{row[field]:.10g}" if isinstance(row[field], float) else row[field]
                    for field in fields
                }
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
            "lines.linewidth": 1.6,
            "lines.markersize": 4.5,
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
        }
    )


def plot_trend(
    samples: list[dict[str, int | float | str]],
    stats: list[dict[str, int | float]],
    out_stem: Path,
    dpi: int,
) -> list[Path]:
    import matplotlib.pyplot as plt

    configure_matplotlib(dpi)
    means = [float(row["mean_ms"]) for row in stats]
    deviations = [float(row["sample_sd_ms"]) for row in stats]

    fig, ax = plt.subplots(figsize=(3.7, 2.8))
    for seed in SEEDS:
        seed_samples = [sample for sample in samples if int(sample["seed"]) == seed]
        values = [float(sample["match_time_ms"]) for sample in seed_samples]
        ax.plot(
            SHARDS,
            values,
            color=LINE_COLOR,
            linewidth=1.0,
            alpha=0.18,
            marker="o",
            markersize=3.5,
            markerfacecolor="white",
            markeredgewidth=0.9,
            zorder=1,
        )
    mean_line = ax.errorbar(
        SHARDS,
        means,
        yerr=deviations,
        color=LINE_COLOR,
        linewidth=2.2,
        marker="o",
        markersize=5.5,
        markerfacecolor="white",
        markeredgewidth=1.4,
        capsize=4,
        capthick=1.1,
        elinewidth=1.1,
        label="Mean ± SD",
        zorder=3,
    )
    for shard, mean, deviation in zip(SHARDS, means, deviations):
        ax.annotate(
            f"{mean:.1f}",
            (shard, mean + deviation),
            xytext=(0, 5),
            textcoords="offset points",
            ha="center",
            va="bottom",
            fontsize=7.2,
            color="#3F4A54",
            zorder=4,
        )

    ax.set_xlabel("Number of shards")
    ax.set_ylabel("Matching execution time (ms)")
    ax.set_xticks(SHARDS)
    ax.set_xlim(3, 25)
    ax.set_ylim(bottom=0)
    ax.yaxis.grid(True)
    ax.xaxis.grid(False)
    ax.tick_params(axis="x", length=0)
    ax.legend(loc="upper left", frameon=False, handlelength=2.3)
    fig.tight_layout(pad=0.6)

    return save_figure(fig, out_stem)


def plot_distribution(
    samples: list[dict[str, int | float | str]],
    stats: list[dict[str, int | float]],
    out_stem: Path,
    dpi: int,
) -> list[Path]:
    import matplotlib.pyplot as plt

    configure_matplotlib(dpi)
    grouped = [
        [
            float(sample["match_time_ms"])
            for sample in samples
            if int(sample["shard_num"]) == shard
        ]
        for shard in SHARDS
    ]
    means = [float(row["mean_ms"]) for row in stats]
    jitter = (-0.48, -0.24, 0.0, 0.24, 0.48)

    fig, ax = plt.subplots(figsize=(3.7, 2.8))
    boxes = ax.boxplot(
        grouped,
        positions=SHARDS,
        widths=1.55,
        patch_artist=True,
        showfliers=False,
        medianprops={"color": "#264653", "linewidth": 1.5},
        boxprops={"facecolor": "#DCEEF7", "edgecolor": LINE_COLOR, "linewidth": 1.2},
        whiskerprops={"color": LINE_COLOR, "linewidth": 1.1},
        capprops={"color": LINE_COLOR, "linewidth": 1.1},
    )
    del boxes
    raw_label = None
    for shard, values in zip(SHARDS, grouped):
        raw_label = ax.scatter(
            [shard + offset for offset in jitter],
            values,
            s=21,
            facecolor="white",
            edgecolor=LINE_COLOR,
            linewidth=0.9,
            alpha=0.8,
            label="Individual runs" if raw_label is None else None,
            zorder=3,
        )
    mean_line = ax.plot(
        SHARDS,
        means,
        color="#D55E00",
        linewidth=1.8,
        marker="D",
        markersize=4.8,
        markerfacecolor="white",
        markeredgewidth=1.1,
        label="Mean",
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
        [mean_line, raw_label],
        ["Mean", "Individual runs"],
        loc="upper left",
        frameon=False,
        handlelength=2.2,
    )
    fig.tight_layout(pad=0.6)
    return save_figure(fig, out_stem)


def save_figure(fig: object, out_stem: Path) -> list[Path]:
    import matplotlib.pyplot as plt

    outputs = [out_stem.with_suffix(suffix) for suffix in (".pdf", ".png", ".svg")]
    for output in outputs:
        fig.savefig(output)
    plt.close(fig)
    return outputs


if __name__ == "__main__":
    raise SystemExit(main())
