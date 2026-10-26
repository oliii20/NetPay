#!/usr/bin/env python3
"""Plot per-transaction mean confirmation times for the 8-shard runs."""

from __future__ import annotations

import argparse
import csv
import math
from collections import defaultdict
from collections.abc import Iterable
from datetime import datetime
from pathlib import Path

import numpy as np


REPO_ROOT = Path(__file__).resolve().parents[3]
DEFAULT_SESSION = (
    REPO_ROOT
    / "exp/netting-paper/repeated-exp1-scale-4-8-16-24/sessions"
    / "exp1-scale-4-8-16-24-repeat5-02"
)
DEFAULT_OUT = Path(__file__).resolve().parent
METHODS = [
    ("static_relay", "Static Relay", "#9DB7DD", "relay_stats_detail_tx_info.csv"),
    ("static_broker", "Static Broker", "#E8C27D", "broker_stats_detail_tx_info.csv"),
    ("clpa_relay", "CLPA Relay", "#C9ADD8", "relay_stats_detail_tx_info.csv"),
    ("netting_static_relay", "Netting", "#9FD5CB", "relay_stats_detail_tx_info.csv"),
]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--session", type=Path, default=DEFAULT_SESSION)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT)
    parser.add_argument("--dpi", type=int, default=300)
    args = parser.parse_args()

    rows = load_shard8_rows(args.session / "summary.csv")
    transaction_means = collect_transaction_means(args.session, rows)
    samples = {
        method: np.asarray([means[tx_id] for tx_id in sorted(means)])
        for method, means in transaction_means.items()
    }
    validate_counts(rows, transaction_means)

    args.out.mkdir(parents=True, exist_ok=True)
    stem = args.out / "fig_shard8_confirmation_time_violin"
    stats_path = stem.with_name(f"{stem.name}_stats.csv")
    density_path = stem.with_name(f"{stem.name}_density.csv")
    scatter_path = stem.with_name(f"{stem.name}_scatter.csv")
    per_transaction_path = stem.with_name(f"{stem.name}_per_transaction.csv")
    sources_path = stem.with_name(f"{stem.name}_sources.csv")
    write_stats(samples, stats_path)
    write_density(samples, density_path)
    write_scatter(samples, scatter_path)
    write_per_transaction(transaction_means, per_transaction_path)
    write_sources(rows, sources_path)
    outputs = plot_violin(samples, stem, args.dpi)

    print(f"8-shard runs: {len(rows)}")
    for method, label, _, _ in METHODS:
        values = samples[method]
        print(
            f"{label}: n={values.size}, mean={np.mean(values):.3f}s, "
            f"p50={np.quantile(values, 0.50):.3f}s, "
            f"p95={np.quantile(values, 0.95):.3f}s"
        )
    print(
        "outputs: "
        + ", ".join(
            str(path)
            for path in [
                *outputs,
                stats_path,
                density_path,
                scatter_path,
                per_transaction_path,
                sources_path,
            ]
        )
    )
    return 0


def load_shard8_rows(summary_path: Path) -> list[dict[str, str]]:
    method_keys = {method for method, _, _, _ in METHODS}
    with summary_path.open(newline="", encoding="utf-8") as fp:
        rows = [
            row
            for row in csv.DictReader(fp)
            if row.get("experiment") == "exp1_scale_repeated"
            and row.get("shard_num") == "8"
            and row.get("method") in method_keys
        ]
    if not rows:
        raise ValueError(f"no 8-shard experiment rows found in {summary_path}")
    return sorted(rows, key=lambda row: (int(row["repeat"]), row["method"]))


