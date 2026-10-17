#!/usr/bin/env python3
"""Run five repeated exp1 experiments at 4, 8, 16, and 24 shards.

The 4/8/16-shard profiles match the previous repeated scale experiment. The
24-shard profile matches the validated single-run command for shard 24.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from datetime import datetime
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import run_experiments as runner
import run_exp1_scale_repeated as repeated


DEFAULT_OUT_ROOT = Path("exp/netting-paper/repeated-exp1-scale-4-8-16-24")
DEFAULT_PAIR_RATE_FILE_24 = Path(
    "pre_exp_scipts/outputs/exp1_real_workload_shard24/pair_matching_rate_ranking.csv"
)
DEFAULT_SHARD_NUMS = "4,8,16,24"
DEFAULT_TIMEOUT = 3600


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repeats", type=repeated.positive_int, default=repeated.DEFAULT_REPEATS)
    parser.add_argument("--seeds", default="", help="optional comma-separated seeds; defaults to 1..repeats")
    parser.add_argument("--shard-nums", default=DEFAULT_SHARD_NUMS, help="comma-separated subset of 4,8,16,24")
    parser.add_argument("--dataset", type=Path, default=runner.DEFAULT_DATASET)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT_ROOT)
    parser.add_argument("--session-name", default="", help="optional repeated-session directory name")
    parser.add_argument("--tx-number", type=repeated.positive_int, default=repeated.DEFAULT_TX_NUMBER)
    parser.add_argument("--node-num", type=repeated.positive_int, default=repeated.DEFAULT_NODE_NUM)
    parser.add_argument("--block-limit", type=repeated.positive_int, default=repeated.DEFAULT_BLOCK_LIMIT)
    parser.add_argument(
        "--block-interval-ms",
        type=repeated.positive_int,
        default=repeated.DEFAULT_BLOCK_INTERVAL_MS,
    )
    parser.add_argument(
        "--beacon-block-interval-ms",
        type=repeated.positive_int,
        default=repeated.DEFAULT_BEACON_BLOCK_INTERVAL_MS,
    )
    parser.add_argument(
        "--settlement-chunk-size",
        type=repeated.positive_int,
        default=repeated.DEFAULT_SETTLEMENT_CHUNK_SIZE,
    )
    parser.add_argument("--batch-size", type=repeated.non_negative_int, default=repeated.DEFAULT_BATCH_SIZE)
    parser.add_argument("--pair-rate-file-24", type=Path, default=DEFAULT_PAIR_RATE_FILE_24)
    parser.add_argument("--pair-rate-threshold-24", type=float, default=0.96)
    parser.add_argument("--tx-speed-24", type=repeated.positive_int, default=24000)
    parser.add_argument("--max-window-ms-24", type=repeated.positive_int, default=2000)
    parser.add_argument("--network-latency-ms", type=repeated.non_negative_int, default=0)
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
        os.environ["GOCACHE"] = str(repo / ".exp" / "gocache")

    shard_nums = parse_shard_nums(args.shard_nums)
    pair_rate_file_24 = runner.repo_path(args.pair_rate_file_24, repo)
    profiles = build_profiles(args, pair_rate_file_24)
    profiles = [profile for profile in profiles if profile.shard_num in shard_nums]
    if not args.dry_run and 24 in shard_nums and not pair_rate_file_24.exists():
        raise SystemExit(f"24-shard pair-rate file not found: {pair_rate_file_24}")

    seeds = repeated.parse_seeds_or_default(args.seeds, args.repeats)
    specs = build_specs(args, profiles, seeds)
    runner.validate_specs(specs)

    out_root = runner.repo_path(args.out, repo)
    out_dir = repeated.resolve_output_dir(out_root, args.session_name)
    summary_path = out_dir / "summary.csv"
    runs_path = out_dir / "runs.jsonl"

    print(f"session: {out_dir}")
    if args.dry_run:
        for repeat_idx, spec in repeated.enumerate_specs_by_repeat(specs, seeds):
            print(f"repeat{repeat_idx:02d}/{spec.run_id}")
        return 0

    out_dir.mkdir(parents=True, exist_ok=True)
    dataset_path = runner.repo_path(args.dataset, repo)
    dataset_rows = runner.load_dataset(dataset_path, args.tx_number)
    bin_dir = out_dir / "bin"
    if not args.skip_build:
        runner.require_clean_worktree(repo, args.allow_dirty)
        runner.build_binaries(repo, bin_dir, args.go)

    repeated.write_repeated_summary_header(summary_path)
    metadata = {
        "created_at": datetime.now().isoformat(timespec="seconds"),
        "argv": sys.argv[1:],
        "repeats": args.repeats,
        "seeds": seeds,
        "shard_nums": sorted(shard_nums),
        "dataset": str(dataset_path),
        "tx_number": args.tx_number,
        "node_num": args.node_num,
        "block_limit": args.block_limit,
        "block_interval_ms": args.block_interval_ms,
        "beacon_block_interval_ms": args.beacon_block_interval_ms,
        "batch_size": args.batch_size,
        "settlement_chunk_size": args.settlement_chunk_size,
        "network_latency_ms": args.network_latency_ms,
        "pair_rate_file_24": str(pair_rate_file_24),
        "pair_rate_threshold_24": args.pair_rate_threshold_24,
        "tx_speed_24": args.tx_speed_24,
        "max_window_ms_24": args.max_window_ms_24,
        "profiles": [profile.__dict__ for profile in profiles],
        "timeout": args.timeout,
        "progress_interval": args.progress_interval,
        "skip_build": args.skip_build,
        "allow_dirty": args.allow_dirty,
        "gocache": os.environ.get("GOCACHE", ""),
    }
    runner.write_session_metadata(out_dir / "metadata.json", metadata, repo, bin_dir, args.go)

    with runs_path.open("w", encoding="utf-8") as runs_fp:
        total = len(specs)
        for idx, (repeat_idx, spec) in enumerate(
            repeated.enumerate_specs_by_repeat(specs, seeds),
            start=1,
        ):
            run_dir = out_dir / "runs" / f"repeat_{repeat_idx:02d}" / spec.run_id
            base_port = 24000 + ((idx - 1) % 40) * 900
            print(
                f"[{idx}/{total}] repeat {repeat_idx:02d} shard {spec.shard_num} {spec.method}",
                flush=True,
            )
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
            repeated.append_repeated_summary(summary_path, result)
            runs_fp.write(json.dumps(result, sort_keys=True) + "\n")
            runs_fp.flush()

    print(f"summary: {summary_path}")
    print(f"runs: {runs_path}")
    runner.update_latest_pointer(out_root, out_dir)
    print(f"latest: {out_root / 'latest'}")
    print(
        "plot with: python3 scripts/netting_experiments/plot_exp1_scale_repeated.py "
        f"--summary {summary_path}"
    )
    return 0


def parse_shard_nums(raw: str) -> set[int]:
    try:
        shard_nums = {
            repeated.positive_int(item.strip())
            for item in raw.split(",")
            if item.strip()
        }
    except argparse.ArgumentTypeError as err:
        raise SystemExit(f"invalid --shard-nums: {err}") from err
    allowed = {4, 8, 16, 24}
    if not shard_nums:
        raise SystemExit("--shard-nums must contain at least one shard count")
    unsupported = sorted(shard_nums - allowed)
    if unsupported:
        raise SystemExit(
            f"unsupported --shard-nums values: {unsupported}; choose from {sorted(allowed)}"
        )
    return shard_nums


def build_profiles(args: argparse.Namespace, pair_rate_file_24: Path) -> list[repeated.ShardProfile]:
    return [
        repeated.ShardProfile(4, 3000, 1500),
        repeated.ShardProfile(8, 6000, 1500),
        repeated.ShardProfile(16, 12000, 1500),
        repeated.ShardProfile(
            24,
            args.tx_speed_24,
            args.max_window_ms_24,
            pair_rate_threshold=args.pair_rate_threshold_24,
            pair_rate_file=str(pair_rate_file_24),
        ),
    ]


def build_specs(
    args: argparse.Namespace,
    profiles: list[repeated.ShardProfile],
    seeds: list[int],
) -> list[runner.RunSpec]:
    specs: list[runner.RunSpec] = []
    method_specs = [
        ("netting_static_relay", "static_relay", True),
        ("static_relay", "static_relay", False),
        ("static_broker", "static_broker", False),
        ("clpa_relay", "clpa_relay", False),
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
                        seed=seed,
                    )
                )
    return specs


if __name__ == "__main__":
    raise SystemExit(main())
