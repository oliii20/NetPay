#!/usr/bin/env python3
"""Plot mean TPS and latency with sample-standard-deviation error bars."""

from __future__ import annotations

import argparse
import csv
import math
import statistics
from collections import defaultdict
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[3]
DEFAULT_SESSIONS = REPO_ROOT / "exp/netting-paper/repeated-exp1-scale-4-8-16-24/sessions"
DEFAULT_OUT = Path(__file__).resolve().parent
SHARDS = [4, 8, 16, 24]
METHODS = [
    ("netting_static_relay", "Netting", "#D55E00", ""),
    ("static_relay", "Static Relay", "#0072B2", "//"),
    ("static_broker", "Static Broker", "#009E73", "\\\\"),
    ("clpa_relay", "CLPA Relay", "#CC79A7", "xx"),
]
METRICS = [
    ("throughput_tps", "TPS (tx/s)", "(a) Throughput"),
    ("avg_latency_s", "Average latency (s)", "(b) Average latency"),
]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sessions", type=Path, default=DEFAULT_SESSIONS)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT)
    parser.add_argument("--dpi", type=int, default=300)
    args = parser.parse_args()

    rows = load_summary_rows(args.sessions)
    recovered = recover_missing_relay_rows(args.sessions, rows)
    rows.extend(recovered)
    validate_complete(rows)
    args.out.mkdir(parents=True, exist_ok=True)

    write_samples(rows, args.out / "fig_exp1_scale_tps_latency_samples.csv")
    write_stats(rows, args.out / "fig_exp1_scale_tps_latency_stats.csv")
    outputs = plot_combined(rows, args.out / "fig_exp1_scale_tps_latency_bars", args.dpi)
    for metric, ylabel, title in METRICS:
        outputs.extend(
            plot_single(
                rows,
                metric,
                ylabel,
                title,
                args.out / f"fig_exp1_scale_{'tps' if metric == 'throughput_tps' else 'latency'}_bars",
                args.dpi,
            )
        )

    print(f"summary rows: {len(rows) - len(recovered)}")
    print(f"recovered rows: {len(recovered)}")
    print("figures: " + ", ".join(str(path) for path in outputs))
    return 0


def load_summary_rows(sessions: Path) -> list[dict[str, str]]:
    rows: list[dict[str, str]] = []
    seen: set[tuple[int, int, str]] = set()
    method_keys = {method for method, _, _, _ in METHODS}
    for path in sorted(sessions.glob("*/summary.csv")):
        with path.open(newline="", encoding="utf-8") as fp:
            for row in csv.DictReader(fp):
                if row.get("experiment") != "exp1_scale_repeated" or row.get("method") not in method_keys:
                    continue
                key = (int(row["seed"]), int(row["shard_num"]), row["method"])
                if key in seen:
                    raise ValueError(f"duplicate seed/shard/method row: {key}")
                seen.add(key)
                row["source"] = str(path)
                rows.append(row)
    if not rows:
        raise FileNotFoundError(f"no experiment summaries found under {sessions}")
    return rows


def recover_missing_relay_rows(sessions: Path, rows: list[dict[str, str]]) -> list[dict[str, str]]:
    """Recover completed relay runs omitted from summary.csv after runner interruption."""
    present = {(int(r["seed"]), int(r["shard_num"]), r["method"]) for r in rows}
    recovered: list[dict[str, str]] = []
    for seed in range(1, 6):
        for shard in SHARDS:
            key = (seed, shard, "clpa_relay")
            if key in present:
                continue
            pattern = f"*/runs/repeat_*/exp1_scale_repeated_clpa_relay_shard_num_{shard}_seed{seed}/results"
            matches = sorted(sessions.glob(pattern))
            if len(matches) != 1:
                continue
            results = matches[0]
            brief = read_csv(results / "relay_stats_brief_info.csv")
            detail = read_csv(results / "relay_stats_detail_tx_info.csv")
            committed = [row for row in detail if row.get("Tx finally commit time")]
            latencies = [
                timestamp(row["Tx finally commit time"]) - timestamp(row["Tx create time"])
                for row in committed
            ]
            positive_latencies = [value for value in latencies if value > 0 and math.isfinite(value)]
            epoch_tps = [number(row.get("Avg. TPS of this epoch (txs per second)")) for row in brief]
            recovered.append(
                {
                    "experiment": "exp1_scale_repeated",
                    "method": "clpa_relay",
                    "seed": str(seed),
                    "shard_num": str(shard),
                    # Match run_experiments.py: mean includes zero-TPS trailing epochs.
                    "throughput_tps": str(statistics.mean(epoch_tps)),
                    "avg_latency_s": str(statistics.mean(positive_latencies)),
                    "source": str(results),
                }
            )
    return recovered


def read_csv(path: Path) -> list[dict[str, str]]:
    if not path.is_file():
        return []
    with path.open(newline="", encoding="utf-8") as fp:
        return list(csv.DictReader(fp))


def timestamp(value: str) -> float:
    from datetime import datetime

    return datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()


def number(value: str | None) -> float:
    try:
        result = float(value or 0.0)
    except ValueError:
        return 0.0
    return result if math.isfinite(result) else 0.0


def validate_complete(rows: list[dict[str, str]]) -> None:
    counts: defaultdict[tuple[int, str], int] = defaultdict(int)
    seeds: defaultdict[tuple[int, str], set[int]] = defaultdict(set)
    for row in rows:
        key = (int(row["shard_num"]), row["method"])
        counts[key] += 1
        seeds[key].add(int(row["seed"]))
    problems = []
    for shard in SHARDS:
        for method, _, _, _ in METHODS:
            key = (shard, method)
            if counts[key] != 5 or seeds[key] != {1, 2, 3, 4, 5}:
                problems.append(f"{method}/{shard}: n={counts[key]}, seeds={sorted(seeds[key])}")
    if problems:
        raise ValueError("incomplete repeated data:\n  " + "\n  ".join(problems))


