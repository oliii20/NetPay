#!/usr/bin/env python3
"""Plot the revised 8-shard stage-latency breakdown.

Source and Target retain their protocol definitions for every method. CLPA
reconfiguration time is not subtracted from either stage and is not displayed
as a separate transaction-latency stage. Per-round CLPA control-plane timings
are exported separately for diagnostics only.
"""

from __future__ import annotations

import argparse
import csv
import math
import sys
from collections import defaultdict
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont
from reportlab.pdfgen import canvas


REPO = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPO / "scripts/netting_experiments"))

import plot_hotspot_hybrid_repeated as stage_plotter  # noqa: E402


DEFAULT_SUMMARY = REPO / (
    "exp/netting-paper/repeated-exp1-scale-4-8-16-24/sessions/"
    "exp1-scale-4-8-16-24-repeat5-02/summary.csv"
)
DEFAULT_OUT = REPO / "figures/netting/exp1_scale_repeat5_02"

METHODS = [
    ("static_relay", "Monoxide"),
    ("static_broker", "BrokerChain"),
    ("clpa_relay", "CLPA"),
    ("netting_static_relay", "Netting"),
]

STAGES = [
    ("source_s", "Source", "#8C8C8C", "dots"),
    ("matching_s", "Matching", "#E58A55", "slash"),
    ("coordination_s", "Coord.", "#D9C27A", "backslash"),
    ("target_s", "Target", "#5DB0CC", "cross"),
    ("settlement_s", "Settlement", "#C95359", "grid"),
    ("fallback_s", "Fallback", "#7A6F9B", "horizontal"),
]


def read_rows(path: Path) -> list[dict[str, str]]:
    with path.open(newline="", encoding="utf-8") as handle:
        return list(csv.DictReader(handle))


def stages_for_row(summary: Path, row: dict[str, str]) -> dict[str, float]:
    """Return mutually exclusive display stages for one completed run."""
    if row.get("method") == "clpa_relay":
        return clpa_transaction_stages(summary, row)
    if row.get("method") == "netting_static_relay":
        return netting_transaction_stages(summary, row)

    raw = stage_plotter.all_tx_stage_stack(summary, row)
    return {
        "source_s": raw.get("stage_source_s", 0.0),
        "matching_s": 0.0,
        "coordination_s": raw.get("stage_coordination_s", 0.0),
        "target_s": raw.get("stage_transfer_s", 0.0) + raw.get("stage_target_s", 0.0),
        "settlement_s": raw.get("stage_settlement_s", 0.0),
        "fallback_s": raw.get("stage_fallback_s", 0.0),
    }


def clpa_transaction_stages(summary: Path, row: dict[str, str]) -> dict[str, float]:
    results = stage_plotter.resolve_run_dir(summary, row) / "results"
    totals = {field: 0.0 for field, _label, _color, _pattern in STAGES}
    count = 0
    for tx in stage_plotter.read_rows_if_exists(results / "relay_stats_detail_tx_info.csv"):
        created = tx.get("Tx create time", "")
        completed = tx.get("Tx finally commit time", "")
        if not created or not completed:
            continue
        count += 1
        source_end = tx.get("Relay1 tx commit time", "")
        if tx.get("Is cross-shard tx or not") != "true" or not source_end:
            totals["source_s"] += stage_plotter.parse_duration_s(created, completed)
            continue

        totals["source_s"] += stage_plotter.parse_duration_s(created, source_end)
        totals["target_s"] += stage_plotter.parse_duration_s(source_end, completed)

    if count == 0:
        return totals
    return {field: max(0.0, value / count) for field, value in totals.items()}


