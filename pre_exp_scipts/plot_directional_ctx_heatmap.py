#!/usr/bin/env python3
"""Plot a directional shard-to-shard CTX count heatmap."""

from __future__ import annotations

import argparse
import csv
from pathlib import Path
from typing import Optional

from PIL import Image, ImageDraw, ImageFont


def parse_args() -> argparse.Namespace:
    script_dir = Path(__file__).resolve().parent
    parser = argparse.ArgumentParser(description="Draw a shard-to-shard CTX count heatmap.")
    parser.add_argument(
        "--input",
        type=Path,
        default=script_dir / "directional_pair_ctx_counts.csv",
        help="Path to directional_pair_ctx_counts.csv.",
    )
    parser.add_argument(
        "--output",
        type=Path,
        default=None,
        help="Output PNG path. Defaults to INPUT_DIR/directional_ctx_count_heatmap.png.",
    )
    parser.add_argument(
        "--title",
        default="Directional Cross-Shard Transaction Counts",
        help="Figure title.",
    )
    parser.add_argument("--dpi", type=int, default=300, help="PNG DPI metadata.")
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


def read_matrix(path: Path) -> tuple[list[int], list[list[Optional[int]]]]:
    rows: list[dict[str, str]]
    with path.open(newline="") as file:
        rows = list(csv.DictReader(file))
    if not rows:
        raise ValueError(f"no rows in {path}")

    shard_ids = sorted(
        {
            int(row["source_shard"])
            for row in rows
        }
        | {
            int(row["destination_shard"])
            for row in rows
        }
    )
    index = {shard: idx for idx, shard in enumerate(shard_ids)}
    matrix: list[list[Optional[int]]] = [[None for _ in shard_ids] for _ in shard_ids]
    for row in rows:
        source = int(row["source_shard"])
        destination = int(row["destination_shard"])
        matrix[index[source]][index[destination]] = int(row["total_ctx"])
    return shard_ids, matrix


def blend(low: tuple[int, int, int], high: tuple[int, int, int], value: float) -> tuple[int, int, int]:
    value = max(0.0, min(1.0, value))
    return tuple(int(low[i] + (high[i] - low[i]) * value) for i in range(3))


def fmt_count(value: int) -> str:
    if value >= 1000:
        return f"{value / 1000:.1f}k"
    return str(value)


def plot_heatmap(shards: list[int], matrix: list[list[Optional[int]]], output: Path, title: str, dpi: int) -> None:
    n = len(shards)
    width, height = 1500, 1320
    left, top = 230, 170
    cell = 105
    gap = 6
    grid_w = n * cell
    grid_h = n * cell

    bg = (255, 255, 255)
    text = (20, 20, 20)
    muted = (80, 86, 96)
    axis = (34, 34, 34)
    diag = (236, 238, 241)
    low = (239, 246, 255)
    high = (32, 87, 147)

    values = [value for row in matrix for value in row if value is not None]
    min_value = min(values)
    max_value = max(values)
    span = max(1, max_value - min_value)

    image = Image.new("RGB", (width, height), bg)
    draw = ImageDraw.Draw(image)

    title_font = font(42, bold=True)
    axis_font = font(34, bold=True)
    tick_font = font(28)
    cell_font = font(24, bold=True)
    note_font = font(24)

    draw_centered(draw, (width / 2, 70), title, title_font, text)
    draw_centered(draw, (left + grid_w / 2, top - 55), "Destination shard", axis_font, text)
    draw_vertical_centered(image, (70, top + grid_h // 2), "Source shard", axis_font, text)

    for col, shard in enumerate(shards):
        x = left + col * cell + cell / 2
        draw_centered(draw, (x, top - 18), f"S{shard}", tick_font, axis)
    for row, shard in enumerate(shards):
        y = top + row * cell + cell / 2
        draw_centered(draw, (left - 42, y), f"S{shard}", tick_font, axis)

    for row in range(n):
        for col in range(n):
            x0 = left + col * cell + gap / 2
            y0 = top + row * cell + gap / 2
            x1 = left + (col + 1) * cell - gap / 2
            y1 = top + (row + 1) * cell - gap / 2
            value = matrix[row][col]
            if value is None:
                fill = diag
                label = "-"
                label_color = muted
            else:
                fill = blend(low, high, (value - min_value) / span)
                label = fmt_count(value)
                label_color = (255, 255, 255) if (value - min_value) / span > 0.58 else text
            draw.rounded_rectangle((x0, y0, x1, y1), radius=9, fill=fill, outline=(255, 255, 255), width=2)
            draw_centered(draw, ((x0 + x1) / 2, (y0 + y1) / 2), label, cell_font, label_color)

    # Color bar.
    bar_x0 = left + grid_w + 85
    bar_y0 = top
    bar_w = 34
    bar_h = grid_h
    for i in range(bar_h):
        ratio = 1 - i / max(1, bar_h - 1)
        color = blend(low, high, ratio)
        draw.line((bar_x0, bar_y0 + i, bar_x0 + bar_w, bar_y0 + i), fill=color, width=1)
    draw.rectangle((bar_x0, bar_y0, bar_x0 + bar_w, bar_y0 + bar_h), outline=axis, width=2)
    draw.text((bar_x0 + 52, bar_y0 - 8), fmt_count(max_value), font=tick_font, fill=text)
    draw.text((bar_x0 + 52, bar_y0 + bar_h - 25), fmt_count(min_value), font=tick_font, fill=text)
    draw_vertical_centered(image, (bar_x0 + 130, bar_y0 + bar_h // 2), "CTX count", tick_font, text)

    note = "Cell (Si, Sj) reports the number of valid cross-shard transactions from source shard Si to destination shard Sj."
    draw.text((left, height - 70), note, font=note_font, fill=muted)

    output.parent.mkdir(parents=True, exist_ok=True)
    image.save(output, format="PNG", dpi=(dpi, dpi))


def main() -> None:
    args = parse_args()
    output = args.output or args.input.with_name("directional_ctx_count_heatmap.png")
    shards, matrix = read_matrix(args.input)
    plot_heatmap(shards, matrix, output, args.title, args.dpi)
    print(f"wrote {output}")


if __name__ == "__main__":
    main()