def collect_transaction_means(
    session: Path,
    rows: list[dict[str, str]],
) -> dict[str, dict[str, float]]:
    grouped: dict[str, defaultdict[str, list[float]]] = {
        method: defaultdict(list) for method, _, _, _ in METHODS
    }
    detail_files = {method: detail for method, _, _, detail in METHODS}
    for row in rows:
        method = row["method"]
        run_dir = session / "runs" / f"repeat_{int(row['repeat']):02d}" / row["run_id"]
        results_dir = run_dir / "results"
        if method == "netting_static_relay":
            records = iter_netting_latency_records(results_dir)
        else:
            records = iter_detail_latency_records(results_dir / detail_files[method])
        count = 0
        for tx_id, latency in records:
            grouped[method][tx_id].append(latency)
            count += 1
        if count == 0:
            raise ValueError(f"no positive confirmation times found for {run_dir}")
    repeat_counts = {
        method: sum(row["method"] == method for row in rows)
        for method, _, _, _ in METHODS
    }
    for method, records in grouped.items():
        incomplete = sum(len(latencies) != repeat_counts[method] for latencies in records.values())
        if incomplete:
            raise ValueError(
                f"{method}: {incomplete} transactions do not have "
                f"{repeat_counts[method]} repeat observations"
            )
    return {
        method: {tx_id: float(np.mean(latencies)) for tx_id, latencies in records.items()}
        for method, records in grouped.items()
    }


def iter_netting_latency_records(results_dir: Path) -> Iterable[tuple[str, float]]:
    intent_path = results_dir / "netting_intent_metrics.csv"
    with intent_path.open(newline="", encoding="utf-8") as fp:
        for row in csv.DictReader(fp):
            latency = parse_float(row.get("EndToEndLatencyNs")) / 1e9
            if latency > 0 and math.isfinite(latency):
                yield row["IntentID"], latency
    yield from iter_detail_latency_records(results_dir / "relay_stats_detail_tx_info.csv")


def iter_detail_latency_records(path: Path) -> Iterable[tuple[str, float]]:
    with path.open(newline="", encoding="utf-8") as fp:
        for row in csv.DictReader(fp):
            start = row.get("Tx create time", "")
            end = row.get("Tx finally commit time", "")
            if not start or not end:
                continue
            latency = (datetime.fromisoformat(end) - datetime.fromisoformat(start)).total_seconds()
            if latency > 0 and math.isfinite(latency):
                yield row["OriginalHash"], latency


def parse_float(value: str | None) -> float:
    try:
        return float(value or 0.0)
    except ValueError:
        return 0.0


def validate_counts(
    rows: list[dict[str, str]],
    transaction_means: dict[str, dict[str, float]],
) -> None:
    expected: dict[str, set[int]] = {method: set() for method, _, _, _ in METHODS}
    run_counts: dict[str, int] = {method: 0 for method, _, _, _ in METHODS}
    for row in rows:
        method = row["method"]
        expected[method].add(int(float(row["latency_tx_count"])))
        run_counts[method] += 1
    problems = []
    for method, label, _, _ in METHODS:
        if len(expected[method]) != 1:
            problems.append(f"{label}: inconsistent per-run counts={sorted(expected[method])}")
            continue
        expected_unique = next(iter(expected[method]))
        actual = len(transaction_means[method])
        if run_counts[method] == 0 or actual != expected_unique:
            problems.append(
                f"{label}: runs={run_counts[method]}, expected unique transactions="
                f"{expected_unique}, parsed={actual}"
            )
    if problems:
        raise ValueError("transaction-count validation failed:\n  " + "\n  ".join(problems))


def write_stats(samples: dict[str, np.ndarray], path: Path) -> None:
    fields = [
        "method",
        "label",
        "observation_unit",
        "repeat_count",
        "tx_count",
        "mean_s",
        "min_s",
        "q1_s",
        "p50_s",
        "q3_s",
        "whislo_s",
        "whishi_s",
        "p95_s",
        "max_s",
    ]
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=fields)
        writer.writeheader()
        for method, label, _, _ in METHODS:
            values = samples[method]
            q1, median, q3 = np.quantile(values, [0.25, 0.50, 0.75])
            iqr = q3 - q1
            inside = values[(values >= q1 - 1.5 * iqr) & (values <= q3 + 1.5 * iqr)]
            writer.writerow(
                {
                    "method": method,
                    "label": label,
                    "observation_unit": "per_transaction_mean_across_repeats",
                    "repeat_count": 2,
                    "tx_count": values.size,
                    "mean_s": f"{np.mean(values):.9f}",
                    "min_s": f"{np.min(values):.9f}",
                    "q1_s": f"{q1:.9f}",
                    "p50_s": f"{median:.9f}",
                    "q3_s": f"{q3:.9f}",
                    "whislo_s": f"{np.min(inside):.9f}",
                    "whishi_s": f"{np.max(inside):.9f}",
                    "p95_s": f"{np.quantile(values, 0.95):.9f}",
                    "max_s": f"{np.max(values):.9f}",
                }
            )