def netting_transaction_stages(summary: Path, row: dict[str, str]) -> dict[str, float]:
    results = stage_plotter.resolve_run_dir(summary, row) / "results"
    totals = {field: 0.0 for field, _label, _color, _pattern in STAGES}

    relay = stage_plotter.relay_detail_stage_totals(results / "relay_stats_detail_tx_info.csv")
    totals["source_s"] += relay.totals["stage_source_s"]
    totals["target_s"] += relay.totals["stage_transfer_s"] + relay.totals["stage_target_s"]
    count = relay.count

    batches = {
        item.get("BatchID", ""): item
        for item in stage_plotter.read_rows_if_exists(results / "netting_batch_metrics.csv")
    }
    for intent in stage_plotter.read_rows_if_exists(results / "netting_intent_metrics.csv"):
        e2e_s = stage_plotter.float_or_zero(intent.get("EndToEndLatencyNs")) / 1e9
        if e2e_s <= 0:
            continue
        count += 1
        batch = batches.get(intent.get("BatchID", ""), {})
        source_s = stage_plotter.float_or_zero(intent.get("ReservationLatencyNs")) / 1e9
        matching_s = sum(
            stage_plotter.float_or_zero(batch.get(field)) / 1e9
            for field in (
                "WindowOpenDurationNs",
                "MatchTimeNs",
                "MerkleBuildTimeNs",
                "BeaconConsensusLatencyNs",
            )
        )
        totals["source_s"] += source_s
        totals["matching_s"] += matching_s
        completion_tail_s = max(0.0, e2e_s - source_s - matching_s)
        if intent.get("UsedFallback", "").strip().lower() == "true":
            totals["fallback_s"] += completion_tail_s
        else:
            totals["settlement_s"] += completion_tail_s

    if count == 0:
        return totals
    return {field: value / count for field, value in totals.items()}


def collect_means(
    summary: Path, shard_num: int = 8
) -> tuple[dict[str, dict[str, float]], dict[str, int]]:
    selected = [row for row in read_rows(summary) if row.get("shard_num") == str(shard_num)]
    grouped: dict[str, list[dict[str, float]]] = defaultdict(list)
    for row in selected:
        if row.get("method") in dict(METHODS):
            grouped[row["method"]].append(stages_for_row(summary, row))

    missing = [method for method, _label in METHODS if not grouped[method]]
    if missing:
        raise ValueError(f"missing {shard_num}-shard rows for: {', '.join(missing)}")

    means: dict[str, dict[str, float]] = {}
    counts: dict[str, int] = {}
    for method, _label in METHODS:
        runs = grouped[method]
        counts[method] = len(runs)
        means[method] = {
            field: sum(run[field] for run in runs) / len(runs)
            for field, _stage_label, _color, _pattern in STAGES
        }
    return means, counts


def write_data_csv(
    path: Path,
    means: dict[str, dict[str, float]],
    counts: dict[str, int],
) -> None:
    fields = [field for field, _label, _color, _pattern in STAGES]
    with path.open("w", newline="", encoding="utf-8") as handle:
        writer = csv.writer(handle)
        writer.writerow(["method", "label", "repeat_count", *fields, "total_s"])
        for method, label in METHODS:
            values = [means[method][field] for field in fields]
            writer.writerow([method, label, counts[method], *[f"{value:.9f}" for value in values], f"{sum(values):.9f}"])


def nice_limit(value: float) -> float:
    raw = value * 1.16
    magnitude = 10 ** math.floor(math.log10(raw))
    step = magnitude / 2
    return math.ceil(raw / step) * step


def axis_ticks(y_max: float) -> list[float]:
    raw_step = y_max / 3.5
    magnitude = 10 ** math.floor(math.log10(raw_step))
    normalized = raw_step / magnitude
    multiplier = 1 if normalized <= 1 else 2 if normalized <= 2 else 5 if normalized <= 5 else 10
    step = multiplier * magnitude
    return [index * step for index in range(math.floor(y_max / step) + 1)]


def load_font(size: int, bold: bool = False) -> ImageFont.FreeTypeFont | ImageFont.ImageFont:
    candidates = [
        Path("/System/Library/Fonts/Supplemental/Times New Roman Bold.ttf" if bold else "/System/Library/Fonts/Supplemental/Times New Roman.ttf"),
        Path("/System/Library/Fonts/Supplemental/Times Bold.ttf" if bold else "/System/Library/Fonts/Times.ttc"),
    ]
    for candidate in candidates:
        if candidate.exists():
            try:
                return ImageFont.truetype(str(candidate), size=size)
            except OSError:
                pass
    return ImageFont.load_default()


