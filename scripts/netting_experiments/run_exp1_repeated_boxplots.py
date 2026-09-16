#!/usr/bin/env python3
"""Run repeated exp1 netting experiments and plot TPS/latency boxplots."""

from __future__ import annotations

import argparse
import csv
import json
import math
import os
import sys
from datetime import datetime
from pathlib import Path
from typing import Iterable

sys.path.insert(0, str(Path(__file__).resolve().parent))

import run_experiments as runner


DEFAULT_OUT_ROOT = Path(".exp/netting-paper/repeated-exp1")
DEFAULT_FIG_DIR = Path("figures/netting")
DEFAULT_REPEATS = 20
DEFAULT_TX_NUMBER = 300000
DEFAULT_TX_SPEED = 3000
DEFAULT_TIMEOUT = 1000
DEFAULT_MAX_WINDOW_MS = 1000
DEFAULT_BLOCK_LIMIT = 2000
DEFAULT_BLOCK_INTERVAL_MS = 2000
DEFAULT_BEACON_BLOCK_INTERVAL_MS = 500
DEFAULT_SETTLEMENT_CHUNK_SIZE = 500


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repeats", type=positive_int, default=DEFAULT_REPEATS)
    parser.add_argument("--profile", choices=["smoke", "pilot", "full"], default="full")
    parser.add_argument("--seeds", default="1", help="seed list passed to each repeat, e.g. 1 or 1,2,3")
    parser.add_argument("--dataset", type=Path, default=runner.DEFAULT_DATASET)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT_ROOT)
    parser.add_argument("--fig-dir", type=Path, default=DEFAULT_FIG_DIR)
    parser.add_argument("--session-name", default="", help="optional name for the repeated-session directory")
    parser.add_argument("--summary", type=Path, help="plot an existing repeated summary.csv without running")
    parser.add_argument("--tx-number", type=positive_int, default=DEFAULT_TX_NUMBER)
    parser.add_argument("--tx-speed", type=positive_int, default=DEFAULT_TX_SPEED)
    parser.add_argument("--block-limit", type=positive_int, default=DEFAULT_BLOCK_LIMIT)
    parser.add_argument("--shard-num", type=positive_int, default=4)
    parser.add_argument("--node-num", type=positive_int, default=4)
    parser.add_argument("--block-interval-ms", type=positive_int, default=DEFAULT_BLOCK_INTERVAL_MS)
    parser.add_argument("--beacon-block-interval-ms", type=positive_int, default=DEFAULT_BEACON_BLOCK_INTERVAL_MS)
    parser.add_argument("--settlement-chunk-size", type=positive_int, default=DEFAULT_SETTLEMENT_CHUNK_SIZE)
    parser.add_argument("--max-window-ms", type=positive_int, default=DEFAULT_MAX_WINDOW_MS)
    parser.add_argument("--go", default=os.environ.get("GO", "go"), help="Go compiler path")
    parser.add_argument("--skip-build", action="store_true")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT)
    parser.add_argument("--progress-interval", type=int, default=1)
    parser.add_argument(
        "--method-level",
        action="store_true",
        help="deprecated; exp1 boxplots now always use Relay, BrokerChain, and Netting",
    )
    args = parser.parse_args()

    repo = runner.REPO_ROOT
    if "GOCACHE" not in os.environ:
        os.environ["GOCACHE"] = str(repo / ".exp" / "gocache")

    if args.summary:
        summary_path = runner.repo_path(args.summary, repo)
        records = read_repeated_summary(summary_path)
        generated = plot_boxplots(records, runner.repo_path(args.fig_dir, repo), args.method_level)
        print("figures: " + ", ".join(str(path) for path in generated))
        return 0

    out_root = runner.repo_path(args.out, repo)
    out_dir = resolve_output_dir(out_root, args.session_name)
    summary_path = out_dir / "summary.csv"
    runs_path = out_dir / "runs.jsonl"
    fig_dir = runner.repo_path(args.fig_dir, repo)

    selected = {"exp1"}
    seeds = runner.parse_seeds(args.seeds)
    specs = runner.build_specs(
        args.profile,
        selected,
        seeds,
        args.tx_number,
        args.tx_speed,
        args.block_limit,
        args.block_interval_ms,
        args.beacon_block_interval_ms,
        args.settlement_chunk_size,
        args.max_window_ms,
    )
    runner.validate_specs(specs)
    specs = order_netting_first(specs)
    specs = [replace_shard_node_count(spec, args.shard_num, args.node_num) for spec in specs]

    print(f"session: {out_dir}")
    if args.dry_run:
        for repeat in range(1, args.repeats + 1):
            for spec in specs:
                print(f"repeat{repeat:02d}/{spec.run_id}")
        return 0

    out_dir.mkdir(parents=True, exist_ok=True)
    dataset_path = runner.repo_path(args.dataset, repo)
    dataset_tx_number = max((spec.tx_number for spec in specs if spec.workload == "dataset"), default=0)
    dataset_rows = runner.load_dataset(dataset_path, dataset_tx_number) if dataset_tx_number > 0 else []
    bin_dir = out_dir / "bin"
    if not args.skip_build:
        runner.build_binaries(repo, bin_dir, args.go)

    write_repeated_summary_header(summary_path)
    metadata = {
        "created_at": datetime.now().isoformat(timespec="seconds"),
        "repeats": args.repeats,
        "seeds": seeds,
        "tx_number": args.tx_number,
        "tx_speed": args.tx_speed,
        "shard_num": args.shard_num,
        "node_num": args.node_num,
        "block_limit": args.block_limit,
        "block_interval_ms": args.block_interval_ms,
        "beacon_block_interval_ms": args.beacon_block_interval_ms,
        "settlement_chunk_size": args.settlement_chunk_size,
        "max_window_ms": args.max_window_ms,
        "gocache": os.environ.get("GOCACHE", ""),
    }
    (out_dir / "metadata.json").write_text(json.dumps(metadata, indent=2, sort_keys=True) + "\n", encoding="utf-8")

    with runs_path.open("w", encoding="utf-8") as runs_fp:
        total = args.repeats * len(specs)
        global_idx = 0
        for repeat_idx in range(1, args.repeats + 1):
            repeat_dir = out_dir / "runs" / f"repeat_{repeat_idx:02d}"
            for spec in specs:
                global_idx += 1
                run_dir = repeat_dir / spec.run_id
                base_port = 24000 + ((global_idx - 1) % 40) * 900
                print(f"[{global_idx}/{total}] repeat {repeat_idx:02d} {spec.run_id}", flush=True)
                result = runner.run_one(
                    repo,
                    bin_dir,
                    run_dir,
                    spec,
                    dataset_rows,
                    base_port,
                    args.timeout,
                    args.progress_interval,
                    overwrite=False,
                )
                result["repeat"] = repeat_idx
                result["session"] = str(out_dir)
                append_repeated_summary(summary_path, result)
                runs_fp.write(json.dumps(result, sort_keys=True) + "\n")
                runs_fp.flush()

    generated = plot_boxplots(read_repeated_summary(summary_path), fig_dir, args.method_level)
    print(f"summary: {summary_path}")
    print(f"runs: {runs_path}")
    print("figures: " + ", ".join(str(path) for path in generated))
    return 0


