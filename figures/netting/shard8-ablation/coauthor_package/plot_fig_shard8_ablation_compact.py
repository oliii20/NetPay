#!/usr/bin/env python3
"""Reproduce the compact eight-shard ablation figure from an aggregated CSV."""

from __future__ import annotations

import argparse
import csv
import math
from dataclasses import dataclass
from pathlib import Path


HERE = Path(__file__).resolve().parent
DEFAULT_DATA = HERE / "fig_shard8_ablation_compact_data.csv"
DEFAULT_OUT = HERE / "fig_shard8_ablation_compact"

# Coauthors can adjust the palette, fonts, line styles, and layout here.
FULL_MATCH_COLOR = "#0072B2"
THROUGHPUT_COLOR = "#E69F00"
BASELINE_COLOR = "#333333"
CAPACITY_COLOR = "#777777"
FIGSIZE = (7.4, 3.1)
FONT_SIZE = 9

PANEL_CONFIG = {
    "injection": {
        "title": "(a) Injection-rate ablation",
        "xlabel": "Injection rate (tx/s)",
    },
    "window": {
        "title": "(b) Netting-window ablation",
        "xlabel": "Maximum netting window (ms)",
    },
}


@dataclass(frozen=True)
class Row:
    experiment: str
    x: float
    full_match_mean: float
    full_match_ci95: float
    throughput_mean_tps: float
    throughput_ci95_tps: float
    sample_count: int
    baseline_x: float
    nominal_capacity_x: float | None


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data", type=Path, default=DEFAULT_DATA)
    parser.add_argument(
        "--out",
        type=Path,
        default=DEFAULT_OUT,
        help="output path without an extension; both PNG and PDF are written",
    )
    parser.add_argument("--dpi", type=int, default=300)
    args = parser.parse_args()

    rows = read_rows(args.data)
    plot(rows, args.out, args.dpi)
    print(f"generated: {args.out.with_suffix('.png')}")
    print(f"generated: {args.out.with_suffix('.pdf')}")
    return 0


def read_rows(path: Path) -> list[Row]:
    required = {
        "experiment",
        "x",
        "full_match_mean",
        "full_match_ci95",
        "throughput_mean_tps",
        "throughput_ci95_tps",
        "sample_count",
        "baseline_x",
        "nominal_capacity_x",
    }
    with path.open(newline="", encoding="utf-8") as fp:
        reader = csv.DictReader(fp)
        missing = required - set(reader.fieldnames or [])
        if missing:
            raise SystemExit(f"CSV is missing columns: {sorted(missing)}")
        rows = [parse_row(raw) for raw in reader]

    experiments = {row.experiment for row in rows}
    if experiments != set(PANEL_CONFIG):
        raise SystemExit(f"expected experiments {sorted(PANEL_CONFIG)}, got {sorted(experiments)}")
    for experiment in PANEL_CONFIG:
        xs = [row.x for row in rows if row.experiment == experiment]
        if len(xs) != len(set(xs)):
            raise SystemExit(f"duplicate x value in {experiment}")
    return rows


def parse_row(raw: dict[str, str]) -> Row:
    experiment = raw["experiment"].strip()
    values = {
        field: finite_float(raw[field], field)
        for field in (
            "x",
            "full_match_mean",
            "full_match_ci95",
            "throughput_mean_tps",
            "throughput_ci95_tps",
            "baseline_x",
        )
    }
    nominal_raw = raw["nominal_capacity_x"].strip()
    nominal = finite_float(nominal_raw, "nominal_capacity_x") if nominal_raw else None
    sample_count = int(raw["sample_count"])
    if sample_count <= 0:
        raise SystemExit("sample_count must be positive")
    if not 0 <= values["full_match_mean"] <= 1:
        raise SystemExit("full_match_mean must be a ratio in [0, 1]")
    if values["full_match_ci95"] < 0 or values["throughput_ci95_tps"] < 0:
        raise SystemExit("confidence intervals must be non-negative")
    return Row(
        experiment=experiment,
        x=values["x"],
        full_match_mean=values["full_match_mean"],
        full_match_ci95=values["full_match_ci95"],
        throughput_mean_tps=values["throughput_mean_tps"],
        throughput_ci95_tps=values["throughput_ci95_tps"],
        sample_count=sample_count,
        baseline_x=values["baseline_x"],
        nominal_capacity_x=nominal,
    )


def finite_float(raw: str, field: str) -> float:
    try:
        value = float(raw)
    except ValueError as err:
        raise SystemExit(f"invalid {field}: {raw!r}") from err
    if not math.isfinite(value):
        raise SystemExit(f"non-finite {field}: {value}")
    return value


def plot(rows: list[Row], out_stem: Path, dpi: int) -> None:
    plt = pyplot()
    configure_matplotlib(plt, dpi)
    fig, axes = plt.subplots(1, 2, figsize=FIGSIZE)

    legend_handles: list[object] = []
    legend_labels: list[str] = []
    for axis, experiment in zip(axes, ("injection", "window")):
        handles, labels = draw_panel(
            axis,
            sorted((row for row in rows if row.experiment == experiment), key=lambda row: row.x),
            PANEL_CONFIG[experiment]["title"],
            PANEL_CONFIG[experiment]["xlabel"],
        )
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


def draw_panel(axis: object, rows: list[Row], title: str, xlabel: str) -> tuple[list[object], list[str]]:
    from matplotlib.ticker import PercentFormatter

    xs = [row.x for row in rows]
    full_handle = axis.errorbar(
        xs,
        [row.full_match_mean for row in rows],
        yerr=[row.full_match_ci95 for row in rows],
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
        [row.throughput_mean_tps for row in rows],
        yerr=[row.throughput_ci95_tps for row in rows],
        color=THROUGHPUT_COLOR,
        linestyle="--",
        marker="s",
        markersize=4.2,
        linewidth=1.6,
        capsize=3,
        label="Throughput",
        zorder=3,
    )

    baseline = rows[0].baseline_x
    if any(row.baseline_x != baseline for row in rows):
        raise SystemExit(f"inconsistent baseline_x values for {rows[0].experiment}")
    baseline_handle = axis.axvline(
        baseline,
        color=BASELINE_COLOR,
        linestyle="--",
        linewidth=1.0,
        label="Baseline",
    )
    handles: list[object] = [full_handle, throughput_handle, baseline_handle]
    labels = ["Fully matched ratio", "Throughput", "Baseline"]

    capacity_values = {row.nominal_capacity_x for row in rows}
    if len(capacity_values) != 1:
        raise SystemExit(f"inconsistent nominal_capacity_x values for {rows[0].experiment}")
    capacity = capacity_values.pop()
    if capacity is not None:
        capacity_handle = axis.axvline(
            capacity,
            color=CAPACITY_COLOR,
            linestyle=":",
            linewidth=1.0,
            label="Nominal capacity",
        )
        handles.append(capacity_handle)
        labels.append("Nominal capacity")

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


def set_ratio_limits(axis: object, rows: list[Row]) -> None:
    low = max(0.0, min(row.full_match_mean - row.full_match_ci95 for row in rows) - 0.03)
    high = min(1.0, max(row.full_match_mean + row.full_match_ci95 for row in rows) + 0.01)
    if high - low < 0.05:
        low = max(0.0, high - 0.05)
    axis.set_ylim(low, high)


def configure_matplotlib(plt: object, dpi: int) -> None:
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
        raise SystemExit("Install the dependency with: python -m pip install matplotlib>=3.8") from err
    return plt


if __name__ == "__main__":
    raise SystemExit(main())