def grouped_values(rows: list[dict[str, str]], metric: str) -> dict[tuple[int, str], list[float]]:
    grouped: defaultdict[tuple[int, str], list[float]] = defaultdict(list)
    for row in rows:
        grouped[(int(row["shard_num"]), row["method"])].append(float(row[metric]))
    return grouped


def summary_stats(rows: list[dict[str, str]], metric: str) -> dict[tuple[int, str], tuple[float, float, int]]:
    return {
        key: (statistics.mean(values), statistics.stdev(values), len(values))
        for key, values in grouped_values(rows, metric).items()
    }


def write_samples(rows: list[dict[str, str]], path: Path) -> None:
    fields = ["seed", "shard_num", "method", "throughput_tps", "avg_latency_s", "source"]
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=fields)
        writer.writeheader()
        for row in sorted(rows, key=lambda r: (int(r["seed"]), int(r["shard_num"]), r["method"])):
            writer.writerow({field: row.get(field, "") for field in fields})


def write_stats(rows: list[dict[str, str]], path: Path) -> None:
    fields = ["shard_num", "method", "metric", "n", "mean", "sample_sd"]
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=fields)
        writer.writeheader()
        for metric, _, _ in METRICS:
            stats = summary_stats(rows, metric)
            for shard in SHARDS:
                for method, _, _, _ in METHODS:
                    mean, sd, count = stats[(shard, method)]
                    writer.writerow(
                        {
                            "shard_num": shard,
                            "method": method,
                            "metric": metric,
                            "n": count,
                            "mean": f"{mean:.10g}",
                            "sample_sd": f"{sd:.10g}",
                        }
                    )


def setup_matplotlib(dpi: int) -> None:
    import matplotlib.pyplot as plt

    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "Times", "DejaVu Serif"],
            "font.size": 9,
            "axes.labelsize": 9.5,
            "axes.titlesize": 10,
            "legend.fontsize": 8.2,
            "xtick.labelsize": 8.5,
            "ytick.labelsize": 8.5,
            "figure.dpi": dpi,
            "savefig.dpi": dpi,
            "savefig.bbox": "tight",
            "axes.spines.top": False,
            "axes.spines.right": False,
            "axes.grid": True,
            "grid.alpha": 0.20,
            "grid.linestyle": "-",
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
        }
    )


def draw_bars(ax: object, rows: list[dict[str, str]], metric: str) -> None:
    import numpy as np

    stats = summary_stats(rows, metric)
    x = np.arange(len(SHARDS))
    group_width = 0.80
    width = group_width / len(METHODS)
    for index, (method, label, color, hatch) in enumerate(METHODS):
        centers = x - group_width / 2 + width * (index + 0.5)
        means = [stats[(shard, method)][0] for shard in SHARDS]
        errors = [stats[(shard, method)][1] for shard in SHARDS]
        ax.bar(
            centers,
            means,
            width=width * 0.90,
            yerr=errors,
            label=label,
            color=color,
            hatch=hatch,
            alpha=0.88,
            edgecolor="#303030",
            linewidth=0.45,
            error_kw={"elinewidth": 0.9, "ecolor": "#222222", "capsize": 2.4, "capthick": 0.9},
            zorder=3,
        )
    ax.set_xticks(x)
    ax.set_xticklabels([str(shard) for shard in SHARDS])
    ax.set_xlabel("Number of shards")
    ax.set_axisbelow(True)
    ax.yaxis.grid(True)
    ax.xaxis.grid(False)
    ax.tick_params(axis="x", length=0)
    ax.margins(x=0.025)


def plot_combined(rows: list[dict[str, str]], stem: Path, dpi: int) -> list[Path]:
    import matplotlib.pyplot as plt

    setup_matplotlib(dpi)
    fig, axes = plt.subplots(1, 2, figsize=(7.15, 2.72))
    for ax, (metric, ylabel, title) in zip(axes, METRICS):
        draw_bars(ax, rows, metric)
        ax.set_ylabel(ylabel)
        ax.set_title(title, pad=5)
    handles, labels = axes[0].get_legend_handles_labels()
    fig.legend(handles, labels, loc="upper center", bbox_to_anchor=(0.5, 1.02), ncol=4, frameon=False)
    fig.tight_layout(rect=(0, 0, 1, 0.90), w_pad=1.6)
    return save(fig, stem)


def plot_single(
    rows: list[dict[str, str]], metric: str, ylabel: str, title: str, stem: Path, dpi: int
) -> list[Path]:
    import matplotlib.pyplot as plt

    setup_matplotlib(dpi)
    fig, ax = plt.subplots(figsize=(4.0, 2.85))
    draw_bars(ax, rows, metric)
    ax.set_ylabel(ylabel)
    ax.set_title(title, pad=5)
    ax.legend(loc="upper center", bbox_to_anchor=(0.5, 1.22), ncol=2, frameon=False, columnspacing=1.0)
    fig.tight_layout(pad=0.6)
    return save(fig, stem)


def save(fig: object, stem: Path) -> list[Path]:
    import matplotlib.pyplot as plt

    outputs = [stem.with_suffix(".pdf"), stem.with_suffix(".png"), stem.with_suffix(".svg")]
    for path in outputs:
        fig.savefig(path)
    plt.close(fig)
    return outputs


if __name__ == "__main__":
    raise SystemExit(main())
