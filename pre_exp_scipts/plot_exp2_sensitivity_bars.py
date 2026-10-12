#!/usr/bin/env python3
"""Plot sensitivity bar charts for shard count and injection rate."""

from __future__ import annotations

import argparse
import csv
from dataclasses import dataclass
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont


@dataclass(frozen=True)
class BarDatum:
    label: str
    value: float
    total_ctx: int
    block_heights: int
    inject_size: int


def parse_args() -> argparse.Namespace:
    script_dir = Path(__file__).resolve().parent
    default_dir = script_dir.parent / "画图_sensitivity"
    parser = argparse.ArgumentParser(description="Draw publication-style sensitivity bar charts.")
    parser.add_argument("--shard-input", type=Path, default=default_dir / "shard_count_sweep.csv")
    parser.add_argument("--inject-input", type=Path, default=default_dir / "inject_size_sweep.csv")
    parser.add_argument("--output-dir", type=Path, default=default_dir)
    parser.add_argument(
        "--metric",
        choices=["avg_height_matching_rate", "global_matching_rate"],
        default="global_matching_rate",
        help="Metric shown on the y-axis.",
    )
    parser.add_argument("--dpi", type=int, default=300)
    return parser.parse_args()


def font(size: int, bold: bool = False):
    candidates = [
        Path("/System/Library/Fonts/Supplemental/Times New Roman Bold.ttf")
        if bold
        else Path("/System/Library/Fonts/Supplemental/Times New Roman.ttf"),
        Path("/System/Library/Fonts/TimesLTMM"),
        Path("/Library/Fonts/Times New Roman.ttf"),
    ]
    for candidate in candidates:
        if candidate.exists():
            try:
                return ImageFont.truetype(str(candidate), size=size)
            except OSError:
                continue
    return ImageFont.load_default()


def text_size(draw: ImageDraw.ImageDraw, text: str, fnt: ImageFont.ImageFont) -> tuple[int, int]:
    box = draw.textbbox((0, 0), text, font=fnt)
    return box[2] - box[0], box[3] - box[1]


def draw_centered(
    draw: ImageDraw.ImageDraw,
    xy: tuple[float, float],
    text: str,
    fnt: ImageFont.ImageFont,
    fill: tuple[int, int, int],
) -> None:
    width, height = text_size(draw, text, fnt)
    draw.text((xy[0] - width / 2, xy[1] - height / 2), text, font=fnt, fill=fill)


def draw_right_aligned(
    draw: ImageDraw.ImageDraw,
    xy: tuple[float, float],
    text: str,
    fnt: ImageFont.ImageFont,
    fill: tuple[int, int, int],
) -> None:
    width, height = text_size(draw, text, fnt)
    draw.text((xy[0] - width, xy[1] - height / 2), text, font=fnt, fill=fill)


def draw_vertical_centered(
    image: Image.Image,
    xy: tuple[int, int],
    text: str,
    fnt: ImageFont.ImageFont,
    fill: tuple[int, int, int],
) -> None:
    dummy = Image.new("RGBA", (1, 1), (255, 255, 255, 0))
    dummy_draw = ImageDraw.Draw(dummy)
    width, height = text_size(dummy_draw, text, fnt)
    label = Image.new("RGBA", (width + 8, height + 8), (255, 255, 255, 0))
    label_draw = ImageDraw.Draw(label)
    label_draw.text((4, 4), text, font=fnt, fill=fill + (255,))
    rotated = label.rotate(90, expand=True)
    image.paste(rotated, (int(xy[0] - rotated.width / 2), int(xy[1] - rotated.height / 2)), rotated)


def read_shard_data(path: Path, metric: str) -> list[BarDatum]:
    rows: list[BarDatum] = []
    with path.open(newline="") as file:
        for row in csv.DictReader(file):
            rows.append(
                BarDatum(
                    label=row["shards"],
                    value=float(row[metric]),
                    total_ctx=int(row["total_ctx"]),
                    block_heights=int(row["block_heights"]),
                    inject_size=int(row["inject_size"]),
                )
            )
    return rows


def read_inject_data(path: Path, metric: str) -> list[BarDatum]:
    rows: list[BarDatum] = []
    with path.open(newline="") as file:
        for row in csv.DictReader(file):
            rows.append(
                BarDatum(
                    label=format_inject_label(int(row["inject_size"])),
                    value=float(row[metric]),
                    total_ctx=int(row["total_ctx"]),
                    block_heights=int(row["block_heights"]),
                    inject_size=int(row["inject_size"]),
                )
            )
    return rows


def format_number(value: int) -> str:
    return f"{value:,}"


def format_inject_label(value: int) -> str:
    if value % 1000 == 0:
        return f"{value // 1000}k"
    return str(value)


def shard_sweep_inject_note(data: list[BarDatum]) -> str:
    values = [item.inject_size for item in data]
    unique = list(dict.fromkeys(values))
    if len(unique) == 1:
        return f"inject size = {format_number(unique[0])}"
    diffs = [later - earlier for earlier, later in zip(values, values[1:])]
    if diffs and len(set(diffs)) == 1 and diffs[0] == 2000:
        return f"inject size = {format_inject_label(values[0])}-{format_inject_label(values[-1])} (+2k per step)"
    return "inject size = " + ", ".join(format_inject_label(value) for value in values)