def positive_int(raw: str) -> int:
    value = int(raw)
    if value <= 0:
        raise argparse.ArgumentTypeError("must be a positive integer")
    return value


def resolve_output_dir(out_root: Path, session_name: str) -> Path:
    label = runner.sanitize_session_name(session_name) if session_name else datetime.now().strftime("%Y%m%d-%H%M%S")
    session_root = out_root / "sessions"
    candidate = session_root / label
    if not candidate.exists():
        return candidate
    for idx in range(2, 1000):
        suffixed = session_root / f"{label}-{idx:02d}"
        if not suffixed.exists():
            return suffixed
    raise SystemExit(f"could not allocate a unique output session under {session_root}")


def write_repeated_summary_header(path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="", encoding="utf-8") as fp:
        csv.DictWriter(fp, fieldnames=repeated_summary_fields()).writeheader()


def append_repeated_summary(path: Path, row: dict[str, object]) -> None:
    with path.open("a", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=repeated_summary_fields(), extrasaction="ignore")
        writer.writerow(row)


def repeated_summary_fields() -> list[str]:
    return ["repeat", "session", *runner.SUMMARY_FIELDS]


def order_netting_first(specs: list[runner.RunSpec]) -> list[runner.RunSpec]:
    order = {
        "netting_static_relay": 0,
        "static_relay": 1,
        "clpa_broker": 2,
    }
    seed_positions: dict[int, int] = {}
    for spec in specs:
        if spec.seed not in seed_positions:
            seed_positions[spec.seed] = len(seed_positions)
    return sorted(
        specs,
        key=lambda spec: (seed_positions[spec.seed], order.get(spec.method, len(order))),
    )


