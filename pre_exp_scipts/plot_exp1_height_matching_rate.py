#!/usr/bin/env python3
"""Plot experiment-1 block-height matching rates as a publication-style PNG."""

from __future__ import annotations

import argparse
import csv
from dataclasses import dataclass
from pathlib import Path
from statistics import mean

from PIL import Image, ImageDraw, ImageFont


@dataclass(frozen=True)
class HeightRate:
    height: int
    total_ctx: int
    fully_matched_ctx: int
    matching_rate: float


def parse_args() -> argparse.Namespace:
    script_dir = Path(__file__).resolve().parent
    parser = argparse.ArgumentParser(
        description="Draw a scientific-style bar chart for height_matching_rate.csv.",
    )
    parser.add_argument(
        "--input",
        type=Path,
        default=script_dir / "height_matching_rate.csv",
        help="Path to height_matching_rate.csv.",
    )
    parser.add_argument(
        "--output",
        type=Path,
        default=None,
        help="Output PNG path. Defaults to INPUT_DIR/global_height_matching_rate.png.",
    )
    parser.add_argument(
        "--dpi",
        type=int,
        default=300,
        help="PNG DPI metadata.",
    )
    parser.add_argument(
        "--title",
        default="Block-wise Cross-Shard Transaction Matching Rate under FIFO Netting",
        help="Figure title.",
    )
    return parser.parse_args()


def read_height_rates(path: Path) -> list[HeightRate]:
    rows: list[HeightRate] = []
    with path.open(newline="") as file:
        reader = csv.DictReader(file)
        for row in reader:
            rows.append(
                HeightRate(
                    height=int(row["block_height"]),
                    total_ctx=int(row["total_ctx"]),
                    fully_matched_ctx=int(row["fully_matched_ctx"]),
                    matching_rate=float(row["matching_rate"]),
                )
            )
    if not rows:
        raise ValueError(f"no rows in {path}")
    return rows


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
    xy: tuple[int, int],
    text: str,
    fnt: ImageFont.ImageFont,
    fill: tuple[int, int, int],
) -> None:
    width, height = text_size(draw, text, fnt)
    draw.text((xy[0] - width / 2, xy[1] - height / 2), text, font=fnt, fill=fill)


def draw_right_aligned(
    draw: ImageDraw.ImageDraw,
    xy: tuple[int, int],
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


def draw_dashed_line(
    draw: ImageDraw.ImageDraw,
    xy: tuple[int, int, int, int],
    fill: tuple[int, int, int],
    width: int = 3,
    dash: int = 14,
    gap: int = 9,
) -> None:
    x0, y0, x1, y1 = xy
    if y0 != y1:
        draw.line(xy, fill=fill, width=width)
        return
    x = x0
    while x <= x1:
        draw.line((x, y0, min(x + dash, x1), y1), fill=fill, width=width)
        x += dash + gap


def percent_label(value: float) -> str:
    return f"{value * 100:.0f}%"


def nice_tick_step(count: int) -> int:
    if count <= 18:
        return 1
    if count <= 36:
        return 2
    return 5


def plot_height_matching_rate(rows: list[HeightRate], output: Path, title: str, dpi: int) -> None:
    width, height = 1800, 1080
    margin_left, margin_right = 165, 90
    margin_top, margin_bottom = 150, 175
    plot_x0, plot_y0 = margin_left, margin_top
    plot_x1, plot_y1 = width - margin_right, height - margin_bottom
    plot_w, plot_h = plot_x1 - plot_x0, plot_y1 - plot_y0
    y_min, y_max = 0.50, 1.00
    y_span = y_max - y_min

    bg = (255, 255, 255)
    axis = (20, 20, 20)
    grid = (222, 226, 232)
    bar = (76, 114, 176)
    bar_edge = (42, 72, 112)
    accent = (178, 78, 55)
    text = (20, 20, 20)
    muted = (78, 86, 96)

    image = Image.new("RGB", (width, height), bg)
    draw = ImageDraw.Draw(image)

    title_font = font(38, bold=True)
    axis_font = font(34, bold=True)
    tick_font = font(28)
    note_font = font(24)
    annot_font = font(25)

    draw_centered(draw, ((plot_x0 + plot_x1) // 2, 58), title, title_font, text)

    for tick in [i / 100 for i in range(50, 101, 10)]:
        y = plot_y1 - int(((tick - y_min) / y_span) * plot_h)
        draw.line((plot_x0, y, plot_x1, y), fill=grid, width=2 if tick > 0 else 1)
        draw_right_aligned(draw, (plot_x0 - 18, y), percent_label(tick), tick_font, text)

    draw.line((plot_x0, plot_y1, plot_x1, plot_y1), fill=axis, width=3)
    draw.line((plot_x0, plot_y0, plot_x0, plot_y1), fill=axis, width=3)

    slot = plot_w / len(rows)
    bar_w = min(33, slot * 0.64)
    for idx, row in enumerate(rows):
        x_center = plot_x0 + slot * idx + slot / 2
        x0 = int(x_center - bar_w / 2)
        x1 = int(x_center + bar_w / 2)
        clipped_rate = max(y_min, min(y_max, row.matching_rate))
        y0 = plot_y1 - int(((clipped_rate - y_min) / y_span) * plot_h)
        draw.rectangle((x0, y0, x1, plot_y1), fill=bar, outline=bar_edge, width=2)

    avg = mean(row.matching_rate for row in rows)
    avg_y = plot_y1 - int(((avg - y_min) / y_span) * plot_h)
    draw_dashed_line(draw, (plot_x0, avg_y, plot_x1, avg_y), fill=accent, width=3, dash=18, gap=11)
    draw.text((plot_x1 - 260, avg_y - 45), f"Mean = {avg * 100:.1f}%", font=annot_font, fill=accent)

    tick_step = nice_tick_step(len(rows))
    for idx, row in enumerate(rows):
        if idx % tick_step != 0 and idx != len(rows) - 1:
            continue
        x_center = int(plot_x0 + slot * idx + slot / 2)
        draw.line((x_center, plot_y1, x_center, plot_y1 + 9), fill=axis, width=2)
        draw_centered(draw, (x_center, plot_y1 + 36), str(row.height), tick_font, text)

    draw_centered(draw, ((plot_x0 + plot_x1) // 2, height - 60), "Block height", axis_font, text)
    draw_vertical_centered(image, (58, plot_y0 + plot_h // 2), "Matching rate", axis_font, text)

    total_ctx = sum(row.total_ctx for row in rows)
    fully_matched = sum(row.fully_matched_ctx for row in rows)
    overall = fully_matched / total_ctx if total_ctx else 0
    note = f"Fully matched CTX / total CTX per block; overall = {overall * 100:.1f}% ({fully_matched:,}/{total_ctx:,})."
    draw.text((plot_x0 + 22, plot_y0 + 22), note, font=note_font, fill=muted)

    output.parent.mkdir(parents=True, exist_ok=True)
    image.save(output, format="PNG", dpi=(dpi, dpi))


def main() -> None:
    args = parse_args()
    output = args.output or args.input.with_name("global_height_matching_rate.png")
    rows = read_height_rates(args.input)
    plot_height_matching_rate(rows, output, args.title, args.dpi)
    print(f"wrote {output}")


if __name__ == "__main__":
    main()
