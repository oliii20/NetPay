#!/usr/bin/env python3
"""Replot 8-shard confirmation-time violins from all 300,000 transactions."""

from __future__ import annotations

import csv
from pathlib import Path

import matplotlib.pyplot as plt
import numpy as np


# ---------------------------------------------------------------------------
# STYLE: coauthors can edit colors, fonts, sizes, spacing, and annotation here.
# ---------------------------------------------------------------------------
METHODS = [
    ("static_relay", "Static Relay", "#9DB7DD"),
    ("static_broker", "Static Broker", "#E8C27D"),
    ("clpa_relay", "CLPA Relay", "#C9ADD8"),
    ("netting_static_relay", "Netting", "#9FD5CB"),
]
FIGSIZE = (7.1, 4.15)
DPI = 300
FONT_FAMILY = "serif"
SERIF_FONTS = ["Times New Roman", "Times", "DejaVu Serif"]
BASE_FONT_SIZE = 10
AXIS_LABEL_SIZE = 12
X_TICK_SIZE = 10.5
Y_TICK_SIZE = 10
VIOLIN_HALF_WIDTH = 0.34
VIOLIN_ALPHA = 0.52
VIOLIN_EDGE_COLOR = "#4A4A4A"
VIOLIN_EDGE_WIDTH = 1.35
SCATTER_SIZE = 5.5
SCATTER_ALPHA = 0.16
SCATTER_COLOR = "#263238"
SCATTER_JITTER = 0.76  # fraction of VIOLIN_HALF_WIDTH
BOX_WIDTH = 0.13
BOX_FACE_COLOR = "white"
BOX_EDGE_COLOR = "#242424"
BOX_ALPHA = 0.90
SHOW_ANNOTATION = True
ANNOTATION_XYTEXT = (3.06, 47.25)
OUTPUT_BASENAME = "shard8_confirmation_time_violin"


ROOT = Path(__file__).resolve().parent
DATA_CSV = ROOT / "confirmation_time_per_transaction.csv"


def main() -> None:
    samples = read_per_transaction(DATA_CSV)
    stats = calculate_stats(samples)
    grid, densities = calculate_densities(samples)
    configure_style()

    fig, ax = plt.subplots(figsize=FIGSIZE)
    rng = np.random.default_rng(20261016)
    for position, (method, label, color) in enumerate(METHODS, start=1):
        density = densities[method]
        peak = max(float(np.max(density)), 1e-12)
        visible = density >= peak * 1e-4
        plot_grid = grid[visible]
        width = VIOLIN_HALF_WIDTH * density[visible] / peak
        ax.fill_betweenx(
            plot_grid,
            position - width,
            position + width,
            facecolor=color,
            edgecolor=VIOLIN_EDGE_COLOR,
            linewidth=VIOLIN_EDGE_WIDTH,
            alpha=VIOLIN_ALPHA,
            zorder=2,
        )
        sample_count = min(500, samples[method].size)
        indices = rng.choice(samples[method].size, size=sample_count, replace=False)
        jitter = rng.uniform(
            -VIOLIN_HALF_WIDTH * SCATTER_JITTER,
            VIOLIN_HALF_WIDTH * SCATTER_JITTER,
            size=sample_count,
        )
        ax.scatter(
            position + jitter,
            samples[method][indices],
            s=SCATTER_SIZE,
            color=SCATTER_COLOR,
            alpha=SCATTER_ALPHA,
            linewidths=0,
            rasterized=True,
            zorder=3,
        )
        draw_boxplot(ax, position, stats[method])
        draw_p95(ax, position, stats[method]["p95_s"])

    if SHOW_ANNOTATION:
        add_annotation(ax, stats)

    upper = float(grid[-1])
    ax.set_xlim(0.45, len(METHODS) + 0.55)
    ax.set_ylim(0, upper)
    ax.set_xticks(range(1, len(METHODS) + 1), [label for _, label, _ in METHODS])
    ax.set_ylabel("Confirmation time (s)")
    ax.yaxis.grid(True, color="#D8D8D8", linewidth=0.65, alpha=0.85)
    ax.xaxis.grid(False)
    ax.set_axisbelow(True)
    for spine in ax.spines.values():
        spine.set_color("#222222")
    fig.tight_layout(pad=0.4)

    for suffix in ("pdf", "png", "svg"):
        output = ROOT / f"{OUTPUT_BASENAME}.{suffix}"
        fig.savefig(output, dpi=DPI if suffix == "png" else None)
        print(output)
    plt.close(fig)


def configure_style() -> None:
    plt.rcParams.update(
        {
            "font.family": FONT_FAMILY,
            "font.serif": SERIF_FONTS,
            "font.size": BASE_FONT_SIZE,
            "axes.labelsize": AXIS_LABEL_SIZE,
            "xtick.labelsize": X_TICK_SIZE,
            "ytick.labelsize": Y_TICK_SIZE,
            "figure.dpi": DPI,
            "savefig.dpi": DPI,
            "savefig.bbox": "tight",
            "savefig.pad_inches": 0.05,
            "axes.linewidth": 0.85,
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
        }
    )