def replace_shard_node_count(spec: runner.RunSpec, shard_num: int, node_num: int) -> runner.RunSpec:
    return runner.replace(spec, shard_num=shard_num, node_num=node_num)


def read_repeated_summary(path: Path) -> list[dict[str, str]]:
    if not path.exists():
        raise SystemExit(f"summary not found: {path}")
    with path.open(newline="", encoding="utf-8") as fp:
        return list(csv.DictReader(fp))


def metric_groups(
    records: Iterable[dict[str, str]],
    metric: str,
    method_level: bool = False,
) -> list[tuple[str, list[float]]]:
    del method_level
    order = method_group_order()
    grouped = {label: [] for label, _ in order}
    for row in records:
        if row.get("experiment") != "exp1_baseline":
            continue
        label = method_label(row.get("method", ""))
        if label not in grouped:
            continue
        value = float_or_none(row.get(metric, ""))
        if value is not None and math.isfinite(value):
            grouped[label].append(value)
    return [(label, grouped[label]) for label, _ in order]


def method_label(method: str) -> str:
    labels = {
        "static_relay": "Relay",
        "clpa_broker": "BrokerChain",
        "netting_static_relay": "Netting",
    }
    return labels.get(method, method)


def method_group_order() -> list[tuple[str, str]]:
    return [
        ("Relay", "#0072B2"),
        ("BrokerChain", "#56B4E9"),
        ("Netting", "#D55E00"),
    ]


def float_or_none(value: str) -> float | None:
    try:
        if value == "":
            return None
        return float(value)
    except (TypeError, ValueError):
        return None


def plot_boxplots(records: list[dict[str, str]], fig_dir: Path, method_level: bool = False) -> list[Path]:
    fig_dir.mkdir(parents=True, exist_ok=True)
    generated: list[Path] = []
    specs = [
        ("throughput_tps", "Throughput (tx/s)", "fig_exp1_repeated_tps_boxplot"),
        ("avg_latency_s", "Latency (s)", "fig_exp1_repeated_latency_boxplot"),
    ]
    for metric, ylabel, stem in specs:
        groups = metric_groups(records, metric, method_level)
        if not any(values for _, values in groups):
            raise SystemExit(f"no values found for {metric}")
        generated.extend(plot_metric_boxplot(groups, ylabel, fig_dir / stem))
    return generated


