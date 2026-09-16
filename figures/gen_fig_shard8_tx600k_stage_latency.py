#!/usr/bin/env python3
"""Plot stage latency breakdown for the shard8_tx600k-02 pre-experiment.

This script intentionally uses the repository's dependency-free PNG helper so
it can run in the same minimal environment as the experiment scripts.
"""

from __future__ import annotations

import csv
import sys
from collections import defaultdict
from pathlib import Path


REPO = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPO / "scripts/netting_experiments"))

from plot_experiments import PNGFigure  # noqa: E402

SUMMARY = REPO / ".exp/netting-paper/repeated-exp1/sessions/shard8_tx600k-02/summary.csv"
OUT_DIR = REPO / "figures/netting"

METHODS = [
    ("static_relay", "Relay"),
    ("clpa_broker", "BrokerChain"),
    ("netting_static_relay", "Netting"),
]

STAGES = [
    ("stage_queue_s", "Queue", "#B8C0C7"),
    ("stage_clpa_s", "CLPA", "#9A8F5A"),
    ("stage_source_s", "Source", "#7F7F7F"),
    ("stage_coordination_s", "Coord.", "#E9C46A"),
    ("stage_transfer_s", "Transfer", "#4C72B0"),
    ("stage_target_s", "Target", "#2A9D8F"),
    ("stage_window_s", "Window", "#F4A261"),
    ("stage_match_s", "Match", "#55A868"),
    ("stage_beacon_s", "Beacon", "#8172B3"),
    ("stage_settlement_s", "Settle", "#C44E52"),
    ("stage_fallback_s", "Fallback", "#595959"),
]


def read_rows(path: Path) -> list[dict[str, str]]:
    with path.open(newline="", encoding="utf-8") as fp:
        return list(csv.DictReader(fp))


def value(row: dict[str, str], key: str) -> float:
    raw = row.get(key, "")
    try:
        return float(raw) if raw else 0.0
    except ValueError:
        return 0.0


def mean(values: list[float]) -> float:
    return sum(values) / len(values) if values else 0.0


def stage_means(rows: list[dict[str, str]]) -> dict[str, list[float]]:
    grouped: dict[str, list[dict[str, str]]] = defaultdict(list)
    for row in rows:
        if row.get("experiment") == "exp1_baseline":
            grouped[row.get("method", "")].append(row)

    result: dict[str, list[float]] = {}
    for method, _label in METHODS:
        method_rows = grouped.get(method, [])
        result[method] = [
            mean([value(row, stage) for row in method_rows])
            for stage, _stage_label, _color in STAGES
        ]
    return result


def plot() -> None:
	rows = read_rows(SUMMARY)
	means = stage_means(rows)
	labels = [label for _method, label in METHODS]
	stacks = [[means[method][idx] for idx in range(len(STAGES))] for method, _label in METHODS]
	OUT_DIR.mkdir(parents=True, exist_ok=True)
	png_path = OUT_DIR / "fig_shard8_tx600k_stage_latency_breakdown.png"
	svg_path = OUT_DIR / "fig_shard8_tx600k_stage_latency_breakdown.svg"

	chart = PNGFigure(1320, 520)
	chart.title("Stage latency breakdown: 8 shards, 600k tx", 24, 30)
	chart.stacked_bar_panel(
		80,
		82,
		850,
		315,
		labels,
		stacks,
		[label for _field, label, _color in STAGES],
		[color for _field, _label, color in STAGES],
		"Avg stage latency (s)",
		980,
		82,
	)
	chart.save(png_path)
	write_svg(svg_path, labels, stacks)


def write_svg(path: Path, labels: list[str], stacks: list[list[float]]) -> None:
	width, height = 980, 330
	margin_left, margin_top = 145, 54
	bar_h, gap = 46, 26
	plot_w = 720
	totals = [sum(stack) for stack in stacks]
	max_total = max(totals) * 1.12
	parts = [
		f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">',
		'<rect width="100%" height="100%" fill="white"/>',
		'<style>text{font-family:Arial,Helvetica,sans-serif;fill:#222} .small{font-size:12px}.label{font-size:14px;font-weight:600}.title{font-size:18px;font-weight:700}</style>',
		'<text x="24" y="28" class="title">Stage latency breakdown: 8 shards, 600k transactions</text>',
	]
	for row_idx, (label, stack) in enumerate(zip(labels, stacks)):
		y = margin_top + row_idx * (bar_h + gap)
		x = margin_left
		parts.append(f'<text x="{margin_left - 12}" y="{y + 29}" text-anchor="end" class="label">{escape(label)}</text>')
		for value, (_field, stage_label, color) in zip(stack, STAGES):
			w = 0 if max_total == 0 else value / max_total * plot_w
			if w > 0:
				parts.append(f'<rect x="{x:.2f}" y="{y}" width="{w:.2f}" height="{bar_h}" fill="{color}" stroke="white" stroke-width="1"/>')
				if w >= 38:
					parts.append(f'<text x="{x + w / 2:.2f}" y="{y + 29}" text-anchor="middle" class="small" fill="white">{value:.0f}</text>')
			x += w
		parts.append(f'<text x="{x + 8:.2f}" y="{y + 29}" class="label">{sum(stack):.1f}s</text>')
	for tick in range(5):
		value = max_total * tick / 4
		x = margin_left + plot_w * tick / 4
		parts.append(f'<line x1="{x:.2f}" y1="{margin_top - 8}" x2="{x:.2f}" y2="{margin_top + 3 * (bar_h + gap) - gap}" stroke="#E6E6E6"/>')
		parts.append(f'<text x="{x:.2f}" y="{height - 44}" text-anchor="middle" class="small">{value:.0f}</text>')
	parts.append(f'<text x="{margin_left + plot_w / 2}" y="{height - 18}" text-anchor="middle" class="small">Average stage latency per completed cross-shard transaction (s)</text>')
	legend_x, legend_y = 24, height - 86
	x = legend_x
	for _field, stage_label, color in STAGES:
		parts.append(f'<rect x="{x}" y="{legend_y}" width="12" height="12" fill="{color}"/>')
		parts.append(f'<text x="{x + 17}" y="{legend_y + 11}" class="small">{escape(stage_label)}</text>')
		x += 74 if len(stage_label) <= 6 else 92
	parts.append("</svg>")
	path.write_text("\n".join(parts), encoding="utf-8")


def escape(text: str) -> str:
	return text.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")


if __name__ == "__main__":
    plot()