def read_per_transaction(path: Path) -> dict[str, np.ndarray]:
    grouped: dict[str, list[float]] = {method: [] for method, _, _ in METHODS}
    with path.open(newline="", encoding="utf-8") as fp:
        for row in csv.DictReader(fp):
            grouped[row["method"]].append(float(row["confirmation_time_mean_s"]))
    samples = {method: np.asarray(values) for method, values in grouped.items()}
    for method, values in samples.items():
        if values.size != 300_000:
            raise ValueError(f"{method}: expected 300,000 transactions, found {values.size}")
    return samples


def calculate_stats(samples: dict[str, np.ndarray]) -> dict[str, dict[str, float]]:
    result: dict[str, dict[str, float]] = {}
    for method, values in samples.items():
        q1, median, q3 = np.quantile(values, [0.25, 0.50, 0.75])
        iqr = q3 - q1
        inside = values[(values >= q1 - 1.5 * iqr) & (values <= q3 + 1.5 * iqr)]
        result[method] = {
            "q1_s": float(q1),
            "p50_s": float(median),
            "q3_s": float(q3),
            "whislo_s": float(np.min(inside)),
            "whishi_s": float(np.max(inside)),
            "p95_s": float(np.quantile(values, 0.95)),
        }
    return result


def calculate_densities(
    samples: dict[str, np.ndarray],
) -> tuple[np.ndarray, dict[str, np.ndarray]]:
    maximum = max(float(np.max(values)) for values in samples.values())
    upper = np.ceil(maximum / 5.0) * 5.0
    grid = np.linspace(0.0, upper, 801)
    densities = {
        method: histogram_density(values, grid, sigma_s=0.80)
        for method, values in samples.items()
    }
    return grid, densities


def histogram_density(values: np.ndarray, grid: np.ndarray, sigma_s: float) -> np.ndarray:
    edges = np.linspace(grid[0], grid[-1], grid.size + 1)
    counts, _ = np.histogram(values, bins=edges)
    step = float(grid[1] - grid[0])
    sigma_bins = max(sigma_s / step, 1.0)
    radius = max(1, int(np.ceil(4.0 * sigma_bins)))
    offsets = np.arange(-radius, radius + 1, dtype=np.float64)
    kernel = np.exp(-0.5 * (offsets / sigma_bins) ** 2)
    kernel /= np.sum(kernel)
    return np.convolve(counts.astype(np.float64), kernel, mode="same")


def draw_p95(ax: object, position: int, value: float) -> None:
    ax.hlines(
        value,
        position - VIOLIN_HALF_WIDTH * 0.70,
        position + VIOLIN_HALF_WIDTH * 0.70,
        color="#202020",
        linewidth=1.1,
        linestyle=(0, (4, 2)),
        zorder=6,
    )


def draw_boxplot(ax: object, position: int, stats: dict[str, float]) -> None:
    from matplotlib.patches import Rectangle

    q1, median, q3 = stats["q1_s"], stats["p50_s"], stats["q3_s"]
    whislo, whishi = stats["whislo_s"], stats["whishi_s"]
    cap_width = BOX_WIDTH * 0.70
    ax.vlines(position, whislo, q1, color=BOX_EDGE_COLOR, linewidth=0.9, zorder=5)
    ax.vlines(position, q3, whishi, color=BOX_EDGE_COLOR, linewidth=0.9, zorder=5)
    ax.hlines(
        [whislo, whishi],
        position - cap_width / 2,
        position + cap_width / 2,
        color=BOX_EDGE_COLOR,
        linewidth=0.9,
        zorder=5,
    )
    ax.add_patch(
        Rectangle(
            (position - BOX_WIDTH / 2, q1),
            BOX_WIDTH,
            q3 - q1,
            facecolor=BOX_FACE_COLOR,
            edgecolor=BOX_EDGE_COLOR,
            linewidth=0.95,
            alpha=BOX_ALPHA,
            zorder=5,
        )
    )
    ax.hlines(
        median,
        position - BOX_WIDTH / 2,
        position + BOX_WIDTH / 2,
        color="#111111",
        linewidth=1.8,
        zorder=6,
    )


def add_annotation(ax: object, stats: dict[str, dict[str, float]]) -> None:
    baseline_p95 = [stats[method]["p95_s"] for method, _, _ in METHODS[:-1]]
    netting_p95 = stats["netting_static_relay"]["p95_s"]
    text = (
        f"Netting p95: {netting_p95:.1f} s\n"
        f"Baselines: {min(baseline_p95):.1f}–{max(baseline_p95):.1f} s"
    )
    ax.annotate(
        text,
        xy=(len(METHODS), netting_p95),
        xytext=ANNOTATION_XYTEXT,
        ha="center",
        va="center",
        fontsize=BASE_FONT_SIZE,
        bbox={"boxstyle": "round,pad=0.38", "fc": "white", "ec": "#8A8A8A", "lw": 0.8},
        arrowprops={"arrowstyle": "-|>", "color": "#404040", "lw": 0.9},
        zorder=6,
    )


if __name__ == "__main__":
    main()
