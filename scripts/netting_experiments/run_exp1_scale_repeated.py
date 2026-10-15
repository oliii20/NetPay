#!/usr/bin/env python3
"""Run repeated exp1 scale experiments across shard counts.

Default design:
- 4 shards: 3k tx/s, 1500 ms netting window
- 8 shards: 6k tx/s, 1500 ms netting window
- 16 shards: 12k tx/s, 1500 ms netting window
- 32 shards: 40k tx/s, 2000 ms netting window, pair-rate threshold 0.8

Each shard scale runs Netting, Relay, BrokerChain, and CLPA+Broker for 5 repeats.
"""

from __future__ import annotations

import argparse
import csv
import json
import os
import sys
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import run_experiments as runner


DEFAULT_OUT_ROOT = Path("exp/netting-paper/repeated-exp1-scale")
DEFAULT_PAIR_RATE_FILE_32 = Path(
    "exp/netting-paper/sessions/exp1_32shard_baseline_300k_speed32000_2000-02/"
    "runs/exp1_baseline_netting_static_relay_method_netting_static_relay_seed1/"
    "results/netting_pair_overall_by_rate_safe.csv"
)
DEFAULT_REPEATS = 5
DEFAULT_TX_NUMBER = 300000
DEFAULT_NODE_NUM = 4
DEFAULT_BLOCK_LIMIT = 2000
DEFAULT_BLOCK_INTERVAL_MS = 2000
DEFAULT_BEACON_BLOCK_INTERVAL_MS = 500
DEFAULT_SETTLEMENT_CHUNK_SIZE = 500
DEFAULT_BATCH_SIZE = 0
DEFAULT_TIMEOUT = 1200


@dataclass(frozen=True)
class ShardProfile:
    shard_num: int
    tx_speed: int
    max_window_ms: int
    pair_rate_threshold: float = 0.0
    pair_rate_file: str = ""
    max_netting_window: int = 0
    max_netting_injection_ms: int = 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repeats", type=positive_int, default=DEFAULT_REPEATS)
    parser.add_argument("--seeds", default="", help="optional comma-separated seeds; defaults to 1..repeats")
    parser.add_argument("--dataset", type=Path, default=runner.DEFAULT_DATASET)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT_ROOT)
    parser.add_argument("--session-name", default="", help="optional repeated-session directory name")
    parser.add_argument("--tx-number", type=positive_int, default=DEFAULT_TX_NUMBER)
    parser.add_argument("--node-num", type=positive_int, default=DEFAULT_NODE_NUM)
    parser.add_argument("--block-limit", type=positive_int, default=DEFAULT_BLOCK_LIMIT)
    parser.add_argument("--block-interval-ms", type=positive_int, default=DEFAULT_BLOCK_INTERVAL_MS)
    parser.add_argument("--beacon-block-interval-ms", type=positive_int, default=DEFAULT_BEACON_BLOCK_INTERVAL_MS)
    parser.add_argument("--settlement-chunk-size", type=positive_int, default=DEFAULT_SETTLEMENT_CHUNK_SIZE)
    parser.add_argument("--batch-size", type=non_negative_int, default=DEFAULT_BATCH_SIZE)
    parser.add_argument("--pair-rate-file-32", type=Path, default=DEFAULT_PAIR_RATE_FILE_32)
    parser.add_argument("--pair-rate-threshold-32", type=float, default=0.8)
    parser.add_argument("--tx-speed-32", type=positive_int, default=40000)
    parser.add_argument("--max-window-ms-32", type=positive_int, default=2000)
    parser.add_argument("--max-netting-window-32", type=non_negative_int, default=0)
    parser.add_argument("--max-netting-injection-ms-32", type=non_negative_int, default=0)
    parser.add_argument("--network-latency-ms", type=non_negative_int, default=0)
    parser.add_argument("--go", default=os.environ.get("GO", "go"), help="Go compiler path")
    parser.add_argument("--skip-build", action="store_true")
    parser.add_argument(
        "--allow-dirty",
        action="store_true",
        help="allow building experiment binaries from a dirty Git worktree",
    )
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT)
    parser.add_argument("--progress-interval", type=int, default=30)
    args = parser.parse_args(runner.normalize_argv(sys.argv[1:]))

    repo = runner.REPO_ROOT
    if "GOCACHE" not in os.environ:
        os.environ["GOCACHE"] = str(repo / "exp" / "gocache")

    pair_rate_file_32 = runner.repo_path(args.pair_rate_file_32, repo)
    if not args.dry_run and not pair_rate_file_32.exists():
        raise SystemExit(f"32-shard pair-rate file not found: {pair_rate_file_32}")
    profiles = [
        ShardProfile(4, 3000, 1500),
        ShardProfile(8, 6000, 1500),
        ShardProfile(16, 12000, 1500),
        ShardProfile(
            32,
            args.tx_speed_32,
            args.max_window_ms_32,
            pair_rate_threshold=args.pair_rate_threshold_32,
            pair_rate_file=str(pair_rate_file_32),
            max_netting_window=args.max_netting_window_32,
            max_netting_injection_ms=args.max_netting_injection_ms_32,
        ),
    ]
    seeds = parse_seeds_or_default(args.seeds, args.repeats)
    specs = build_specs(args, profiles, seeds)
    runner.validate_specs(specs)

    out_root = runner.repo_path(args.out, repo)
    out_dir = resolve_output_dir(out_root, args.session_name)
    summary_path = out_dir / "summary.csv"
    runs_path = out_dir / "runs.jsonl"

    print(f"session: {out_dir}")
    if args.dry_run:
        for repeat_idx, spec in enumerate_specs_by_repeat(specs, seeds):
            print(f"repeat{repeat_idx:02d}/{spec.run_id}")
        return 0

    out_dir.mkdir(parents=True, exist_ok=True)
    dataset_path = runner.repo_path(args.dataset, repo)
    dataset_rows = runner.load_dataset(dataset_path, args.tx_number)
    bin_dir = out_dir / "bin"
    if not args.skip_build:
        runner.require_clean_worktree(repo, args.allow_dirty)
        runner.build_binaries(repo, bin_dir, args.go)

    write_repeated_summary_header(summary_path)
    metadata = {
        "created_at": datetime.now().isoformat(timespec="seconds"),
        "argv": sys.argv[1:],
        "repeats": args.repeats,
        "seeds": seeds,
        "dataset": str(dataset_path),
        "tx_number": args.tx_number,
        "node_num": args.node_num,
        "block_limit": args.block_limit,
        "block_interval_ms": args.block_interval_ms,
        "beacon_block_interval_ms": args.beacon_block_interval_ms,
        "batch_size": args.batch_size,
        "settlement_chunk_size": args.settlement_chunk_size,
        "network_latency_ms": args.network_latency_ms,
        "pair_rate_file_32": str(pair_rate_file_32),
        "pair_rate_threshold_32": args.pair_rate_threshold_32,
        "tx_speed_32": args.tx_speed_32,
        "max_window_ms_32": args.max_window_ms_32,
        "max_netting_window_32": args.max_netting_window_32,
        "max_netting_injection_ms_32": args.max_netting_injection_ms_32,
        "profiles": [profile.__dict__ for profile in profiles],
        "skip_build": args.skip_build,
        "allow_dirty": args.allow_dirty,
        "gocache": os.environ.get("GOCACHE", ""),
    }
    runner.write_session_metadata(out_dir / "metadata.json", metadata, repo, bin_dir, args.go)

    with runs_path.open("w", encoding="utf-8") as runs_fp:
        total = len(specs)
        for idx, (repeat_idx, spec) in enumerate(enumerate_specs_by_repeat(specs, seeds), start=1):
            run_dir = out_dir / "runs" / f"repeat_{repeat_idx:02d}" / spec.run_id
            base_port = 24000 + ((idx - 1) % 40) * 900
            print(f"[{idx}/{total}] repeat {repeat_idx:02d} shard {spec.shard_num} {spec.method}", flush=True)
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

    print(f"summary: {summary_path}")
    print(f"runs: {runs_path}")
    runner.update_latest_pointer(out_root, out_dir)
    print(f"latest: {out_root / 'latest'}")
    print(
        "plot with: python scripts/netting_experiments/plot_exp1_scale_repeated.py "
        f"--summary {summary_path}"
    )
    return 0


