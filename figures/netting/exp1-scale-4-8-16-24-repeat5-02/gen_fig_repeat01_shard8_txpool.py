#!/usr/bin/env python3
"""Plot summed or mean txpool size for repeat 1 of the 8-shard experiment."""

from __future__ import annotations

import argparse
import bisect
import csv
import math
import re
from datetime import datetime
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[3]
DEFAULT_SESSION = REPO_ROOT / (
    "exp/netting-paper/repeated-exp1-scale-4-8-16-24/sessions/"
    "exp1-scale-4-8-16-24-repeat5-02"
)
DEFAULT_OUT = Path(__file__).resolve().parent
METHODS = [
    ("netting_static_relay", "Netting", "#D55E00", "-"),
    ("static_relay", "Relay", "#0072B2", "--"),
    ("static_broker", "Broker", "#009E73", "-."),
    ("clpa_relay", "CLPA", "#CC79A7", ":"),
]
FRACTION_RE = re.compile(r"^(?P<prefix>.*\.)(?P<fraction>\d+)(?P<zone>Z|[+-]\d\d:\d\d)$")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--session", type=Path, default=DEFAULT_SESSION)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT)
    parser.add_argument("--sample-seconds", type=float, default=0.5)
    parser.add_argument("--aggregation", choices=("sum", "mean"), default="sum")
    parser.add_argument("--dpi", type=int, default=300)
    return parser.parse_args()


def read_samples(path: Path) -> list[tuple[float, int]]:
    samples: list[tuple[float, int]] = []
    with path.open(newline="", encoding="utf-8") as fp:
        for row in csv.DictReader(fp):
            timestamp = parse_timestamp(row["Timestamp"])
            samples.append((timestamp, int(row["TxCount"])))
    return sorted(samples)


def parse_timestamp(raw: str) -> float:
    """Parse Go RFC3339Nano timestamps on Python versions limited to microseconds."""
    match = FRACTION_RE.match(raw)
    if match:
        fraction = match.group("fraction")[:6].ljust(6, "0")
        raw = f"{match.group('prefix')}{fraction}{match.group('zone')}"
    return datetime.fromisoformat(raw.replace("Z", "+00:00")).timestamp()


def step_value(samples: list[tuple[float, int]], timestamp: float) -> int:
    idx = bisect.bisect_right([sample[0] for sample in samples], timestamp) - 1
    return 0 if idx < 0 else samples[idx][1]


def load_system_curve(run_dir: Path, sample_seconds: float) -> tuple[list[float], list[int]]:
    shard_samples = []
    for shard_id in range(8):
        path = run_dir / "block_record" / f"shard={shard_id}_node=0" / "txpool_record.csv"
        if not path.exists():
            raise SystemExit(f"missing txpool record: {path}")
        samples = read_samples(path)
        if not samples:
            raise SystemExit(f"empty txpool record: {path}")
        shard_samples.append(samples)

    positive_times = [time for samples in shard_samples for time, count in samples if count > 0]
    if not positive_times:
        raise SystemExit(f"no positive txpool samples: {run_dir}")
    start = min(positive_times)
    end = min(samples[-1][0] for samples in shard_samples)
    point_count = math.floor((end - start) / sample_seconds) + 1
    times = [idx * sample_seconds for idx in range(point_count)]
    sizes = [
        sum(step_value(samples, start + elapsed) for samples in shard_samples)
        for elapsed in times
    ]
    return times, sizes


def load_curves(session: Path, sample_seconds: float) -> dict[str, tuple[list[float], list[int]]]:
    repeat_dir = session / "runs" / "repeat_01"
    curves = {}
    for method, _, _, _ in METHODS:
        run_dir = repeat_dir / f"exp1_scale_repeated_{method}_shard_num_8_seed1"
        if not run_dir.exists():
            raise SystemExit(f"run not found: {run_dir}")
        curves[method] = load_system_curve(run_dir, sample_seconds)
    return curves


def aggregate_curves(
    curves: dict[str, tuple[list[float], list[int]]], aggregation: str
) -> dict[str, tuple[list[float], list[float]]]:
    divisor = 8.0 if aggregation == "mean" else 1.0
    return {
        method: (times, [size / divisor for size in sizes])
        for method, (times, sizes) in curves.items()
    }


def write_csv(path: Path, curves: dict[str, tuple[list[float], list[float]]], aggregation: str) -> None:
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.writer(fp)
        value_column = "MeanTxPoolSizePerShard" if aggregation == "mean" else "TxPoolSize"
        writer.writerow(["Method", "SamplingTimeSeconds", value_column])
        for method, _, _, _ in METHODS:
            times, sizes = curves[method]
            writer.writerows((method, f"{time:.3f}", f"{size:.3f}") for time, size in zip(times, sizes))


def plot(
    path_stem: Path,
    curves: dict[str, tuple[list[float], list[float]]],
    aggregation: str,
    dpi: int,
) -> None:
    import matplotlib

    matplotlib.use("Agg")
    import matplotlib.pyplot as plt

    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "Times", "DejaVu Serif"],
            "font.size": 10,
            "axes.labelsize": 10.5,
            "legend.fontsize": 9,
            "figure.dpi": dpi,
            "savefig.dpi": dpi,
            "savefig.bbox": "tight",
            "axes.spines.top": False,
            "axes.spines.right": False,
            "axes.grid": True,
            "grid.alpha": 0.2,
            "grid.linestyle": "-",
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
        }
    )

    fig, ax = plt.subplots(figsize=(6.4, 3.2))
    for method, label, color, linestyle in METHODS:
        times, sizes = curves[method]
        ax.plot(times, sizes, label=label, color=color, linestyle=linestyle, linewidth=1.9)

    ax.set_xlabel("Sampling time (s)")
    ylabel = "Mean txpool size per shard" if aggregation == "mean" else "Txpool size (transactions)"
    ax.set_ylabel(ylabel)
    ax.set_ylim(bottom=0)
    ax.margins(x=0.01)
    ax.xaxis.grid(False)
    ax.legend(ncol=4, frameon=False, loc="upper center", bbox_to_anchor=(0.5, 1.16))
    fig.tight_layout(pad=0.55)
    fig.savefig(path_stem.with_suffix(".pdf"))
    fig.savefig(path_stem.with_suffix(".png"))
    plt.close(fig)


def main() -> int:
    args = parse_args()
    if args.sample_seconds <= 0:
        raise SystemExit("sample-seconds must be positive")
    system_curves = load_curves(args.session, args.sample_seconds)
    curves = aggregate_curves(system_curves, args.aggregation)
    args.out.mkdir(parents=True, exist_ok=True)
    suffix = "_mean" if args.aggregation == "mean" else ""
    stem = args.out / f"fig_repeat01_shard8_txpool{suffix}"
    write_csv(stem.with_suffix(".csv"), curves, args.aggregation)
    plot(stem, curves, args.aggregation, args.dpi)
    print(f"generated: {stem.with_suffix('.pdf')}")
    print(f"generated: {stem.with_suffix('.png')}")
    print(f"generated: {stem.with_suffix('.csv')}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
