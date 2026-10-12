#!/usr/bin/env python3
"""Plot an undirected shard-pair bidirectional netting-potential heatmap."""

from __future__ import annotations

import argparse
import csv
from dataclasses import dataclass
from pathlib import Path
from typing import Optional

from PIL import Image, ImageDraw, ImageFont


@dataclass(frozen=True)
class PairPotential:
    lower: int
    higher: int
    lower_to_higher: int
    higher_to_lower: int
    total: int
    nettable_count: int
    potential: float


def parse_args() -> argparse.Namespace:
    script_dir = Path(__file__).resolve().parent
    parser = argparse.ArgumentParser(description="Draw a bidirectional netting-potential heatmap.")
    parser.add_argument(
        "--input",
        type=Path,
        default=script_dir / "bidirectional_pair_ctx_balance.csv",
        help="Path to bidirectional_pair_ctx_balance.csv.",
    )
    parser.add_argument(
        "--output",
        type=Path,
        default=None,
        help="Output PNG path. Defaults to INPUT_DIR/bidirectional_netting_potential_heatmap.png.",
    )
    parser.add_argument(
        "--csv-output",
        type=Path,
        default=None,
        help="Optional output CSV path. Defaults to INPUT_DIR/bidirectional_netting_potential.csv.",
    )
    parser.add_argument(
        "--title",
        default="Bidirectional Netting Potential Between Shards",
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


def draw_multiline_centered(
    draw: ImageDraw.ImageDraw,
    xy: tuple[float, float],
    lines: list[tuple[str, ImageFont.ImageFont, tuple[int, int, int]]],
    line_gap: int = 5,
) -> None:
    sizes = [text_size(draw, text, fnt) for text, fnt, _ in lines]
    total_h = sum(height for _, height in sizes) + line_gap * (len(lines) - 1)
    y = xy[1] - total_h / 2
    for (text, fnt, fill), (width, height) in zip(lines, sizes):
        draw.text((xy[0] - width / 2, y), text, font=fnt, fill=fill)
        y += height + line_gap


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


def read_pairs(path: Path) -> tuple[list[int], list[PairPotential]]:
    pairs: list[PairPotential] = []
    shard_ids: set[int] = set()
    with path.open(newline="") as file:
        for row in csv.DictReader(file):
            lower = int(row["lower_shard"])
            higher = int(row["higher_shard"])
            forward = int(row["lower_to_higher_ctx"])
            reverse = int(row["higher_to_lower_ctx"])
            total = forward + reverse
            nettable_count = 2 * min(forward, reverse)
            potential = nettable_count / total if total else 0.0
            shard_ids.update([lower, higher])
            pairs.append(
                PairPotential(
                    lower=lower,
                    higher=higher,
                    lower_to_higher=forward,
                    higher_to_lower=reverse,
                    total=total,
                    nettable_count=nettable_count,
                    potential=potential,
                )
            )

    if not pairs:
        raise ValueError(f"no rows in {path}")
    return sorted(shard_ids), pairs


def write_potential_csv(path: Path, pairs: list[PairPotential]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="") as file:
        writer = csv.DictWriter(
            file,
            fieldnames=[
                "lower_shard",
                "higher_shard",
                "lower_to_higher_ctx",
                "higher_to_lower_ctx",
                "total_ctx",
                "nettable_ctx_count",
                "potential_ratio",
            ],
        )
        writer.writeheader()
        for pair in pairs:
            writer.writerow(
                {
                    "lower_shard": pair.lower,
                    "higher_shard": pair.higher,
                    "lower_to_higher_ctx": pair.lower_to_higher,
                    "higher_to_lower_ctx": pair.higher_to_lower,
                    "total_ctx": pair.total,
                    "nettable_ctx_count": pair.nettable_count,
                    "potential_ratio": f"{pair.potential:.6f}",
                }
            )


def blend(low: tuple[int, int, int], high: tuple[int, int, int], value: float) -> tuple[int, int, int]:
    value = max(0.0, min(1.0, value))
    return tuple(int(low[i] + (high[i] - low[i]) * value) for i in range(3))


def fmt_count(value: int) -> str:
    if value >= 1000:
        return f"{value / 1000:.1f}k"
    return str(value)


def fmt_percent(value: float) -> str:
    return f"{value * 100:.1f}%"


def build_matrix(
    shards: list[int],
    pairs: list[PairPotential],
) -> tuple[list[list[Optional[PairPotential]]], float, float]:
    index = {shard: idx for idx, shard in enumerate(shards)}
    matrix: list[list[Optional[PairPotential]]] = [[None for _ in shards] for _ in shards]
    for pair in pairs:
        lower_idx = index[pair.lower]
        higher_idx = index[pair.higher]
        matrix[lower_idx][higher_idx] = pair
        matrix[higher_idx][lower_idx] = pair

    values = [pair.potential for pair in pairs]
    return matrix, min(values), max(values)


def plot_heatmap(shards: list[int], pairs: list[PairPotential], output: Path, title: str, dpi: int) -> None:
    n = len(shards)
    matrix, min_value, max_value = build_matrix(shards, pairs)
    span = max(0.000001, max_value - min_value)

    width, height = 1580, 1390
    left, top = 235, 180
    cell = 108
    gap = 6
    grid_w = n * cell
    grid_h = n * cell

    bg = (255, 255, 255)
    text = (20, 22, 26)
    muted = (82, 90, 104)
    axis = (32, 35, 40)
    diag = (235, 237, 240)
    low = (255, 247, 230)
    high = (40, 120, 105)

    image = Image.new("RGB", (width, height), bg)
    draw = ImageDraw.Draw(image)

    title_font = font(42, bold=True)
    axis_font = font(34, bold=True)
    tick_font = font(28)
    percent_font = font(23, bold=True)
    count_font = font(19)
    note_font = font(23)

    draw_centered(draw, (width / 2, 70), title, title_font, text)
    draw_centered(draw, (left + grid_w / 2, top - 57), "Shard pair", axis_font, text)
    draw_vertical_centered(image, (72, top + grid_h // 2), "Shard pair", axis_font, text)

    for col, shard in enumerate(shards):
        x = left + col * cell + cell / 2
        draw_centered(draw, (x, top - 20), f"S{shard}", tick_font, axis)
    for row, shard in enumerate(shards):
        y = top + row * cell + cell / 2
        draw_centered(draw, (left - 42, y), f"S{shard}", tick_font, axis)

    for row in range(n):
        for col in range(n):
            x0 = left + col * cell + gap / 2
            y0 = top + row * cell + gap / 2
            x1 = left + (col + 1) * cell - gap / 2
            y1 = top + (row + 1) * cell - gap / 2
            pair = matrix[row][col]
            if pair is None:
                fill = diag
                draw.rounded_rectangle((x0, y0, x1, y1), radius=9, fill=fill, outline=(255, 255, 255), width=2)
                draw_centered(draw, ((x0 + x1) / 2, (y0 + y1) / 2), "-", tick_font, muted)
                continue

            ratio = (pair.potential - min_value) / span
            fill = blend(low, high, ratio)
            label_color = (255, 255, 255) if ratio > 0.58 else text
            sub_color = (241, 245, 249) if ratio > 0.58 else muted
            draw.rounded_rectangle((x0, y0, x1, y1), radius=9, fill=fill, outline=(255, 255, 255), width=2)
            draw_multiline_centered(
                draw,
                ((x0 + x1) / 2, (y0 + y1) / 2),
                [
                    (fmt_percent(pair.potential), percent_font, label_color),
                    (f"{fmt_count(pair.lower_to_higher)}/{fmt_count(pair.higher_to_lower)}", count_font, sub_color),
                ],
                line_gap=6,
            )

    bar_x0 = left + grid_w + 88
    bar_y0 = top
    bar_w = 34
    bar_h = grid_h
    for i in range(bar_h):
        ratio = 1 - i / max(1, bar_h - 1)
        color = blend(low, high, ratio)
        draw.line((bar_x0, bar_y0 + i, bar_x0 + bar_w, bar_y0 + i), fill=color, width=1)
    draw.rectangle((bar_x0, bar_y0, bar_x0 + bar_w, bar_y0 + bar_h), outline=axis, width=2)
    draw.text((bar_x0 + 52, bar_y0 - 8), fmt_percent(max_value), font=tick_font, fill=text)
    draw.text((bar_x0 + 52, bar_y0 + bar_h - 25), fmt_percent(min_value), font=tick_font, fill=text)
    draw_vertical_centered(image, (bar_x0 + 145, bar_y0 + bar_h // 2), "2min(a,b)/(a+b)", tick_font, text)

    note = (
        "Color encodes bidirectional potential; each cell label shows potential and "
        "the two directional CTX counts a/b."
    )
    draw.text((left, height - 75), note, font=note_font, fill=muted)

    output.parent.mkdir(parents=True, exist_ok=True)
    image.save(output, format="PNG", dpi=(dpi, dpi))


def main() -> None:
    args = parse_args()
    output = args.output or args.input.with_name("bidirectional_netting_potential_heatmap.png")
    csv_output = args.csv_output or args.input.with_name("bidirectional_netting_potential.csv")
    shards, pairs = read_pairs(args.input)
    write_potential_csv(csv_output, pairs)
    plot_heatmap(shards, pairs, output, args.title, args.dpi)
    print(f"wrote {csv_output}")
    print(f"wrote {output}")


if __name__ == "__main__":
    main()