def plot_metric_boxplot(groups: list[tuple[str, list[float]]], ylabel: str, out_stem: Path) -> list[Path]:
    try:
        import matplotlib.pyplot as plt
    except ModuleNotFoundError:
        return plot_metric_boxplot_vector(groups, ylabel, out_stem)

    colors = dict(method_group_order())
    labels = [label for label, values in groups if values]
    values = [vals for _, vals in groups if vals]
    box_colors = [colors[label] for label in labels]

    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "Times", "DejaVu Serif"],
            "font.size": 10,
            "axes.labelsize": 10,
            "xtick.labelsize": 9,
            "ytick.labelsize": 9,
            "figure.dpi": 300,
            "savefig.dpi": 300,
            "savefig.bbox": "tight",
            "axes.spines.top": False,
            "axes.spines.right": False,
            "axes.grid": True,
            "grid.alpha": 0.22,
            "grid.linestyle": "-",
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
        }
    )

    fig_width = 4.2
    fig, ax = plt.subplots(figsize=(fig_width, 2.7))
    bp = ax.boxplot(
        values,
        labels=labels,
        widths=0.52,
        patch_artist=True,
        showmeans=True,
        meanline=False,
        boxprops={"linewidth": 1.0, "edgecolor": "#222222"},
        medianprops={"linewidth": 1.3, "color": "#111111"},
        meanprops={
            "marker": "D",
            "markerfacecolor": "white",
            "markeredgecolor": "#222222",
            "markersize": 4,
        },
        whiskerprops={"linewidth": 1.0, "color": "#333333"},
        capprops={"linewidth": 1.0, "color": "#333333"},
        flierprops={
            "marker": "o",
            "markerfacecolor": "white",
            "markeredgecolor": "#555555",
            "markersize": 3,
            "alpha": 0.75,
        },
    )
    for patch, color in zip(bp["boxes"], box_colors):
        patch.set_facecolor(color)
        patch.set_alpha(0.34)

    for idx, vals in enumerate(values, start=1):
        x_offsets = centered_offsets(len(vals), width=0.15)
        color = box_colors[idx - 1]
        ax.scatter(
            [idx + offset for offset in x_offsets],
            vals,
            s=12,
            color=color,
            edgecolors="white",
            linewidths=0.35,
            alpha=0.82,
            zorder=3,
        )

    ax.set_ylabel(ylabel)
    ax.yaxis.grid(True)
    ax.xaxis.grid(False)
    ax.tick_params(axis="x", length=0)
    ax.margins(x=0.08)
    fig.tight_layout(pad=0.6)

    pdf_path = out_stem.with_suffix(".pdf")
    png_path = out_stem.with_suffix(".png")
    fig.savefig(pdf_path)
    fig.savefig(png_path)
    plt.close(fig)
    return [pdf_path, png_path]


def plot_metric_boxplot_vector(groups: list[tuple[str, list[float]]], ylabel: str, out_stem: Path) -> list[Path]:
    colors = dict(method_group_order())
    labels = [label for label, values in groups if values]
    values = [vals for _, vals in groups if vals]
    stats = [box_stats(vals) for vals in values]
    all_values = [value for vals in values for value in vals]
    y_min, y_max = padded_range(all_values)
    ticks = linear_ticks(y_min, y_max, 5)

    width = 260 if len(labels) <= 2 else 330
    height = 220
    margin_left = 52
    margin_right = 14
    margin_top = 12
    margin_bottom = 42
    plot_w = width - margin_left - margin_right
    plot_h = height - margin_top - margin_bottom

    def sx(index: int) -> float:
        return margin_left + (index + 0.5) * plot_w / len(labels)

    def sy(value: float) -> float:
        return margin_top + (y_max - value) * plot_h / max(1e-9, y_max - y_min)

    svg_path = out_stem.with_suffix(".svg")
    pdf_path = out_stem.with_suffix(".pdf")
    write_svg_boxplot(
        svg_path,
        labels,
        values,
        stats,
        [colors[label] for label in labels],
        ylabel,
        ticks,
        width,
        height,
        margin_left,
        margin_bottom,
        plot_w,
        plot_h,
        sx,
        sy,
    )
    write_pdf_boxplot(
        pdf_path,
        labels,
        values,
        stats,
        [colors[label] for label in labels],
        ylabel,
        ticks,
        width,
        height,
        margin_left,
        margin_bottom,
        plot_w,
        plot_h,
        sx,
        sy,
    )
    return [pdf_path, svg_path]


def box_stats(values: list[float]) -> dict[str, float | list[float]]:
    vals = sorted(values)
    q1 = percentile(vals, 25)
    median = percentile(vals, 50)
    q3 = percentile(vals, 75)
    iqr = q3 - q1
    low_fence = q1 - 1.5 * iqr
    high_fence = q3 + 1.5 * iqr
    inliers = [value for value in vals if low_fence <= value <= high_fence]
    return {
        "q1": q1,
        "median": median,
        "q3": q3,
        "low": min(inliers) if inliers else vals[0],
        "high": max(inliers) if inliers else vals[-1],
        "outliers": [value for value in vals if value < low_fence or value > high_fence],
    }