def write_sources(rows: list[dict[str, str]], path: Path) -> None:
    fields = ["repeat", "seed", "method", "run_id", "latency_tx_count"]
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=fields)
        writer.writeheader()
        for row in rows:
            writer.writerow({field: row.get(field, "") for field in fields})


def write_density(samples: dict[str, np.ndarray], path: Path) -> None:
    """Export the calculated violin densities so plotting no longer needs raw runs."""
    maximum = max(float(np.max(values)) for values in samples.values())
    upper = math.ceil(maximum / 5.0) * 5.0
    grid = np.linspace(0.0, upper, 801)
    densities = {
        method: histogram_density(samples[method], grid, sigma_s=0.80)
        for method, _, _, _ in METHODS
    }
    fields = ["confirmation_time_s", *[method for method, _, _, _ in METHODS]]
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=fields)
        writer.writeheader()
        for index, latency in enumerate(grid):
            row: dict[str, str] = {"confirmation_time_s": f"{latency:.6f}"}
            for method, _, _, _ in METHODS:
                row[method] = f"{densities[method][index]:.9f}"
            writer.writerow(row)


def write_scatter(samples: dict[str, np.ndarray], path: Path, count: int = 500) -> None:
    """Export a deterministic representative sample for the scatter overlay."""
    rng = np.random.default_rng(20261016)
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=["method", "confirmation_time_s"])
        writer.writeheader()
        for method, _, _, _ in METHODS:
            values = samples[method]
            indices = rng.choice(values.size, size=min(count, values.size), replace=False)
            for value in values[indices]:
                writer.writerow({"method": method, "confirmation_time_s": f"{value:.9f}"})


def write_per_transaction(
    transaction_means: dict[str, dict[str, float]],
    path: Path,
) -> None:
    """Export 300,000 transaction-level means per method without false ID joins."""
    fields = ["method", "transaction_id", "confirmation_time_mean_s"]
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=fields)
        writer.writeheader()
        for method, _, _, _ in METHODS:
            for tx_id in sorted(transaction_means[method]):
                writer.writerow(
                    {
                        "method": method,
                        "transaction_id": tx_id,
                        "confirmation_time_mean_s": f"{transaction_means[method][tx_id]:.9f}",
                    }
                )