def draw_pattern(
    image: Image.Image,
    box: tuple[float, float, float, float],
    pattern: str,
    scale: float = 1.0,
) -> None:
    left, top, right, bottom = [int(round(value)) for value in box]
    patch = Image.new("RGBA", (max(1, right - left + 1), max(1, bottom - top + 1)), (0, 0, 0, 0))
    draw = ImageDraw.Draw(patch)
    patch_right, patch_bottom = patch.width - 1, patch.height - 1
    ink = "#555555"
    gap = max(8, int(22 * scale))
    width = max(1, int(2 * scale))
    if pattern in {"slash", "cross"}:
        for offset in range(-patch_bottom, patch_right + gap, gap):
            draw.line((offset, patch_bottom, offset + patch_bottom, 0), fill=ink, width=width)
    if pattern in {"backslash", "cross", "grid"}:
        for offset in range(0, patch_right + patch_bottom + gap, gap):
            draw.line((offset, 0, offset - patch_bottom, patch_bottom), fill=ink, width=width)
    if pattern == "grid":
        for x in range(gap // 2, patch_right, gap):
            draw.line((x, 0, x, patch_bottom), fill=ink, width=width)
    if pattern == "horizontal":
        for y in range(gap // 2, patch_bottom, gap):
            draw.line((0, y, patch_right, y), fill=ink, width=width)
    if pattern == "dots":
        radius = max(1, int(2.5 * scale))
        for y in range(gap // 2, patch_bottom, gap):
            for x in range(gap // 2, patch_right, gap):
                draw.ellipse((x - radius, y - radius, x + radius, y + radius), outline=ink, width=width)
    image.paste(patch, (left, top), patch)


def draw_png(
    path: Path,
    means: dict[str, dict[str, float]],
    shard_num: int,
    repeat_count: int,
) -> None:
    width, height = 2100, 1320
    image = Image.new("RGB", (width, height), "white")
    draw = ImageDraw.Draw(image)
    plot = (230, 105, 1560, 1030)
    max_total = max(sum(means[method].values()) for method, _label in METHODS)
    y_max = nice_limit(max_total)

    font_tick = load_font(52)
    font_axis = load_font(66)
    font_label = load_font(54)
    font_total = load_font(56)
    font_legend = load_font(44)
    font_note = load_font(42)

    left, top, right, bottom = plot
    for value in axis_ticks(y_max):
        y = bottom - (bottom - top) * value / y_max
        draw.line((left, y, right, y), fill="#DDDDDD", width=3)
        label = f"{value:g}"
        bbox = draw.textbbox((0, 0), label, font=font_tick)
        draw.text((left - 30 - (bbox[2] - bbox[0]), y - (bbox[3] - bbox[1]) / 2), label, fill="#202020", font=font_tick)

    draw.line((left, top, left, bottom), fill="#222222", width=4)
    draw.line((left, bottom, right, bottom), fill="#222222", width=4)

    centers = [left + (right - left) * (index + 0.5) / len(METHODS) for index in range(len(METHODS))]
    bar_width = 190
    for center, (method, label) in zip(centers, METHODS):
        cumulative = 0.0
        for field, _stage_label, color, pattern in STAGES:
            value = means[method][field]
            y_bottom = bottom - (bottom - top) * cumulative / y_max
            cumulative += value
            y_top = bottom - (bottom - top) * cumulative / y_max
            box = (center - bar_width / 2, y_top, center + bar_width / 2, y_bottom)
            draw.rectangle(box, fill=color, outline="#454545", width=3)
            draw_pattern(image, box, pattern, 1.0)
        total_label = f"{cumulative:.1f}"
        bbox = draw.textbbox((0, 0), total_label, font=font_total)
        draw.text((center - (bbox[2] - bbox[0]) / 2, bottom - (bottom - top) * cumulative / y_max - 70), total_label, fill="#111111", font=font_total)
        bbox = draw.textbbox((0, 0), label, font=font_label)
        draw.text((center - (bbox[2] - bbox[0]) / 2, bottom + 25), label, fill="#111111", font=font_label)

    y_label = "Avg stage latency (s)"
    temp = Image.new("RGBA", (900, 110), (255, 255, 255, 0))
    temp_draw = ImageDraw.Draw(temp)
    temp_draw.text((0, 0), y_label, fill="#111111", font=font_axis)
    rotated = temp.rotate(90, expand=True)
    image.paste(rotated, (35, int((top + bottom - rotated.height) / 2)), rotated)

    legend_x, legend_y = 1515, 125
    legend_order = list(reversed(STAGES))
    draw.rounded_rectangle((1480, 85, 2055, 770), radius=18, fill="#FAFAFA", outline="#999999", width=3)
    for index, (_field, stage_label, color, pattern) in enumerate(legend_order):
        y = legend_y + index * 88
        box = (legend_x, y, legend_x + 110, y + 54)
        draw.rectangle(box, fill=color, outline="#454545", width=3)
        draw_pattern(image, box, pattern, 1.0)
        draw.text((legend_x + 135, y + 1), stage_label, fill="#111111", font=font_legend)

    note = f"{shard_num} shards; mean over {repeat_count} runs"
    draw.text((right - 470, bottom + 150), note, fill="#555555", font=font_note)
    image.save(path, dpi=(300, 300))


def svg_pattern(pattern: str, color: str) -> str:
    body = f'<rect width="100%" height="100%" fill="{color}"/>'
    if pattern == "dots":
        body += '<circle cx="5" cy="5" r="1.2" fill="none" stroke="#555" stroke-width="0.8"/>'
    elif pattern == "slash":
        body += '<path d="M-2,10 L10,-2 M6,14 L14,6" stroke="#555" stroke-width="1"/>'
    elif pattern == "backslash":
        body += '<path d="M-2,-2 L10,10 M6,-2 L14,6" stroke="#555" stroke-width="1"/>'
    elif pattern == "cross":
        body += '<path d="M-2,10 L10,-2 M-2,-2 L10,10 M6,14 L14,6 M6,-2 L14,6" stroke="#555" stroke-width="0.9"/>'
    elif pattern == "horizontal":
        body += '<path d="M0,5 H10" stroke="#555" stroke-width="1"/>'
    else:
        body += '<path d="M-2,-2 L10,10 M6,-2 L14,6 M5,0 V10" stroke="#555" stroke-width="0.9"/>'
    return body


def write_clpa_control_csv(summary: Path, path: Path, shard_num: int) -> None:
    with path.open("w", newline="", encoding="utf-8") as handle:
        writer = csv.writer(handle)
        writer.writerow([
            "repeat",
            "epoch",
            "partition_s",
            "broadcast_s",
            "migration_sync_s",
            "total_reconfiguration_s",
            "migrated_accounts",
        ])
        for row in read_rows(summary):
            if row.get("method") != "clpa_relay" or row.get("shard_num") != str(shard_num):
                continue
            results = stage_plotter.resolve_run_dir(summary, row) / "results"
            for metric in stage_plotter.read_rows_if_exists(results / "clpa_repartition_metrics.csv"):
                writer.writerow([
                    row.get("repeat", ""),
                    metric.get("Epoch", ""),
                    f"{stage_plotter.float_or_zero(metric.get('Partition latency ns')) / 1e9:.9f}",
                    f"{stage_plotter.float_or_zero(metric.get('Broadcast latency ns')) / 1e9:.9f}",
                    f"{stage_plotter.float_or_zero(metric.get('Migration sync latency ns')) / 1e9:.9f}",
                    f"{stage_plotter.float_or_zero(metric.get('Total latency ns')) / 1e9:.9f}",
                    metric.get("Migrated account count", ""),
                ])


def write_definitions(path: Path) -> None:
    path.write_text(
        """# Stage latency definitions

All plotted values are seconds per completed original transaction. Durations are
computed within each repeat and then repeats are averaged with equal weight.

- **Monoxide / Source:** transaction creation to Relay1 commit (or final commit for
  an intra-shard transaction).
- **Monoxide / Target:** Relay1 commit to final Relay2 commit; this includes relay
  transfer and target-shard consensus.
- **BrokerChain / Source:** transaction creation to Broker1 commit.
- **BrokerChain / Coordination:** Broker1 commit to Broker2 creation.
- **BrokerChain / Target:** Broker2 creation to Broker2/final commit.
- **CLPA / Source:** transaction creation to Relay1 commit (or final commit for
  an intra-shard transaction). CLPA reconfiguration time is not subtracted.
- **CLPA / Target:** Relay1 commit to final commit. CLPA reconfiguration time is
  not subtracted.
- **Netting / Source:** transaction creation to reservation commit. Intra-shard
  transactions use creation to final commit.
- **Netting / Matching:** window-open duration + matching time + Merkle-build
  time + beacon consensus latency.
- **Netting / Settlement:** for intents with `UsedFallback=false`, end-to-end
  latency minus Source minus Matching; zero for fallback intents.
- **Netting / Fallback:** for intents with `UsedFallback=true`, end-to-end
  latency minus Source minus Matching; zero for normally settled intents.

Settlement and Fallback are summed over their respective intents but divided by
the count of all completed original transactions. They are therefore additive
contributions to the overall per-transaction mean, not conditional means.

The companion `*_clpa_control.csv` preserves full per-round control-plane time
for diagnostics only. No CLPA time is displayed in the transaction-stage plot.
""",
        encoding="utf-8",
    )


def draw_svg(path: Path, means: dict[str, dict[str, float]], shard_num: int, repeat_count: int) -> None:
    width, height = 980, 610
    left, top, right, bottom = 115, 45, 735, 475
    y_max = nice_limit(max(sum(means[method].values()) for method, _label in METHODS))
    parts = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">',
        '<rect width="100%" height="100%" fill="white"/>',
        '<defs>',
    ]
    for field, _label, color, pattern in STAGES:
        parts.append(f'<pattern id="{field}" width="10" height="10" patternUnits="userSpaceOnUse">{svg_pattern(pattern, color)}</pattern>')
    parts.extend([
        '</defs>',
        '<style>text{font-family:"Times New Roman",Times,serif;fill:#111}.tick{font-size:19px}.axis{font-size:24px}.xlabel{font-size:25px}.total{font-size:21px}.legend{font-size:16px}</style>',
    ])
    for value in axis_ticks(y_max):
        y = bottom - (bottom - top) * value / y_max
        parts.append(f'<line x1="{left}" y1="{y:.2f}" x2="{right}" y2="{y:.2f}" stroke="#DDD"/>')
        parts.append(f'<text x="{left - 14}" y="{y + 6:.2f}" text-anchor="end" class="tick">{value:g}</text>')
    parts.append(f'<line x1="{left}" y1="{top}" x2="{left}" y2="{bottom}" stroke="#222" stroke-width="1.5"/>')
    parts.append(f'<line x1="{left}" y1="{bottom}" x2="{right}" y2="{bottom}" stroke="#222" stroke-width="1.5"/>')

    centers = [left + (right - left) * (index + 0.5) / len(METHODS) for index in range(len(METHODS))]
    bar_width = 82
    for center, (method, label) in zip(centers, METHODS):
        cumulative = 0.0
        for field, _stage_label, _color, _pattern in STAGES:
            value = means[method][field]
            y_bottom = bottom - (bottom - top) * cumulative / y_max
            cumulative += value
            y_top = bottom - (bottom - top) * cumulative / y_max
            parts.append(f'<rect x="{center - bar_width / 2:.2f}" y="{y_top:.2f}" width="{bar_width}" height="{y_bottom - y_top:.2f}" fill="url(#{field})" stroke="#444" stroke-width="0.8"/>')
        parts.append(f'<text x="{center:.2f}" y="{bottom - (bottom - top) * cumulative / y_max - 10:.2f}" text-anchor="middle" class="total">{cumulative:.1f}</text>')
        parts.append(f'<text x="{center:.2f}" y="{bottom + 35}" text-anchor="middle" class="xlabel">{label}</text>')

    parts.append(f'<text transform="translate(32 {(top + bottom) / 2}) rotate(-90)" text-anchor="middle" class="axis">Avg stage latency (s)</text>')
    legend_x, legend_y = 765, 74
    parts.append('<rect x="745" y="48" width="225" height="354" rx="8" fill="#FAFAFA" stroke="#999"/>')
    for index, (field, stage_label, _color, _pattern) in enumerate(reversed(STAGES)):
        y = legend_y + index * 47
        parts.append(f'<rect x="{legend_x}" y="{y}" width="45" height="28" fill="url(#{field})" stroke="#444"/>')
        parts.append(f'<text x="{legend_x + 57}" y="{y + 21}" class="legend">{stage_label}</text>')
    parts.append(f'<text x="{right}" y="{bottom + 78}" text-anchor="end" class="tick" fill="#555">{shard_num} shards; mean over {repeat_count} runs</text>')
    parts.append('</svg>')
    path.write_text("\n".join(parts), encoding="utf-8")


def draw_pdf(path: Path, means: dict[str, dict[str, float]], shard_num: int, repeat_count: int) -> None:
    page_w, page_h = 6.75 * 72, 4.2 * 72
    pdf = canvas.Canvas(str(path), pagesize=(page_w, page_h))
    left, top, right, bottom = 60, page_h - 20, 365, 52
    y_max = nice_limit(max(sum(means[method].values()) for method, _label in METHODS))
    pdf.setFont("Times-Roman", 9)
    for value in axis_ticks(y_max):
        y = bottom + (top - bottom) * value / y_max
        pdf.setStrokeColorRGB(0.86, 0.86, 0.86)
        pdf.line(left, y, right, y)
        pdf.setFillColorRGB(0.1, 0.1, 0.1)
        pdf.drawRightString(left - 7, y - 3, f"{value:g}")
    pdf.setStrokeColorRGB(0.12, 0.12, 0.12)
    pdf.line(left, bottom, right, bottom)
    pdf.line(left, bottom, left, top)

    centers = [left + (right - left) * (index + 0.5) / len(METHODS) for index in range(len(METHODS))]
    bar_width = 39
    for center, (method, label) in zip(centers, METHODS):
        cumulative = 0.0
        for field, _stage_label, color, _pattern in STAGES:
            value = means[method][field]
            y0 = bottom + (top - bottom) * cumulative / y_max
            cumulative += value
            y1 = bottom + (top - bottom) * cumulative / y_max
            red, green, blue = tuple(int(color[index:index + 2], 16) / 255 for index in (1, 3, 5))
            pdf.setFillColorRGB(red, green, blue)
            pdf.setStrokeColorRGB(0.27, 0.27, 0.27)
            pdf.rect(center - bar_width / 2, y0, bar_width, y1 - y0, fill=1, stroke=1)
        pdf.setFillColorRGB(0.05, 0.05, 0.05)
        pdf.setFont("Times-Roman", 10)
        pdf.drawCentredString(center, bottom + (top - bottom) * cumulative / y_max + 5, f"{cumulative:.1f}")
        pdf.setFont("Times-Roman", 11)
        pdf.drawCentredString(center, bottom - 17, label)

    pdf.saveState()
    pdf.translate(17, (top + bottom) / 2)
    pdf.rotate(90)
    pdf.setFont("Times-Roman", 11)
    pdf.drawCentredString(0, 0, "Avg stage latency (s)")
    pdf.restoreState()

    legend_x, legend_y = 375, top - 15
    pdf.setFont("Times-Roman", 8.5)
    for index, (_field, stage_label, color, _pattern) in enumerate(reversed(STAGES)):
        y = legend_y - index * 24
        red, green, blue = tuple(int(color[pos:pos + 2], 16) / 255 for pos in (1, 3, 5))
        pdf.setFillColorRGB(red, green, blue)
        pdf.rect(legend_x, y, 27, 13, fill=1, stroke=1)
        pdf.setFillColorRGB(0.05, 0.05, 0.05)
        pdf.drawString(legend_x + 31, y + 2, stage_label)
    pdf.setFillColorRGB(0.3, 0.3, 0.3)
    pdf.setFont("Times-Roman", 8)
    pdf.drawRightString(right, 12, f"{shard_num} shards; mean over {repeat_count} runs")
    pdf.showPage()
    pdf.save()


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--summary", type=Path, default=DEFAULT_SUMMARY)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT)
    parser.add_argument("--shard-num", type=int, default=8)
    args = parser.parse_args()

    means, counts = collect_means(args.summary, args.shard_num)
    if len(set(counts.values())) != 1:
        raise SystemExit(f"inconsistent repeat counts: {counts}")
    repeat_count = next(iter(counts.values()))
    args.out.mkdir(parents=True, exist_ok=True)
    stem = args.out / f"fig_exp1_scale_shard{args.shard_num}_stage_latency_revised"
    write_data_csv(stem.with_name(stem.name + "_data.csv"), means, counts)
    write_clpa_control_csv(
        args.summary,
        stem.with_name(stem.name + "_clpa_control.csv"),
        args.shard_num,
    )
    write_definitions(stem.with_name(stem.name + "_definitions.md"))
    draw_png(stem.with_suffix(".png"), means, args.shard_num, repeat_count)
    draw_svg(stem.with_suffix(".svg"), means, args.shard_num, repeat_count)
    draw_pdf(stem.with_suffix(".pdf"), means, args.shard_num, repeat_count)
    for suffix in (".png", ".svg", ".pdf", "_data.csv", "_clpa_control.csv", "_definitions.md"):
        print(stem.with_name(stem.name + suffix) if suffix.startswith("_") else stem.with_suffix(suffix))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
