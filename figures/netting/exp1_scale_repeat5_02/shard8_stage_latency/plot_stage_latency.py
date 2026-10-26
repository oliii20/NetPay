#!/usr/bin/env python3
"""Draw the 8-shard transaction-stage latency figure from a prepared CSV.

This file is intentionally independent of the BlockEmulator-X repository. Put
it beside ``stage_latency_shard8.csv``, install matplotlib, and run it directly.
Edit the STYLE section below to change colors, fonts, hatches, and dimensions.
"""

from __future__ import annotations

import argparse
import csv
import math
from pathlib import Path

import matplotlib.pyplot as plt
from matplotlib.ticker import MultipleLocator


# ---------------------------------------------------------------------------
# STYLE: coauthors can edit this section without touching data processing.
# ---------------------------------------------------------------------------
FIGSIZE = (7.2, 4.4)
DPI = 300
FONT_FAMILY = "Times New Roman"
BASE_FONT_SIZE = 11
AXIS_LABEL_SIZE = 13
TICK_LABEL_SIZE = 12
VALUE_LABEL_SIZE = 11
LEGEND_FONT_SIZE = 10
BAR_WIDTH = 0.58
EDGE_COLOR = "#4A4A4A"
EDGE_WIDTH = 0.8
GRID_COLOR = "#D9D9D9"
GRID_ALPHA = 0.85

# Stack order is bottom to top. CLPA reconfiguration time is intentionally not
# displayed because it is not a separate transaction-latency stage.
STAGES = [
    ("source_s", "Source", "#8C8C8C", ".."),
    ("matching_s", "Matching", "#E58A55", "//"),
    ("coordination_s", "Coord.", "#D9C27A", "\\\\"),
    ("target_s", "Target", "#5DB0CC", "xx"),
    ("settlement_s", "Settlement", "#C95359", "++"),
    ("fallback_s", "Fallback", "#7A6F9B", "--"),
]


def parse_args() -> argparse.Namespace:
    here = Path(__file__).resolve().parent
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--csv",
        type=Path,
        default=here / "stage_latency_shard8.csv",
        help="prepared stage-latency CSV",
    )
    parser.add_argument(
        "--output-dir",
        type=Path,
        default=here / "output",
        help="directory for PNG/PDF/SVG outputs",
    )
    parser.add_argument("--stem", default="stage_latency_shard8")
    return parser.parse_args()


def read_data(path: Path) -> list[dict[str, str]]:
    with path.open(newline="", encoding="utf-8") as handle:
        rows = list(csv.DictReader(handle))
    if not rows:
        raise ValueError(f"no rows found in {path}")

    required = {"label", "repeat_count", "total_s", *(field for field, *_ in STAGES)}
    missing = required.difference(rows[0])
    if missing:
        raise ValueError(f"missing CSV columns: {', '.join(sorted(missing))}")

    for row in rows:
        stage_total = sum(float(row[field]) for field, *_ in STAGES)
        recorded_total = float(row["total_s"])
        if not math.isclose(stage_total, recorded_total, abs_tol=1e-6):
            raise ValueError(
                f"{row['label']}: stages sum to {stage_total:.9f}, "
                f"but total_s is {recorded_total:.9f}"
            )
    return rows


def nice_y_limit(maximum: float) -> float:
    """Leave headroom while retaining clean five-second boundaries."""
    return math.ceil(maximum * 1.16 / 5.0) * 5.0


def configure_style() -> None:
    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": [FONT_FAMILY, "Times", "DejaVu Serif"],
            "font.size": BASE_FONT_SIZE,
            "axes.labelsize": AXIS_LABEL_SIZE,
            "xtick.labelsize": TICK_LABEL_SIZE,
            "ytick.labelsize": TICK_LABEL_SIZE,
            "legend.fontsize": LEGEND_FONT_SIZE,
            "hatch.linewidth": 0.7,
            "figure.dpi": DPI,
            "savefig.dpi": DPI,
            "savefig.bbox": "tight",
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
        }
    )


def draw(rows: list[dict[str, str]], output_dir: Path, stem: str) -> list[Path]:
    configure_style()
    labels = [row["label"] for row in rows]
    x_positions = list(range(len(rows)))
    totals = [float(row["total_s"]) for row in rows]

    fig, ax = plt.subplots(figsize=FIGSIZE)
    bottoms = [0.0] * len(rows)
    handles = []
    for field, stage_label, color, hatch in STAGES:
        values = [float(row[field]) for row in rows]
        bars = ax.bar(
            x_positions,
            values,
            BAR_WIDTH,
            bottom=bottoms,
            label=stage_label,
            color=color,
            edgecolor=EDGE_COLOR,
            linewidth=EDGE_WIDTH,
            hatch=hatch,
            zorder=3,
        )
        handles.append(bars)
        bottoms = [bottom + value for bottom, value in zip(bottoms, values)]

    y_max = nice_y_limit(max(totals))
    ax.set_ylim(0, y_max)
    ax.yaxis.set_major_locator(MultipleLocator(10))
    ax.set_ylabel("Avg stage latency (s)")
    ax.set_xticks(x_positions, labels)
    ax.grid(axis="y", color=GRID_COLOR, alpha=GRID_ALPHA, linewidth=0.8, zorder=0)
    ax.spines["top"].set_visible(False)
    ax.spines["right"].set_visible(False)

    for x_value, total in zip(x_positions, totals):
        ax.text(
            x_value,
            total + y_max * 0.025,
            f"{total:.1f}",
            ha="center",
            va="bottom",
            fontsize=VALUE_LABEL_SIZE,
        )

    # Reverse legend order so the legend mirrors the stack from top to bottom.
    ax.legend(
        handles=list(reversed(handles)),
        labels=[stage[1] for stage in reversed(STAGES)],
        loc="upper left",
        bbox_to_anchor=(1.01, 1.0),
        frameon=True,
        facecolor="white",
        edgecolor="#999999",
        borderaxespad=0.0,
    )

    repeat_counts = {int(row["repeat_count"]) for row in rows}
    if len(repeat_counts) == 1:
        repeat_count = next(iter(repeat_counts))
        ax.text(
            1.0,
            -0.16,
            f"8 shards; mean over {repeat_count} runs",
            transform=ax.transAxes,
            ha="right",
            va="top",
            fontsize=BASE_FONT_SIZE - 1,
            color="#555555",
        )

    fig.subplots_adjust(right=0.73, bottom=0.20)
    output_dir.mkdir(parents=True, exist_ok=True)
    outputs = [output_dir / f"{stem}.{suffix}" for suffix in ("png", "pdf", "svg")]
    for output in outputs:
        fig.savefig(output)
    plt.close(fig)
    return outputs


def main() -> int:
    args = parse_args()
    outputs = draw(read_data(args.csv), args.output_dir, args.stem)
    for output in outputs:
        print(output)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
