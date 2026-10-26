#!/usr/bin/env python3
"""Plot the repeat-5 eight-shard ablations from a self-contained CSV."""

from __future__ import annotations

import argparse
import csv
from pathlib import Path


HERE = Path(__file__).resolve().parent
DEFAULT_DATA = HERE / "fig_shard8_ablation_compact_repeat5_data.csv"
DEFAULT_OUT = HERE / "fig_shard8_ablation_compact_repeat5"

# Publication style: edit these values to restyle the figure.
FULL_MATCH_COLOR = "#0072B2"
THROUGHPUT_COLOR = "#E69F00"
FIGSIZE = (7.4, 3.1)
FONT_SIZE = 9

PANELS = {
    "injection": ("(a) Injection-rate ablation", "Injection rate (tx/s)"),
    "window": ("(b) Netting-window ablation", "Maximum netting window (ms)"),
}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data", type=Path, default=DEFAULT_DATA)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT)
    parser.add_argument("--dpi", type=int, default=300)
    args = parser.parse_args()

    rows = read_data(args.data)
    make_figure(rows, args.out, args.dpi)
    print(f"generated: {args.out.with_suffix('.png')}")
    print(f"generated: {args.out.with_suffix('.pdf')}")
    return 0


def read_data(path: Path) -> list[dict[str, object]]:
    numeric_fields = (
        "x",
        "full_match_mean",
        "full_match_ci95",
        "throughput_mean_tps",
        "throughput_ci95_tps",
    )
    rows: list[dict[str, object]] = []
    with path.open(newline="", encoding="utf-8") as fp:
        for raw in csv.DictReader(fp):
            row: dict[str, object] = {"experiment": raw["experiment"]}
            row.update({field: float(raw[field]) for field in numeric_fields})
            row["sample_count"] = int(raw["sample_count"])
            rows.append(row)
    if {row["experiment"] for row in rows} != set(PANELS):
        raise SystemExit("CSV must contain both injection and window experiments")
    return rows


def make_figure(rows: list[dict[str, object]], out_stem: Path, dpi: int) -> None:
    plt = pyplot()
    configure_style(plt, dpi)
    fig, axes = plt.subplots(1, 2, figsize=FIGSIZE)
    legend_handles: list[object] = []
    legend_labels: list[str] = []

    for axis, experiment in zip(axes, ("injection", "window")):
        panel_rows = sorted(
            (row for row in rows if row["experiment"] == experiment),
            key=lambda row: float(row["x"]),
        )
        handles, labels = draw_panel(axis, panel_rows, *PANELS[experiment])
        if experiment == "injection":
            legend_handles, legend_labels = handles, labels

    fig.legend(
        legend_handles,
        legend_labels,
        frameon=False,
        loc="upper center",
        bbox_to_anchor=(0.5, 1.01),
        ncol=len(legend_labels),
        columnspacing=1.2,
        handlelength=2.4,
    )
    fig.tight_layout(rect=(0, 0, 1, 0.88), pad=0.8, w_pad=1.4)
    out_stem.parent.mkdir(parents=True, exist_ok=True)
    fig.savefig(out_stem.with_suffix(".png"))
    fig.savefig(out_stem.with_suffix(".pdf"))
    plt.close(fig)


def draw_panel(
    axis: object,
    rows: list[dict[str, object]],
    title: str,
    xlabel: str,
) -> tuple[list[object], list[str]]:
    from matplotlib.ticker import PercentFormatter

    xs = [float(row["x"]) for row in rows]
    full_handle = axis.errorbar(
        xs,
        [float(row["full_match_mean"]) for row in rows],
        yerr=[float(row["full_match_ci95"]) for row in rows],
        color=FULL_MATCH_COLOR,
        marker="o",
        markersize=4.5,
        linewidth=1.7,
        capsize=3,
        label="Fully matched ratio",
        zorder=3,
    )
    throughput_axis = axis.twinx()
    throughput_handle = throughput_axis.errorbar(
        xs,
        [float(row["throughput_mean_tps"]) for row in rows],
        yerr=[float(row["throughput_ci95_tps"]) for row in rows],
        color=THROUGHPUT_COLOR,
        linestyle="--",
        marker="s",
        markersize=4.2,
        linewidth=1.6,
        capsize=3,
        label="Throughput",
        zorder=3,
    )

    handles: list[object] = [full_handle, throughput_handle]
    labels = ["Fully matched ratio", "Throughput"]

    axis.set_title(title, fontsize=10)
    axis.set_xlabel(xlabel)
    axis.set_ylabel("Fully matched transaction ratio", color=FULL_MATCH_COLOR)
    axis.yaxis.set_major_formatter(PercentFormatter(xmax=1.0, decimals=0))
    axis.tick_params(axis="y", labelcolor=FULL_MATCH_COLOR)
    axis.set_xticks(xs)
    axis.margins(x=0.06)
    set_ratio_limits(axis, rows)

    throughput_axis.set_ylabel("Throughput (tx/s)", color=THROUGHPUT_COLOR)
    throughput_axis.tick_params(axis="y", labelcolor=THROUGHPUT_COLOR)
    throughput_axis.grid(False)
    throughput_axis.spines["right"].set_visible(True)
    throughput_axis.margins(y=0.12)
    return handles, labels


def set_ratio_limits(axis: object, rows: list[dict[str, object]]) -> None:
    low = max(
        0.0,
        min(float(row["full_match_mean"]) - float(row["full_match_ci95"]) for row in rows) - 0.03,
    )
    high = min(
        1.0,
        max(float(row["full_match_mean"]) + float(row["full_match_ci95"]) for row in rows) + 0.01,
    )
    if high - low < 0.05:
        low = max(0.0, high - 0.05)
    axis.set_ylim(low, high)


def configure_style(plt: object, dpi: int) -> None:
    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "Times", "DejaVu Serif"],
            "font.size": FONT_SIZE,
            "axes.labelsize": 9.5,
            "xtick.labelsize": 8.5,
            "ytick.labelsize": 8.5,
            "legend.fontsize": 8,
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


def pyplot() -> object:
    try:
        import matplotlib

        matplotlib.use("Agg")
        import matplotlib.pyplot as plt
    except ModuleNotFoundError as err:
        raise SystemExit("Install Matplotlib with: python -m pip install matplotlib>=3.8") from err
    return plt


if __name__ == "__main__":
    raise SystemExit(main())