def percentile(values: list[float], pct: float) -> float:
    if not values:
        return 0.0
    idx = (len(values) - 1) * pct / 100.0
    lo = math.floor(idx)
    hi = math.ceil(idx)
    if lo == hi:
        return values[int(idx)]
    return values[lo] * (hi - idx) + values[hi] * (idx - lo)


def padded_range(values: list[float]) -> tuple[float, float]:
    lo = min(values)
    hi = max(values)
    if math.isclose(lo, hi):
        pad = max(abs(lo) * 0.08, 1.0)
        return lo - pad, hi + pad
    pad = (hi - lo) * 0.08
    return lo - pad, hi + pad


def linear_ticks(lo: float, hi: float, count: int) -> list[float]:
    if count <= 1:
        return [lo]
    step = (hi - lo) / (count - 1)
    return [lo + idx * step for idx in range(count)]


def write_svg_boxplot(
    path: Path,
    labels: list[str],
    values: list[list[float]],
    stats: list[dict[str, float | list[float]]],
    colors: list[str],
    ylabel: str,
    ticks: list[float],
    width: int,
    height: int,
    margin_left: int,
    margin_bottom: int,
    plot_w: int,
    plot_h: int,
    sx,
    sy,
) -> None:
    import html

    plot_bottom = height - margin_bottom
    parts = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{width}" height="{height}" viewBox="0 0 {width} {height}">',
        '<rect width="100%" height="100%" fill="white"/>',
        '<g font-family="Times New Roman, Times, serif" font-size="9" fill="#222222">',
    ]
    for tick in ticks:
        y = sy(tick)
        parts.append(f'<line x1="{margin_left}" y1="{y:.2f}" x2="{margin_left + plot_w}" y2="{y:.2f}" stroke="#d9d9d9" stroke-width="0.55"/>')
        parts.append(f'<text x="{margin_left - 7}" y="{y + 3:.2f}" text-anchor="end">{format_tick(tick)}</text>')
    parts.append(f'<line x1="{margin_left}" y1="{plot_bottom}" x2="{margin_left + plot_w}" y2="{plot_bottom}" stroke="#222222" stroke-width="0.9"/>')
    parts.append(f'<line x1="{margin_left}" y1="{height - margin_bottom - plot_h}" x2="{margin_left}" y2="{plot_bottom}" stroke="#222222" stroke-width="0.9"/>')
    parts.append(
        f'<text transform="translate(14 {height / 2:.2f}) rotate(-90)" text-anchor="middle" font-size="10">{html.escape(ylabel)}</text>'
    )
    for idx, (label, vals, stat, color) in enumerate(zip(labels, values, stats, colors)):
        x = sx(idx)
        box_w = min(44, plot_w / len(labels) * 0.46)
        q1 = float(stat["q1"])
        q3 = float(stat["q3"])
        median = float(stat["median"])
        low = float(stat["low"])
        high = float(stat["high"])
        parts.append(f'<line x1="{x:.2f}" y1="{sy(high):.2f}" x2="{x:.2f}" y2="{sy(low):.2f}" stroke="#333333" stroke-width="1"/>')
        parts.append(f'<line x1="{x - box_w * 0.28:.2f}" y1="{sy(high):.2f}" x2="{x + box_w * 0.28:.2f}" y2="{sy(high):.2f}" stroke="#333333" stroke-width="1"/>')
        parts.append(f'<line x1="{x - box_w * 0.28:.2f}" y1="{sy(low):.2f}" x2="{x + box_w * 0.28:.2f}" y2="{sy(low):.2f}" stroke="#333333" stroke-width="1"/>')
        parts.append(
            f'<rect x="{x - box_w / 2:.2f}" y="{sy(q3):.2f}" width="{box_w:.2f}" height="{max(0.8, sy(q1) - sy(q3)):.2f}" fill="{color}" fill-opacity="0.34" stroke="#222222" stroke-width="1"/>'
        )
        parts.append(f'<line x1="{x - box_w / 2:.2f}" y1="{sy(median):.2f}" x2="{x + box_w / 2:.2f}" y2="{sy(median):.2f}" stroke="#111111" stroke-width="1.3"/>')
        for offset, value in zip(centered_offsets(len(vals), 0.15 * box_w), vals):
            parts.append(f'<circle cx="{x + offset:.2f}" cy="{sy(value):.2f}" r="1.8" fill="{color}" fill-opacity="0.82" stroke="white" stroke-width="0.45"/>')
        parts.append(f'<text x="{x:.2f}" y="{plot_bottom + 17}" text-anchor="middle">{html.escape(label)}</text>')
    parts.append("</g></svg>")
    path.write_text("\n".join(parts) + "\n", encoding="utf-8")