def percent(value: float) -> str:
    return f"{value * 100:.1f}%"


def nice_y_bounds(values: list[float]) -> tuple[float, float]:
    low = min(values)
    high = max(values)
    lower = max(0.0, (int(low * 100) // 5 * 5 - 5) / 100)
    upper = min(1.0, ((int(high * 100 + 4.999) // 5 * 5) + 5) / 100)
    if upper - lower < 0.10:
        center = (upper + lower) / 2
        lower = max(0.0, center - 0.08)
        upper = min(1.0, center + 0.08)
    return lower, upper


def plot_bar_chart(
    data: list[BarDatum],
    output: Path,
    title: str,
    x_label: str,
    y_label: str,
    note: str,
    dpi: int,
) -> None:
    if not data:
        raise ValueError("no data to plot")

    width, height = 1320, 960
    left, right = 160, 85
    top, bottom = 135, 175
    plot_x0, plot_y0 = left, top
    plot_x1, plot_y1 = width - right, height - bottom
    plot_w, plot_h = plot_x1 - plot_x0, plot_y1 - plot_y0
    y_min, y_max = nice_y_bounds([item.value for item in data])
    y_span = y_max - y_min

    bg = (255, 255, 255)
    axis = (22, 24, 28)
    grid = (224, 228, 235)
    text = (22, 24, 28)
    muted = (83, 91, 105)
    bar_colors = [
        (38, 70, 83),
        (42, 157, 143),
        (233, 196, 106),
        (231, 111, 81),
        (86, 180, 233),
    ]
    edge = (255, 255, 255)

    image = Image.new("RGB", (width, height), bg)
    draw = ImageDraw.Draw(image)

    title_font = font(38, bold=True)
    axis_font = font(32, bold=True)
    tick_font = font(27)
    value_font = font(27, bold=True)
    note_font = font(22)

    draw_centered(draw, ((plot_x0 + plot_x1) / 2, 58), title, title_font, text)

    tick_count = 5
    for i in range(tick_count + 1):
        tick = y_min + (y_span * i / tick_count)
        y = plot_y1 - int(((tick - y_min) / y_span) * plot_h)
        draw.line((plot_x0, y, plot_x1, y), fill=grid, width=2)
        draw_right_aligned(draw, (plot_x0 - 18, y), percent(tick), tick_font, text)

    draw.line((plot_x0, plot_y1, plot_x1, plot_y1), fill=axis, width=3)
    draw.line((plot_x0, plot_y0, plot_x0, plot_y1), fill=axis, width=3)

    slot = plot_w / len(data)
    bar_w = min(145, slot * 0.48)
    for idx, item in enumerate(data):
        x_center = plot_x0 + slot * idx + slot / 2
        x0 = int(x_center - bar_w / 2)
        x1 = int(x_center + bar_w / 2)
        y0 = plot_y1 - int(((item.value - y_min) / y_span) * plot_h)
        color = bar_colors[idx % len(bar_colors)]
        draw.rounded_rectangle((x0, y0, x1, plot_y1), radius=7, fill=color, outline=edge, width=2)
        draw_centered(draw, (x_center, y0 - 24), percent(item.value), value_font, color)
        draw_centered(draw, (x_center, plot_y1 + 36), item.label, tick_font, text)

    draw_centered(draw, ((plot_x0 + plot_x1) / 2, height - 80), x_label, axis_font, text)
    draw_vertical_centered(image, (64, (plot_y0 + plot_y1) // 2), y_label, axis_font, text)
    draw.text((plot_x0, height - 35), note, font=note_font, fill=muted)

    output.parent.mkdir(parents=True, exist_ok=True)
    image.save(output, format="PNG", dpi=(dpi, dpi))
    print(f"wrote {output}")


def main() -> None:
    args = parse_args()
    y_label = "Global matching rate" if args.metric == "global_matching_rate" else "Average matching rate"

    shard_data = read_shard_data(args.shard_input, args.metric)
    plot_bar_chart(
        shard_data,
        args.output_dir / "shard_count_vs_matching_rate.png",
        "Sensitivity to Shard Count",
        "Number of shards",
        y_label,
        f"FIFO split netting; {shard_sweep_inject_note(shard_data)}; "
        "block size = 2,000 per shard; first 500K valid transactions.",
        args.dpi,
    )

    inject_data = read_inject_data(args.inject_input, args.metric)
    plot_bar_chart(
        inject_data,
        args.output_dir / "inject_speed_vs_matching_rate.png",
        "Sensitivity to Transaction Injection Rate",
        "Transactions injected per round",
        y_label,
        "FIFO split netting; shard count = 8; block size = 2,000 per shard; first 500K valid transactions.",
        args.dpi,
    )


if __name__ == "__main__":
    main()
