#!/usr/bin/env python3
"""Run the 700 tx/s/shard, five-repeat paper suite and plot all repeats."""
import argparse
from pathlib import Path
import subprocess
import sys
import time

from common import ROOT

ALL_METHODS = "adaptive,full,monoxide,brokerchain,clpa"


def commands(args):
    dataset = args.dataset or (
        ROOT / "data/25250000to25499999_BlockTransaction.csv"
        if args.tx_number <= 300000
        else ROOT.parent
        / "block-emulator-x-netting-dev_多边净额2.0/data/25250000to25499999_BlockTransaction.csv"
    )
    output = (
        args.output
        or ROOT
        / f"exp/scaling-700-{args.tx_number}-repeat5-{time.strftime('%Y%m%d-%H%M%S')}"
    ).resolve()
    run = [
        sys.executable,
        str(ROOT / "experiments/adaptive_aggregation/run_suite.py"),
        "scaling",
        "--dataset",
        str(dataset.resolve()),
        "--shard-counts",
        "4,8,16,24",
        "--methods",
        getattr(args, "methods", ALL_METHODS),
        "--tx-number",
        str(args.tx_number),
        "--per-shard-rate",
        "700",
        "--block-limit",
        "2000",
        "--block-interval-ms",
        "2000",
        "--beacon-interval-ms",
        "1000",
        "--window-ms",
        "2000",
        "--freeze-high-ms",
        "500",
        "--freeze-low-ms",
        "100",
        "--cooldown-ms",
        "10000",
        "--recovery-window-ms",
        "2000",
        "--recovery-windows",
        "3",
        "--recovery-change-percent",
        "75",
        "--min-destination-percent",
        "25",
        "--beacon-work-us",
        "0",
        "--broker-hot",
        "0",
        "--broker-seed",
        "1",
        "--repeats",
        "5",
        "--timeout",
        str(args.timeout),
        "--port-base",
        str(args.port_base),
        "--output",
        str(output),
    ]
    if args.dry_run:
        run.append("--dry-run")
    plot = [
        sys.executable,
        str(ROOT / "figures/gen_fig_repeated_scaling.py"),
        str(output),
        "--repeats",
        "5",
        "--methods",
        getattr(args, "methods", ALL_METHODS),
    ]
    return run, plot


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument(
        "--tx-number",
        type=int,
        required=True,
        help="positive valid original transactions in EACH run",
    )
    p.add_argument("--dataset", type=Path)
    p.add_argument(
        "--methods",
        default=ALL_METHODS,
        help="comma-separated methods; omit clpa to run 80 cases",
    )
    p.add_argument("--output", type=Path)
    p.add_argument(
        "--timeout", type=int, default=3600, help="timeout in seconds per run"
    )
    p.add_argument("--port-base", type=int, default=42000)
    p.add_argument(
        "--dry-run",
        action="store_true",
        help="write planned configs only; do not build, launch or plot",
    )
    args = p.parse_args()
    if args.tx_number <= 0 or args.timeout <= 0:
        p.error("tx-number and timeout must be positive")
    methods = args.methods.split(",")
    if (
        len(set(methods)) != len(methods)
        or not {"adaptive", "full"}.issubset(methods)
        or any(m not in ALL_METHODS.split(",") for m in methods)
    ):
        p.error("methods must be unique supported modes including adaptive and full")
    run, plot = commands(args)
    subprocess.run(run, cwd=ROOT, check=True)
    if not args.dry_run:
        subprocess.run(plot, cwd=ROOT, check=True)


if __name__ == "__main__":
    main()
