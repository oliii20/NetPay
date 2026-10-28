#!/usr/bin/env python3
"""Serial scaling suite or paired, same-binary Beacon congestion experiment."""
import argparse
import json
from pathlib import Path
import statistics
import subprocess
import time
from common import (
    ROOT,
    MODES,
    build_binaries,
    provenance,
    sha256_file,
    write_csv,
    write_json,
)
from run import parser, execute


def prepare_binaries(requested, default):
    if requested is None:
        return build_binaries(default)
    requested = requested.resolve()
    for name in ("consensusnode", "beaconnode", "supervisor"):
        if not (requested / name).is_file():
            raise ValueError(f"missing reusable binary: {requested / name}")
    return requested


def main():
    p = parser()
    p.add_argument("suite", choices=("scaling", "congestion"))
    p.add_argument("--shard-counts", default="4,8,16,24")
    p.add_argument("--methods", default=",".join(MODES))
    p.add_argument("--repeats", type=int, default=3)
    p.add_argument(
        "--broker-hot",
        type=int,
        default=5,
        help="hot brokers among 50; 0 selects a purely random list",
    )
    p.add_argument(
        "--broker-seed",
        type=int,
        default=1,
        help="reproducible random broker selection",
    )
    a = p.parse_args()
    if a.repeats <= 0:
        p.error("repeats must be positive")
    if not 0 <= a.broker_hot <= 50:
        p.error("broker-hot must be between 0 and 50")
    modes = a.methods.split(",") if a.suite == "scaling" else ["full", "adaptive"]
    if any(m not in MODES for m in modes) or len(set(modes)) != len(modes):
        p.error("unknown or duplicate methods")
    shards = (
        [int(s) for s in a.shard_counts.split(",")]
        if a.suite == "scaling"
        else [a.shards]
    )
    if a.suite == "scaling" and (a.beacon_work_us or a.pressure_test):
        p.error(
            "scaling measures normal execution; use congestion for injected service cost"
        )
    root = (
        a.output or ROOT / f"exp/{a.suite}-{time.strftime('%Y%m%d-%H%M%S')}"
    ).resolve()
    root.mkdir(parents=True, exist_ok=False)
    requested_bins = a.bins
    a.bins = a.bins or root / "bin"
    shared = {}
    if not a.dry_run:
        a.bins = prepare_binaries(requested_bins, root / "bin")
        shared = provenance(a.dataset, a.bins)
        if "brokerchain" in modes:
            a.broker_file = root / "brokers.txt"
            subprocess.run(
                [
                    "go",
                    "run",
                    "./cmd/selectbrokers",
                    "-positive-value-only=true",
                    "-dataset",
                    str(a.dataset.resolve()),
                    "-tx-number",
                    str(a.tx_number),
                    "-count",
                    "50",
                    "-hot",
                    str(a.broker_hot),
                    "-seed",
                    str(a.broker_seed),
                    "-output",
                    str(a.broker_file),
                ],
                cwd=ROOT,
                check=True,
            )
            shared["broker_sha256"] = sha256_file(a.broker_file)
    cases = []
    for shard in shards:
        for repeat in range(1, a.repeats + 1):
            order = (
                modes[(repeat - 1) % len(modes) :] + modes[: (repeat - 1) % len(modes)]
            )
            for mode in order:
                cases.append(
                    dict(
                        shards=shard,
                        repeat=repeat,
                        mode=mode,
                        path=str(root / f"shards-{shard}/repeat-{repeat:02}/{mode}"),
                    )
                )
    write_json(root / "plan.json", dict(suite=a.suite, provenance=shared, cases=cases))
    records = []
    for case in cases:
        args = argparse.Namespace(**vars(a))
        args.shards, args.mode, args.output = (
            case["shards"],
            case["mode"],
            Path(case["path"]),
        )
        summary = execute(args, shared)
        if summary:
            records.append(
                dict(
                    shards=case["shards"],
                    repeat=case["repeat"],
                    path=case["path"],
                    **summary,
                )
            )
            write_csv(root / "comparison_runs.csv", records)
    finish_suite(root, records, a.suite)


def finish_suite(root, records, suite):
    groups = []
    shards = sorted({r["shards"] for r in records})
    modes = [m for m in MODES if any(r["mode"] == m for r in records)]
    for shard in shards:
        for mode in modes:
            values = [r for r in records if r["shards"] == shard and r["mode"] == mode]
            if not values:
                continue
            row = dict(shards=shard, mode=mode, repeats=len(values))
            for key in (
                "logical_tps",
                "latency_mean_ms",
                "latency_p95_ms",
                "destination_entries",
                "receiver_updates",
            ):
                nums = [r[key] for r in values if r[key] is not None]
                row[key + "_mean"] = statistics.fmean(nums) if nums else None
                row[key + "_sd"] = statistics.stdev(nums) if len(nums) > 1 else None
            groups.append(row)
    write_csv(root / "comparison_summary.csv", groups)
    if suite == "congestion" and records:
        from analyze import analyze_suite

        analyze_suite(root)
    print(f"Suite output: {root}", flush=True)


if __name__ == "__main__":
    main()