def plot_violin(
    samples: dict[str, np.ndarray],
    output_stem: Path,
    dpi: int,
) -> list[Path]:
    import matplotlib.pyplot as plt

    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "Times", "DejaVu Serif"],
            "font.size": 10,
            "axes.labelsize": 12,
            "xtick.labelsize": 10.5,
            "ytick.labelsize": 10,
            "figure.dpi": dpi,
            "savefig.dpi": dpi,
            "savefig.bbox": "tight",
            "savefig.pad_inches": 0.05,
            "axes.linewidth": 0.85,
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
        }
    )

    ordered = [samples[method] for method, _, _, _ in METHODS]
    labels = [label for _, label, _, _ in METHODS]
    colors = [color for _, _, color, _ in METHODS]
    maximum = max(float(np.max(values)) for values in ordered)
    upper = math.ceil(maximum / 5.0) * 5.0
    grid = np.linspace(0.0, upper, 801)

    fig, ax = plt.subplots(figsize=(7.1, 4.15))
    half_width = 0.34
    rng = np.random.default_rng(20261016)
    for position, (values, color) in enumerate(zip(ordered, colors), start=1):
        density = histogram_density(values, grid, sigma_s=0.80)
        peak = max(float(np.max(density)), 1e-12)
        # Trim numerically tiny KDE tails so the polygon does not draw a long
        # center line from zero-density bins to the top of the shared axis.
        visible = density >= peak * 1e-4
        plot_grid = grid[visible]
        width = half_width * density[visible] / peak
        ax.fill_betweenx(
            plot_grid,
            position - width,
            position + width,
            facecolor=color,
            edgecolor="#4A4A4A",
            linewidth=1.35,
            alpha=0.52,
            zorder=2,
        )
        indices = rng.choice(values.size, size=min(500, values.size), replace=False)
        jitter = rng.uniform(-half_width * 0.76, half_width * 0.76, size=indices.size)
        ax.scatter(
            position + jitter,
            values[indices],
            s=5.5,
            color="#263238",
            alpha=0.16,
            linewidths=0,
            rasterized=True,
            zorder=3,
        )
        draw_boxplot(ax, position, values)
        p95 = float(np.quantile(values, 0.95))
        ax.hlines(
            p95,
            position - half_width * 0.70,
            position + half_width * 0.70,
            color="#202020",
            linewidth=1.1,
            linestyle=(0, (4, 2)),
            zorder=6,
        )

    p95_values = [float(np.quantile(values, 0.95)) for values in ordered]
    annotation = (
        f"Netting p95: {p95_values[-1]:.1f} s\n"
        f"Baselines: {min(p95_values[:-1]):.1f}–{max(p95_values[:-1]):.1f} s"
    )
    ax.annotate(
        annotation,
        xy=(4, p95_values[-1]),
        xytext=(3.06, upper * 0.63),
        ha="center",
        va="center",
        fontsize=10,
        bbox={"boxstyle": "round,pad=0.38", "fc": "white", "ec": "#8A8A8A", "lw": 0.8},
        arrowprops={"arrowstyle": "-|>", "color": "#404040", "lw": 0.9},
        zorder=6,
    )

    ax.set_xlim(0.45, 4.55)
    ax.set_ylim(0, upper)
    ax.set_xticks(range(1, 5), labels)
    ax.set_ylabel("Confirmation time (s)")
    ax.yaxis.grid(True, color="#D8D8D8", linewidth=0.65, alpha=0.85)
    ax.xaxis.grid(False)
    ax.set_axisbelow(True)
    for spine in ax.spines.values():
        spine.set_color("#222222")
    fig.tight_layout(pad=0.4)

    outputs = [output_stem.with_suffix(suffix) for suffix in (".pdf", ".png", ".svg")]
    fig.savefig(outputs[0])
    fig.savefig(outputs[1], dpi=dpi)
    fig.savefig(outputs[2])
    plt.close(fig)
    return outputs


def draw_boxplot(ax: object, position: int, values: np.ndarray) -> None:
    from matplotlib.patches import Rectangle

    q1, median, q3 = np.quantile(values, [0.25, 0.50, 0.75])
    iqr = q3 - q1
    inside = values[(values >= q1 - 1.5 * iqr) & (values <= q3 + 1.5 * iqr)]
    whislo, whishi = float(np.min(inside)), float(np.max(inside))
    box_width = 0.13
    cap_width = 0.09
    ax.vlines(position, whislo, q1, color="#242424", linewidth=0.9, zorder=5)
    ax.vlines(position, q3, whishi, color="#242424", linewidth=0.9, zorder=5)
    ax.hlines([whislo, whishi], position - cap_width / 2, position + cap_width / 2,
              color="#242424", linewidth=0.9, zorder=5)
    ax.add_patch(
        Rectangle(
            (position - box_width / 2, q1),
            box_width,
            q3 - q1,
            facecolor="white",
            edgecolor="#242424",
            linewidth=0.95,
            alpha=0.90,
            zorder=5,
        )
    )
    ax.hlines(median, position - box_width / 2, position + box_width / 2,
              color="#111111", linewidth=1.8, zorder=6)


def histogram_density(values: np.ndarray, grid: np.ndarray, sigma_s: float) -> np.ndarray:
    edges = np.linspace(grid[0], grid[-1], grid.size + 1)
    counts, _ = np.histogram(values, bins=edges)
    step = float(grid[1] - grid[0])
    sigma_bins = max(sigma_s / step, 1.0)
    radius = max(1, int(math.ceil(4.0 * sigma_bins)))
    offsets = np.arange(-radius, radius + 1, dtype=np.float64)
    kernel = np.exp(-0.5 * (offsets / sigma_bins) ** 2)
    kernel /= np.sum(kernel)
    return np.convolve(counts.astype(np.float64), kernel, mode="same")


if __name__ == "__main__":
    raise SystemExit(main())
