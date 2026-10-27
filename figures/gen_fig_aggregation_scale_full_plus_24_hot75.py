#!/usr/bin/env python3
"""Plot TPS and mean latency for full aggregation and 24-shard 75% aggregation."""

from __future__ import annotations

import csv
from pathlib import Path

import matplotlib.pyplot as plt
import numpy as np


ROOT = Path(__file__).resolve().parents[1]
FULL_DATA = ROOT / "exp/aggregation-scale-beacon1000-repeat3/scaling_runs.csv"
HOT75_DATA = ROOT / "exp/aggregation-24shard-recheck-v1/scaling_runs.csv"
OUT_DIR = ROOT / "figures/aggregation-scale-full-plus-24-hot75"


def read_selected_rows() -> list[dict[str, str]]:
    rows: list[dict[str, str]] = []
    with FULL_DATA.open(newline="", encoding="utf-8") as handle:
        for row in csv.DictReader(handle):
            if int(row["shards"]) in {4, 8, 16} and row["method"] == "full_aggregation":
                rows.append(row)

    with HOT75_DATA.open(newline="", encoding="utf-8") as handle:
        for row in csv.DictReader(handle):
            if int(row["shards"]) == 24 and row["method"] == "hot_destination_75":
                rows.append(row)
    return rows


def summarize(rows: list[dict[str, str]]) -> list[dict[str, float | int | str]]:
    groups: dict[int, list[dict[str, str]]] = {4: [], 8: [], 16: [], 24: []}
    for row in rows:
        groups[int(row["shards"])].append(row)

    summary: list[dict[str, float | int | str]] = []
    for shards in (4, 8, 16, 24):
        group = groups[shards]
        if not group:
            raise ValueError(f"missing selected data for {shards} shards")
        tps = np.asarray([float(row["logical_tps"]) for row in group])
        latency_s = np.asarray([float(row["latency_mean_ms"]) / 1000.0 for row in group])
        summary.append(
            {
                "shards": shards,
                "aggregation": "Full" if shards < 24 else "75% target (78.84% actual)",
                "n": len(group),
                "logical_tps_mean": float(tps.mean()),
                "logical_tps_std": float(tps.std(ddof=1)) if len(tps) > 1 else 0.0,
                "latency_mean_s": float(latency_s.mean()),
                "latency_std_s": float(latency_s.std(ddof=1)) if len(latency_s) > 1 else 0.0,
            }
        )
    return summary


def write_summary(summary: list[dict[str, float | int | str]]) -> None:
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    output = OUT_DIR / "selected_metrics_summary.csv"
    with output.open("w", newline="", encoding="utf-8") as handle:
        writer = csv.DictWriter(handle, fieldnames=list(summary[0]))
        writer.writeheader()
        writer.writerows(summary)


def add_value_labels(ax: plt.Axes, bars, values: np.ndarray, decimals: int) -> None:
    top = max(values)
    for bar, value in zip(bars, values, strict=True):
        ax.text(
            bar.get_x() + bar.get_width() / 2,
            bar.get_height() + top * 0.025,
            f"{value:,.{decimals}f}",
            ha="center",
            va="bottom",
            fontsize=8,
            color="#333333",
        )


def plot(summary: list[dict[str, float | int | str]]) -> None:
    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "DejaVu Serif"],
            "font.size": 10,
            "axes.labelsize": 10,
            "axes.titlesize": 11,
            "axes.titleweight": "bold",
            "axes.spines.top": False,
            "axes.spines.right": False,
            "axes.grid": True,
            "axes.axisbelow": True,
            "grid.alpha": 0.18,
            "grid.linestyle": "-",
            "figure.dpi": 150,
            "savefig.dpi": 300,
            "savefig.bbox": "tight",
        }
    )

    labels = ["4\nFull", "8\nFull", "16\nFull", "24\n75% target"]
    x = np.arange(len(summary))
    colors = ["#0072B2", "#0072B2", "#0072B2", "#E69F00"]
    tps = np.asarray([float(row["logical_tps_mean"]) for row in summary])
    tps_err = np.asarray([float(row["logical_tps_std"]) for row in summary])
    latency = np.asarray([float(row["latency_mean_s"]) for row in summary])
    latency_err = np.asarray([float(row["latency_std_s"]) for row in summary])

    fig, axes = plt.subplots(1, 2, figsize=(7.0, 3.0))
    panels = (
        (axes[0], tps, tps_err, "Logical TPS", "(a) Throughput", 0),
        (axes[1], latency, latency_err, "Mean confirmation latency (s)", "(b) Latency", 2),
    )
    for ax, values, errors, ylabel, title, decimals in panels:
        bars = ax.bar(
            x,
            values,
            yerr=errors,
            capsize=3,
            width=0.66,
            color=colors,
            edgecolor="white",
            linewidth=0.7,
            error_kw={"elinewidth": 1.0, "ecolor": "#444444"},
        )
        add_value_labels(ax, bars, values, decimals)
        ax.set_xticks(x, labels)
        ax.set_xlabel("Number of shards / aggregation scope")
        ax.set_ylabel(ylabel)
        ax.set_title(title, pad=8)
        ax.set_ylim(0, max(values + errors) * 1.20)
        ax.grid(axis="x", visible=False)

    fig.text(
        0.5,
        -0.04,
        "Bars show mean ± SD; n=3 for 4/8/16 shards and n=1 for 24 shards.\n"
        "The 24-shard run aggregated 78.84% of cross-shard transactions (75% target).",
        ha="center",
        va="top",
        fontsize=8,
        color="#444444",
    )
    fig.tight_layout(w_pad=2.0)
    fig.subplots_adjust(bottom=0.27)
    fig.savefig(OUT_DIR / "fig_tps_latency.png")
    fig.savefig(OUT_DIR / "fig_tps_latency.pdf")
    fig.savefig(OUT_DIR / "fig_tps_latency.svg")
    plt.close(fig)


def main() -> None:
    rows = read_selected_rows()
    summary = summarize(rows)
    write_summary(summary)
    plot(summary)


if __name__ == "__main__":
    main()
