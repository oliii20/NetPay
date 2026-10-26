#!/usr/bin/env python3
"""Generate single-seed scale overview figures from summary.csv."""

from __future__ import annotations

import argparse
import csv
from pathlib import Path


SHARDS = [4, 8, 16, 32]
METHODS = [
    ("netting_static_relay", "Netting", "#D55E00", "o", "-"),
    ("static_relay", "Relay", "#0072B2", "s", "--"),
    ("static_broker", "Broker", "#009E73", "^", "-."),
    ("clpa_broker", "CLPA+Broker", "#CC79A7", "D", ":"),
]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--summary", required=True, type=Path)
    parser.add_argument("--out", required=True, type=Path)
    parser.add_argument("--dpi", type=int, default=300)
    args = parser.parse_args()

    rows = read_rows(args.summary)
    args.out.mkdir(parents=True, exist_ok=True)
    outputs: list[Path] = []
    outputs += plot_methods(rows, "throughput_tps", "Throughput (tx/s)", args.out / "fig_tps", args.dpi)
    outputs += plot_methods(rows, "avg_latency_s", "Average latency (s)", args.out / "fig_avg_latency", args.dpi)
    outputs += plot_netting_rate(
        rows,
        "matched_intent_ratio",
        "Transaction-count matching rate (%)",
        args.out / "fig_matching_rate",
        args.dpi,
    )
    outputs += plot_netting_rate(
        rows,
        "fallback_intent_ratio",
        "Fallback CTX ratio (%)",
        args.out / "fig_fallback_rate",
        args.dpi,
    )
    write_source_data(rows, args.out / "single_seed_overview_data.csv")
    print("figures: " + ", ".join(str(path) for path in outputs))
    return 0


def read_rows(path: Path) -> list[dict[str, str]]:
    with path.open(newline="", encoding="utf-8") as fp:
        rows = list(csv.DictReader(fp))
    expected = {(shard, method) for shard in SHARDS for method, *_ in METHODS}
    present = {(int(row["shard_num"]), row["method"]) for row in rows}
    missing = sorted(expected - present)
    if missing:
        raise SystemExit(f"missing shard/method rows: {missing}")
    return rows


def configure(dpi: int) -> None:
    import matplotlib.pyplot as plt

    plt.rcParams.update({
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
    })


def plot_methods(
    rows: list[dict[str, str]], metric: str, ylabel: str, stem: Path, dpi: int
) -> list[Path]:
    import matplotlib.pyplot as plt

    configure(dpi)
    index = {(int(row["shard_num"]), row["method"]): row for row in rows}
    fig, ax = plt.subplots(figsize=(4.8, 2.85))
    for method, label, color, marker, linestyle in METHODS:
        values = [float(index[(shard, method)][metric]) for shard in SHARDS]
        ax.plot(
            SHARDS,
            values,
            label=label,
            color=color,
            marker=marker,
            linestyle=linestyle,
            linewidth=1.8,
            markersize=4.5,
        )
    ax.set_xlabel("Number of shards")
    ax.set_ylabel(ylabel)
    ax.set_xticks(SHARDS)
    ax.legend(ncol=2, frameon=False)
    ax.xaxis.grid(False)
    ax.tick_params(axis="x", length=0)
    fig.tight_layout(pad=0.6)
    return save(fig, stem)


def plot_netting_rate(
    rows: list[dict[str, str]], metric: str, ylabel: str, stem: Path, dpi: int
) -> list[Path]:
    import matplotlib.pyplot as plt

    configure(dpi)
    values_by_shard = {
        int(row["shard_num"]): float(row[metric]) * 100.0
        for row in rows
        if row["method"] == "netting_static_relay"
    }
    values = [values_by_shard[shard] for shard in SHARDS]
    fig, ax = plt.subplots(figsize=(3.65, 2.75))
    ax.plot(
        SHARDS,
        values,
        color="#D55E00",
        marker="o",
        linewidth=1.8,
        markersize=4.5,
        markerfacecolor="white",
        markeredgewidth=1.2,
    )
    for shard, value in zip(SHARDS, values):
        ax.annotate(
            f"{value:.1f}%",
            (shard, value),
            xytext=(0, 6),
            textcoords="offset points",
            ha="center",
            fontsize=7.5,
        )
    ax.set_xlabel("Number of shards")
    ax.set_ylabel(ylabel)
    ax.set_xticks(SHARDS)
    ax.set_ylim(bottom=0)
    ax.xaxis.grid(False)
    ax.tick_params(axis="x", length=0)
    fig.tight_layout(pad=0.6)
    return save(fig, stem)


def save(fig: object, stem: Path) -> list[Path]:
    import matplotlib.pyplot as plt

    outputs = [stem.with_suffix(".pdf"), stem.with_suffix(".png")]
    for output in outputs:
        fig.savefig(output)
    plt.close(fig)
    return outputs


def write_source_data(rows: list[dict[str, str]], path: Path) -> None:
    fields = [
        "seed",
        "shard_num",
        "method",
        "throughput_tps",
        "avg_latency_s",
        "matched_intent_ratio",
        "fallback_intent_ratio",
    ]
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=fields)
        writer.writeheader()
        writer.writerows({field: row.get(field, "") for field in fields} for row in rows)


if __name__ == "__main__":
    raise SystemExit(main())
