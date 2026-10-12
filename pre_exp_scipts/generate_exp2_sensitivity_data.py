#!/usr/bin/env python3
"""Run sensitivity experiments for shard count and injection rate.

This wrapper reuses generate_exp1_fifo_data.py so the filtering, account
placement, and FIFO split matching logic stay exactly aligned with experiment 1.
"""

from __future__ import annotations

import argparse
import csv
import subprocess
import sys
from pathlib import Path
from statistics import mean, pstdev


DEFAULT_RAW_CSV = "25250000区块高度-25255233区块高度_500K.csv"


def parse_args() -> argparse.Namespace:
    script_dir = Path(__file__).resolve().parent
    root_dir = script_dir.parent
    parser = argparse.ArgumentParser(description="Generate sensitivity experiment CSVs.")
    parser.add_argument(
        "--input",
        type=Path,
        default=root_dir / "Raw_data" / DEFAULT_RAW_CSV,
        help="Input CSV containing valid Ethereum transactions.",
    )
    parser.add_argument(
        "--output-dir",
        type=Path,
        default=root_dir / "画图_sensitivity",
        help="Directory for generated sweep outputs.",
    )
    parser.add_argument(
        "--generator",
        type=Path,
        default=script_dir / "generate_exp1_fifo_data.py",
        help="Path to generate_exp1_fifo_data.py.",
    )
    parser.add_argument("--max-valid-txs", type=int, default=500_000)
    parser.add_argument("--block-size", type=int, default=2_000)
    parser.add_argument("--base-inject-size", type=int, default=10_000)
    parser.add_argument("--base-shards", type=int, default=8)
    parser.add_argument("--shard-counts", default="4,8,16,32")
    parser.add_argument(
        "--shard-inject-sizes",
        default="5000,10000,20000,40000",
        help="Inject sizes aligned with --shard-counts for the shard-count sweep.",
    )
    parser.add_argument("--inject-sizes", default="8000,10000,12000,14000")
    parser.add_argument(
        "--skip-existing",
        action="store_true",
        help="Reuse an existing per-configuration height_matching_rate.csv if present.",
    )
    return parser.parse_args()


def parse_int_list(value: str) -> list[int]:
    result = [int(part.strip()) for part in value.split(",") if part.strip()]
    if not result:
        raise ValueError(f"empty integer list: {value!r}")
    return result


def run_generator(
    generator: Path,
    input_csv: Path,
    output_dir: Path,
    shards: int,
    inject_size: int,
    block_size: int,
    max_valid_txs: int,
    skip_existing: bool,
) -> None:
    height_csv = output_dir / "height_matching_rate.csv"
    summary_csv = output_dir / "run_summary.csv"
    if skip_existing and height_csv.exists() and summary_csv.exists():
        print(f"reuse {output_dir}")
        return

    output_dir.mkdir(parents=True, exist_ok=True)
    cmd = [
        sys.executable,
        str(generator),
        "--input",
        str(input_csv),
        "--output-dir",
        str(output_dir),
        "--shards",
        str(shards),
        "--inject-size",
        str(inject_size),
        "--block-size",
        str(block_size),
        "--max-valid-txs",
        str(max_valid_txs),
    ]
    print("run", " ".join(cmd))
    subprocess.run(cmd, check=True)


def read_summary(path: Path) -> dict[str, str]:
    with path.open(newline="") as file:
        return {row["metric"]: row["value"] for row in csv.DictReader(file)}


def read_height_rates(path: Path) -> list[float]:
    rates: list[float] = []
    with path.open(newline="") as file:
        for row in csv.DictReader(file):
            if int(row["total_ctx"]) > 0:
                rates.append(float(row["matching_rate"]))
    if not rates:
        raise ValueError(f"no height rows with CTX in {path}")
    return rates


def summarize_case(
    case_name: str,
    case_dir: Path,
    shards: int,
    inject_size: int,
    block_size: int,
    max_valid_txs: int,
) -> dict[str, object]:
    summary = read_summary(case_dir / "run_summary.csv")
    rates = read_height_rates(case_dir / "height_matching_rate.csv")
    return {
        "case": case_name,
        "shards": shards,
        "inject_size": inject_size,
        "block_size": block_size,
        "max_valid_txs": max_valid_txs,
        "block_heights": int(summary["block_heights"]),
        "total_ctx": int(summary["total_ctx_in_blocks"]),
        "fully_matched_ctx": int(summary["fully_matched_ctx"]),
        "global_matching_rate": f"{float(summary['global_matching_rate']):.6f}",
        "avg_height_matching_rate": f"{mean(rates):.6f}",
        "std_height_matching_rate": f"{pstdev(rates):.6f}",
        "min_height_matching_rate": f"{min(rates):.6f}",
        "max_height_matching_rate": f"{max(rates):.6f}",
        "output_dir": case_dir,
    }


def write_csv(path: Path, rows: list[dict[str, object]]) -> None:
    if not rows:
        raise ValueError(f"no rows for {path}")
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="") as file:
        writer = csv.DictWriter(file, fieldnames=list(rows[0].keys()))
        writer.writeheader()
        writer.writerows(rows)


def main() -> None:
    args = parse_args()
    shard_counts = parse_int_list(args.shard_counts)
    shard_inject_sizes = parse_int_list(args.shard_inject_sizes)
    inject_sizes = parse_int_list(args.inject_sizes)
    if len(shard_counts) != len(shard_inject_sizes):
        raise ValueError(
            "--shard-counts and --shard-inject-sizes must have the same length "
            f"({len(shard_counts)} != {len(shard_inject_sizes)})"
        )

    shard_rows: list[dict[str, object]] = []
    for shards, inject_size in zip(shard_counts, shard_inject_sizes):
        case = f"shards_{shards}_inject_{inject_size}"
        case_dir = args.output_dir / "shard_count_sweep" / case
        run_generator(
            args.generator,
            args.input,
            case_dir,
            shards,
            inject_size,
            args.block_size,
            args.max_valid_txs,
            args.skip_existing,
        )
        shard_rows.append(
            summarize_case(case, case_dir, shards, inject_size, args.block_size, args.max_valid_txs)
        )

    inject_rows: list[dict[str, object]] = []
    for inject_size in inject_sizes:
        case = f"shards_{args.base_shards}_inject_{inject_size}"
        case_dir = args.output_dir / "inject_size_sweep" / case
        run_generator(
            args.generator,
            args.input,
            case_dir,
            args.base_shards,
            inject_size,
            args.block_size,
            args.max_valid_txs,
            args.skip_existing,
        )
        inject_rows.append(
            summarize_case(case, case_dir, args.base_shards, inject_size, args.block_size, args.max_valid_txs)
        )

    write_csv(args.output_dir / "shard_count_sweep.csv", shard_rows)
    write_csv(args.output_dir / "inject_size_sweep.csv", inject_rows)
    print(f"wrote {args.output_dir / 'shard_count_sweep.csv'}")
    print(f"wrote {args.output_dir / 'inject_size_sweep.csv'}")


if __name__ == "__main__":
    main()