def write_pdf_boxplot(
    path: Path,
    labels: list[str],
    values: list[list[float]],
    stats: list[dict[str, float | list[float]]],
    colors: list[str],
    ylabel: str,
    ticks: list[float],
    width: int,
    height: int,
    margin_left: int,
    margin_bottom: int,
    plot_w: int,
    plot_h: int,
    sx,
    sy,
) -> None:
    plot_bottom = height - margin_bottom
    commands = ["1 1 1 rg 0 0 {0} {1} re f".format(width, height)]

    def pdf_y(svg_y: float) -> float:
        return height - svg_y

    for tick in ticks:
        y = sy(tick)
        commands.append("0.85 0.85 0.85 RG 0.55 w")
        commands.append(f"{margin_left:.2f} {pdf_y(y):.2f} m {margin_left + plot_w:.2f} {pdf_y(y):.2f} l S")
        commands.append(pdf_text(format_tick(tick), margin_left - 8, pdf_y(y) - 3, 9, align="right"))
    commands.append("0.13 0.13 0.13 RG 0.9 w")
    commands.append(f"{margin_left:.2f} {pdf_y(plot_bottom):.2f} m {margin_left + plot_w:.2f} {pdf_y(plot_bottom):.2f} l S")
    commands.append(f"{margin_left:.2f} {pdf_y(height - margin_bottom - plot_h):.2f} m {margin_left:.2f} {pdf_y(plot_bottom):.2f} l S")
    commands.append(pdf_rotated_text(ylabel, 14, height / 2, 10))
    for idx, (label, vals, stat, color) in enumerate(zip(labels, values, stats, colors)):
        x = sx(idx)
        box_w = min(44, plot_w / len(labels) * 0.46)
        q1 = float(stat["q1"])
        q3 = float(stat["q3"])
        median = float(stat["median"])
        low = float(stat["low"])
        high = float(stat["high"])
        fill = hex_to_rgb(mix_with_white(color, 0.66))
        stroke = (0.13, 0.13, 0.13)
        commands.append("0.20 0.20 0.20 RG 1 w")
        commands.append(f"{x:.2f} {pdf_y(sy(high)):.2f} m {x:.2f} {pdf_y(sy(low)):.2f} l S")
        commands.append(f"{x - box_w * 0.28:.2f} {pdf_y(sy(high)):.2f} m {x + box_w * 0.28:.2f} {pdf_y(sy(high)):.2f} l S")
        commands.append(f"{x - box_w * 0.28:.2f} {pdf_y(sy(low)):.2f} m {x + box_w * 0.28:.2f} {pdf_y(sy(low)):.2f} l S")
        commands.append(f"{fill[0]:.4f} {fill[1]:.4f} {fill[2]:.4f} rg {stroke[0]:.4f} {stroke[1]:.4f} {stroke[2]:.4f} RG 1 w")
        commands.append(
            f"{x - box_w / 2:.2f} {pdf_y(sy(q1)):.2f} {box_w:.2f} {max(0.8, sy(q1) - sy(q3)):.2f} re B"
        )
        commands.append("0.07 0.07 0.07 RG 1.3 w")
        commands.append(f"{x - box_w / 2:.2f} {pdf_y(sy(median)):.2f} m {x + box_w / 2:.2f} {pdf_y(sy(median)):.2f} l S")
        point_rgb = hex_to_rgb(color)
        commands.append(f"{point_rgb[0]:.4f} {point_rgb[1]:.4f} {point_rgb[2]:.4f} rg")
        for offset, value in zip(centered_offsets(len(vals), 0.15 * box_w), vals):
            commands.append(pdf_circle(x + offset, pdf_y(sy(value)), 1.8))
        commands.append(pdf_text(label, x, pdf_y(plot_bottom + 17), 9, align="center"))

    write_simple_pdf(path, width, height, "\n".join(commands) + "\n")