def parse_seeds_or_default(raw: str, repeats: int) -> list[int]:
    seeds = runner.parse_seeds(raw)
    if seeds:
        return seeds
    return list(range(1, repeats + 1))


def build_specs(args: argparse.Namespace, profiles: list[ShardProfile], seeds: list[int]) -> list[runner.RunSpec]:
    specs: list[runner.RunSpec] = []
    method_specs = [
        ("netting_static_relay", "static_relay", True),
        ("static_relay", "static_relay", False),
        ("static_broker", "static_broker", False),
        ("clpa_broker", "clpa_broker", False),
    ]
    for seed in seeds:
        for profile in profiles:
            for method, consensus_type, netting_enabled in method_specs:
                specs.append(
                    runner.RunSpec(
                        experiment="exp1_scale_repeated",
                        variable="shard_num",
                        value=str(profile.shard_num),
                        method=method,
                        consensus_type=consensus_type,
                        netting_enabled=netting_enabled,
                        workload="dataset",
                        tx_number=args.tx_number,
                        tx_speed=profile.tx_speed,
                        shard_num=profile.shard_num,
                        node_num=args.node_num,
                        block_limit=args.block_limit,
                        batch_size=args.batch_size,
                        settlement_chunk_size=args.settlement_chunk_size,
                        max_window_ms=profile.max_window_ms,
                        block_interval_ms=args.block_interval_ms,
                        beacon_block_interval_ms=args.beacon_block_interval_ms,
                        network_latency_ms=args.network_latency_ms,
                        pair_rate_file=profile.pair_rate_file if netting_enabled else "",
                        pair_rate_threshold=profile.pair_rate_threshold if netting_enabled else 0.0,
                        max_netting_window=profile.max_netting_window if netting_enabled else 0,
                        max_netting_injection_ms=profile.max_netting_injection_ms if netting_enabled else 0,
                        seed=seed,
                    )
                )
    return specs


def enumerate_specs_by_repeat(
    specs: list[runner.RunSpec],
    seeds: list[int],
) -> list[tuple[int, runner.RunSpec]]:
    seed_to_repeat = {seed: idx for idx, seed in enumerate(seeds, start=1)}
    return [(seed_to_repeat[spec.seed], spec) for spec in specs]


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


def positive_int(raw: str) -> int:
    value = int(raw)
    if value <= 0:
        raise argparse.ArgumentTypeError("must be a positive integer")
    return value


def non_negative_int(raw: str) -> int:
    value = int(raw)
    if value < 0:
        raise argparse.ArgumentTypeError("must be non-negative")
    return value


if __name__ == "__main__":
    raise SystemExit(main())