def format_tick(value: float) -> str:
    if abs(value) >= 100:
        return f"{value:.0f}"
    if abs(value) >= 10:
        return f"{value:.1f}"
    return f"{value:.2f}"


def mix_with_white(hex_color: str, white_ratio: float) -> str:
    r, g, b = [int(hex_color[idx : idx + 2], 16) for idx in (1, 3, 5)]
    mixed = [round(channel * (1.0 - white_ratio) + 255 * white_ratio) for channel in (r, g, b)]
    return "#" + "".join(f"{channel:02x}" for channel in mixed)


def hex_to_rgb(hex_color: str) -> tuple[float, float, float]:
    return tuple(int(hex_color[idx : idx + 2], 16) / 255.0 for idx in (1, 3, 5))


def pdf_circle(x: float, y: float, r: float) -> str:
    c = 0.5522847498 * r
    return (
        f"{x + r:.2f} {y:.2f} m "
        f"{x + r:.2f} {y + c:.2f} {x + c:.2f} {y + r:.2f} {x:.2f} {y + r:.2f} c "
        f"{x - c:.2f} {y + r:.2f} {x - r:.2f} {y + c:.2f} {x - r:.2f} {y:.2f} c "
        f"{x - r:.2f} {y - c:.2f} {x - c:.2f} {y - r:.2f} {x:.2f} {y - r:.2f} c "
        f"{x + c:.2f} {y - r:.2f} {x + r:.2f} {y - c:.2f} {x + r:.2f} {y:.2f} c f"
    )


def pdf_text(text: str, x: float, y: float, size: int, align: str = "left") -> str:
    text_width = len(text) * size * 0.44
    if align == "center":
        x -= text_width / 2
    elif align == "right":
        x -= text_width
    return f"BT /F1 {size} Tf {x:.2f} {y:.2f} Td ({pdf_escape(text)}) Tj ET"


def pdf_rotated_text(text: str, x: float, y: float, size: int) -> str:
    text_width = len(text) * size * 0.44
    return f"q 0 1 -1 0 {x:.2f} {y - text_width / 2:.2f} cm BT /F1 {size} Tf 0 0 Td ({pdf_escape(text)}) Tj ET Q"


def pdf_escape(text: str) -> str:
    return text.replace("\\", "\\\\").replace("(", "\\(").replace(")", "\\)")


def write_simple_pdf(path: Path, width: int, height: int, content: str) -> None:
    stream = content.encode("latin-1", errors="replace")
    objects = [
        b"<< /Type /Catalog /Pages 2 0 R >>",
        b"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
        f"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 {width} {height}] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>".encode(
            "ascii"
        ),
        b"<< /Type /Font /Subtype /Type1 /BaseFont /Times-Roman >>",
        b"<< /Length " + str(len(stream)).encode("ascii") + b" >>\nstream\n" + stream + b"endstream",
    ]
    chunks = [b"%PDF-1.4\n%\xe2\xe3\xcf\xd3\n"]
    offsets = [0]
    for idx, obj in enumerate(objects, start=1):
        offsets.append(sum(len(chunk) for chunk in chunks))
        chunks.append(f"{idx} 0 obj\n".encode("ascii") + obj + b"\nendobj\n")
    xref_at = sum(len(chunk) for chunk in chunks)
    chunks.append(f"xref\n0 {len(objects) + 1}\n0000000000 65535 f \n".encode("ascii"))
    for offset in offsets[1:]:
        chunks.append(f"{offset:010d} 00000 n \n".encode("ascii"))
    chunks.append(
        f"trailer\n<< /Size {len(objects) + 1} /Root 1 0 R >>\nstartxref\n{xref_at}\n%%EOF\n".encode(
            "ascii"
        )
    )
    path.write_bytes(b"".join(chunks))


def centered_offsets(count: int, width: float) -> list[float]:
    if count <= 1:
        return [0.0] * count
    step = width / max(1, count - 1)
    start = -width / 2.0
    return [start + idx * step for idx in range(count)]


if __name__ == "__main__":
    raise SystemExit(main())
