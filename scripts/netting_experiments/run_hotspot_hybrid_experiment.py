#!/usr/bin/env python3
"""Run relay vs hot-shard hybrid netting on generated hotspot workloads."""

from __future__ import annotations

import argparse
import csv
import json
import os
import socket
import sys
from datetime import datetime
from pathlib import Path
from typing import Iterable, Optional

import run_experiments as runner


REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_WORKLOAD_DIR = REPO_ROOT / "data" / "hotspot_workloads"
DEFAULT_OUT_ROOT = REPO_ROOT / "exp" / "hotspot-hybrid"
MAX_TCP_PORT = 65535


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--workload-dir", type=Path, default=DEFAULT_WORKLOAD_DIR)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT_ROOT)
    parser.add_argument("--session-name", default="")
    parser.add_argument("--overwrite", action="store_true")
    parser.add_argument("--shard-nums", default="8,16,32,64")
    parser.add_argument("--repeats", type=positive_int, default=1)
    parser.add_argument("--seed-start", type=positive_int, default=1)
    parser.add_argument("--include-broker", action="store_true")
    parser.add_argument("--broker-method", choices=["static_broker", "clpa_broker"], default="clpa_broker")
    parser.add_argument("--workload-rows", type=positive_int, default=400_000)
    parser.add_argument("--hot-shard-ratio", type=arg_ratio, default=0.25)
    parser.add_argument("--min-hot-shards", type=positive_int, default=2)
    parser.add_argument("--hot-account-ratio", type=arg_ratio, default=0.1)
    parser.add_argument("--tx-number", type=positive_int, default=300_000)
    parser.add_argument("--tx-speed", type=positive_int, default=20_000)
    parser.add_argument("--node-num", type=positive_int, default=4)
    parser.add_argument("--block-limit", type=positive_int, default=runner.BLOCK_SIZE_LIMIT)
    parser.add_argument("--block-interval-ms", type=positive_int, default=500)
    parser.add_argument("--beacon-block-interval-ms", type=positive_int, default=0)
    parser.add_argument("--batch-size", type=non_negative_int, default=0)
    parser.add_argument("--settlement-chunk-size", type=non_negative_int, default=0)
    parser.add_argument("--max-window-ms", type=positive_int, default=runner.DEFAULT_MAX_WINDOW_MS)
    parser.add_argument("--network-latency-ms", type=non_negative_int, default=0)
    parser.add_argument("--timeout", type=positive_int, default=900)
    parser.add_argument("--progress-interval", type=non_negative_int, default=1)
    parser.add_argument("--base-port", type=positive_int, default=26000)
    parser.add_argument("--port-step", type=positive_int, default=1200)
    parser.add_argument("--go", default=os.environ.get("GO", "go"))
    parser.add_argument("--skip-build", action="store_true")
    parser.add_argument("--allow-dirty", action="store_true")
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()

    repo = REPO_ROOT
    os.environ.setdefault("GOCACHE", str(repo / ".exp" / "gocache"))
    (repo / ".exp" / "gocache").mkdir(parents=True, exist_ok=True)

    shard_nums = parse_ints(args.shard_nums)
    workload_dir = runner.repo_path(args.workload_dir, repo)
    out_root = runner.repo_path(args.out, repo)
    out_dir = runner.resolve_output_dir(out_root, args.session_name, args.overwrite)
    actual_tx_number = usable_tx_number(
        workload_dir,
        shard_nums,
        args.workload_rows,
        args.hot_shard_ratio,
        args.min_hot_shards,
        args.hot_account_ratio,
        args.tx_number,
    )
    if actual_tx_number != args.tx_number:
        print(
            f"requested tx_number={args.tx_number}, but only {actual_tx_number} rows are readable by CSVSource "
            f"across the selected first-{args.workload_rows} workloads; using that value."
        )
    specs = build_specs(args, shard_nums, actual_tx_number)
    runner.validate_specs(specs)

    if args.dry_run:
        total = args.repeats * len(specs)
        for repeat_idx in range(1, args.repeats + 1):
            seed = args.seed_start + repeat_idx - 1
            for idx, spec in enumerate(specs, start=1):
                repeated_spec = runner.replace(spec, seed=seed)
                print(f"[{(repeat_idx - 1) * len(specs) + idx}/{total}] repeat_{repeat_idx:02d}/{repeated_spec.run_id}")
        return 0

    out_dir.mkdir(parents=True, exist_ok=True)
    print(f"session: {out_dir}")

    if not args.skip_build:
        runner.require_clean_worktree(repo, args.allow_dirty)
        runner.build_binaries(repo, out_dir / "bin", args.go)

    runner.write_session_metadata(
        out_dir / "metadata.json",
        {
            "created_at": datetime.now().isoformat(timespec="seconds"),
            "argv": sys.argv[1:],
            "workload_dir": str(workload_dir),
            "shard_nums": shard_nums,
            "repeats": args.repeats,
            "seed_start": args.seed_start,
            "include_broker": args.include_broker,
            "broker_method": args.broker_method if args.include_broker else "",
            "workload_rows": args.workload_rows,
            "hot_shard_ratio": args.hot_shard_ratio,
            "min_hot_shards": args.min_hot_shards,
            "hot_account_ratio": args.hot_account_ratio,
            "requested_tx_number": args.tx_number,
            "tx_number": actual_tx_number,
            "tx_speed": args.tx_speed,
            "node_num": args.node_num,
            "block_limit": args.block_limit,
            "block_interval_ms": args.block_interval_ms,
            "beacon_block_interval_ms": args.beacon_block_interval_ms,
            "batch_size": args.batch_size,
            "settlement_chunk_size": args.settlement_chunk_size,
            "max_window_ms": args.max_window_ms,
            "network_latency_ms": args.network_latency_ms,
            "base_port": args.base_port,
            "port_step": args.port_step,
            "gocache": os.environ.get("GOCACHE", ""),
        },
        repo,
        out_dir / "bin",
        args.go,
    )

    summary_path = out_dir / "summary.csv"
    runs_path = out_dir / "runs.jsonl"
    write_summary_header(summary_path)
    results: list[dict[str, object]] = []
    with runs_path.open("w", encoding="utf-8") as runs_fp:
        total = args.repeats * len(specs)
        global_idx = 0
        for repeat_idx in range(1, args.repeats + 1):
            seed = args.seed_start + repeat_idx - 1
            for spec in specs:
                global_idx += 1
                repeated_spec = runner.replace(spec, seed=seed)
                workload_path = workload_for_shard(
                    workload_dir,
                    args.workload_rows,
                    repeated_spec.shard_num,
                    args.hot_shard_ratio,
                    args.min_hot_shards,
                    args.hot_account_ratio,
                )
                dataset_rows = read_workload_rows(workload_path, actual_tx_number)
                run_dir = out_dir / "runs" / f"repeat_{repeat_idx:02d}" / repeated_spec.run_id
                base_port = allocate_base_port(
                    args.base_port + (global_idx - 1) * args.port_step,
                    args.base_port,
                    args.port_step,
                    ports_required(repeated_spec),
                )
                print(
                    f"[{global_idx}/{total}] repeat {repeat_idx:02d} {repeated_spec.run_id} "
                    f"(base_port={base_port})",
                    flush=True,
                )
                result = runner.run_one(
                    repo,
                    out_dir / "bin",
                    run_dir,
                    repeated_spec,
                    dataset_rows,
                    base_port,
                    args.timeout,
                    args.progress_interval,
                    args.overwrite,
                )
                result["repeat"] = repeat_idx
                result["workload"] = str(workload_path)
                results.append(result)
                append_summary(summary_path, result)
                runs_fp.write(json.dumps(result, ensure_ascii=False, sort_keys=True) + "\n")
                runs_fp.flush()

    comparison_path = out_dir / "hotspot_hybrid_comparison.csv"
    comparison_rows = build_comparison_rows(results)
    write_comparison(comparison_path, comparison_rows)
    aggregate_comparison_path = out_dir / "hotspot_hybrid_comparison_mean.csv"
    write_aggregate_comparison(aggregate_comparison_path, comparison_rows)
    print(f"summary: {summary_path}")
    print(f"comparison: {comparison_path}")
    print(f"comparison_mean: {aggregate_comparison_path}")

    return 0


def build_specs(args: argparse.Namespace, shard_nums: list[int], tx_number: int) -> list[runner.RunSpec]:
    specs: list[runner.RunSpec] = []
    for shard_num in shard_nums:
        hot_shards = list(range(hot_shard_count(shard_num, args.hot_shard_ratio, args.min_hot_shards)))
        specs.append(
            runner.RunSpec(
                "hotspot_hybrid",
                "shard_num",
                str(shard_num),
                method="netting_static_relay",
                consensus_type="static_relay",
                netting_enabled=True,
                workload="dataset",
                tx_number=tx_number,
                tx_speed=args.tx_speed,
                shard_num=shard_num,
                node_num=args.node_num,
                block_limit=args.block_limit,
                block_interval_ms=args.block_interval_ms,
                beacon_block_interval_ms=args.beacon_block_interval_ms,
                batch_size=args.batch_size,
                settlement_chunk_size=args.settlement_chunk_size,
                max_window_ms=args.max_window_ms,
                network_latency_ms=args.network_latency_ms,
                hot_shards=hot_shards,
                seed=1,
            )
        )
        specs.append(
            runner.RunSpec(
                "hotspot_hybrid",
                "shard_num",
                str(shard_num),
                method="static_relay",
                consensus_type="static_relay",
                netting_enabled=False,
                workload="dataset",
                tx_number=tx_number,
                tx_speed=args.tx_speed,
                shard_num=shard_num,
                node_num=args.node_num,
                block_limit=args.block_limit,
                block_interval_ms=args.block_interval_ms,
                beacon_block_interval_ms=args.beacon_block_interval_ms,
                batch_size=args.batch_size,
                settlement_chunk_size=args.settlement_chunk_size,
                max_window_ms=args.max_window_ms,
                network_latency_ms=args.network_latency_ms,
                seed=1,
            )
        )
        if args.include_broker:
            specs.append(
                runner.RunSpec(
                    "hotspot_hybrid",
                    "shard_num",
                    str(shard_num),
                    method=args.broker_method,
                    consensus_type=args.broker_method,
                    netting_enabled=False,
                    workload="dataset",
                    tx_number=tx_number,
                    tx_speed=args.tx_speed,
                    shard_num=shard_num,
                    node_num=args.node_num,
                    block_limit=args.block_limit,
                    block_interval_ms=args.block_interval_ms,
                    beacon_block_interval_ms=args.beacon_block_interval_ms,
                    batch_size=args.batch_size,
                    settlement_chunk_size=args.settlement_chunk_size,
                    max_window_ms=args.max_window_ms,
                    network_latency_ms=args.network_latency_ms,
                    seed=1,
                )
            )
    return specs


def workload_for_shard(
    workload_dir: Path,
    workload_rows: int,
    shard_num: int,
    hot_shard_ratio: float,
    min_hot_shards: int,
    hot_account_ratio: float,
) -> Path:
    hot_count = hot_shard_count(shard_num, hot_shard_ratio, min_hot_shards)
    pattern = (
        f"hotspot_25250000to25499999_rows{workload_rows}"
        f"_shard{shard_num}_hotshards{hot_count}_hotacct{ratio_tag(hot_account_ratio)}.csv"
    )
    matches = sorted(path for path in workload_dir.glob(pattern) if not path.name.endswith("_account_mapping.csv"))
    if len(matches) != 1:
        raise SystemExit(f"expected exactly one workload for shard {shard_num}, found {len(matches)} in {workload_dir}")
    return matches[0]


def read_workload_rows(path: Path, min_tx_number: int) -> list[list[str]]:
    rows: list[list[str]] = []
    with path.open(newline="", encoding="utf-8") as fp:
        reader = csv.reader(fp)
        next(reader, None)
        for row in reader:
            if not csvsource_readable(row):
                continue
            rows.append(row)
    if len(rows) < min_tx_number:
        raise SystemExit(f"{path} has only {len(rows)} rows; need {min_tx_number}")
    return rows


def usable_tx_number(
    workload_dir: Path,
    shard_nums: list[int],
    workload_rows: int,
    hot_shard_ratio: float,
    min_hot_shards: int,
    hot_account_ratio: float,
    requested: int,
) -> int:
    usable_counts = [
        count_csvsource_readable(
            workload_for_shard(
                workload_dir,
                workload_rows,
                shard_num,
                hot_shard_ratio,
                min_hot_shards,
                hot_account_ratio,
            )
        )
        for shard_num in shard_nums
    ]
    usable = min(usable_counts)
    return min(requested, usable)


def count_csvsource_readable(path: Path) -> int:
    count = 0
    with path.open(newline="", encoding="utf-8") as fp:
        reader = csv.reader(fp)
        next(reader, None)
        for row in reader:
            if csvsource_readable(row):
                count += 1
    return count


def csvsource_readable(row: list[str]) -> bool:
    if len(row) < 13:
        return False
    if not (is_hex_address(row[3]) and is_hex_address(row[4])):
        return False
    if row[3].lower() == row[4].lower():
        return False
    try:
        amount = int(row[8])
    except ValueError:
        return False
    return amount > 0


def is_hex_address(value: str) -> bool:
    if not value.startswith("0x") or len(value) != 42:
        return False
    try:
        int(value[2:], 16)
    except ValueError:
        return False
    return True


def hot_shard_count(shard_num: int, hot_shard_ratio: float, min_hot_shards: int) -> int:
    return min(shard_num, max(min_hot_shards, round(hot_shard_ratio * shard_num)))


def arg_ratio(value: str) -> float:
    ret = float(value)
    if ret <= 0 or ret > 1:
        raise argparse.ArgumentTypeError("ratio must be in (0, 1]")
    return ret


def ratio_tag(value: float) -> str:
    return f"{round(value * 100):02d}"


def ports_required(spec: runner.RunSpec) -> int:
    count = spec.shard_num * spec.node_num
    if spec.netting_enabled:
        count += 1 + spec.node_num
    return count + 1


def allocate_base_port(start: int, min_port: int, step: int, count: int) -> int:
    max_start = MAX_TCP_PORT - count + 1
    if min_port > max_start:
        raise SystemExit(
            f"need {count} consecutive ports, but --base-port {min_port} leaves only "
            f"{MAX_TCP_PORT - min_port + 1} ports"
        )

    start = max(start, min_port)
    search_from = start if start <= max_start else min_port
    for candidate in port_candidates(search_from, min_port, max_start, step):
        if ports_are_available(candidate, count):
            return candidate
    raise SystemExit(
        f"could not find {count} available consecutive ports in {min_port}-{MAX_TCP_PORT}; "
        "lower --base-port, reduce --port-step, or close stale experiment processes"
    )


def port_candidates(start: int, min_port: int, max_start: int, step: int) -> Iterable[int]:
    seen: set[int] = set()
    candidate = start
    while candidate <= max_start:
        seen.add(candidate)
        yield candidate
        candidate += step

    candidate = min_port
    while candidate < start and candidate <= max_start:
        if candidate not in seen:
            yield candidate
        candidate += step


def ports_are_available(start: int, count: int) -> bool:
    return all(port_is_available(port) for port in range(start, start + count))


def port_is_available(port: int) -> bool:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            sock.bind(("127.0.0.1", port))
        except OSError:
            return False
    return True


def write_summary_header(path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="", encoding="utf-8") as fp:
        csv.DictWriter(fp, fieldnames=summary_fields()).writeheader()


def append_summary(path: Path, row: dict[str, object]) -> None:
    with path.open("a", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=summary_fields(), extrasaction="ignore")
        writer.writerow(row)


def summary_fields() -> list[str]:
    return ["repeat", *runner.SUMMARY_FIELDS, "workload"]


def build_comparison_rows(results: list[dict[str, object]]) -> list[dict[str, object]]:
    by_repeat_shard_method = {
        (int(row.get("repeat", 1)), int(row["shard_num"]), str(row["method"])): row
        for row in results
    }
    rows: list[dict[str, object]] = []
    repeats = sorted({int(row.get("repeat", 1)) for row in results})
    shard_nums = sorted({int(row["shard_num"]) for row in results})
    for repeat in repeats:
        for shard_num in shard_nums:
            relay = by_repeat_shard_method.get((repeat, shard_num, "static_relay"))
            hybrid = by_repeat_shard_method.get((repeat, shard_num, "netting_static_relay"))
            broker = first_existing_method(
                by_repeat_shard_method,
                repeat,
                shard_num,
                ["clpa_broker", "static_broker"],
            )
            if relay is None or hybrid is None:
                continue
            rows.append(comparison_row(repeat, shard_num, relay, hybrid, broker))
    return rows


def first_existing_method(
    rows: dict[tuple[int, int, str], dict[str, object]],
    repeat: int,
    shard_num: int,
    methods: list[str],
) -> Optional[dict[str, object]]:
    for method in methods:
        row = rows.get((repeat, shard_num, method))
        if row is not None:
            return row
    return None


def comparison_row(
    repeat: int,
    shard_num: int,
    relay: dict[str, object],
    hybrid: dict[str, object],
    broker: Optional[dict[str, object]],
) -> dict[str, object]:
    relay_tps = as_float(relay.get("throughput_tps"))
    hybrid_tps = as_float(hybrid.get("throughput_tps"))
    broker_tps = as_float(broker.get("throughput_tps")) if broker else 0.0
    relay_latency = as_float(relay.get("avg_latency_s"))
    hybrid_latency = as_float(hybrid.get("avg_latency_s"))
    broker_latency = as_float(broker.get("avg_latency_s")) if broker else 0.0
    return {
        "repeat": repeat,
        "shard_num": shard_num,
        "broker_method": str(broker.get("method")) if broker else "",
        "relay_tps": relay_tps,
        "broker_tps": broker_tps,
        "hybrid_tps": hybrid_tps,
        "broker_tps_speedup": ratio(broker_tps, relay_tps),
        "tps_speedup": ratio(hybrid_tps, relay_tps),
        "hybrid_tps_speedup": ratio(hybrid_tps, relay_tps),
        "relay_avg_latency_s": relay_latency,
        "broker_avg_latency_s": broker_latency,
        "hybrid_avg_latency_s": hybrid_latency,
        "broker_avg_latency_reduction": reduction(relay_latency, broker_latency),
        "avg_latency_reduction": reduction(relay_latency, hybrid_latency),
        "hybrid_avg_latency_reduction": reduction(relay_latency, hybrid_latency),
        "relay_p95_latency_s": as_float(relay.get("p95_latency_s")),
        "broker_p95_latency_s": as_float(broker.get("p95_latency_s")) if broker else 0.0,
        "hybrid_p95_latency_s": as_float(hybrid.get("p95_latency_s")),
        "matched_intent_ratio": as_float(hybrid.get("matched_intent_ratio")),
        "matched_value_ratio": as_float(hybrid.get("matched_value_ratio")),
        "fallback_intent_ratio": as_float(hybrid.get("fallback_intent_ratio")),
        "broker_fallback_intent_ratio": as_float(broker.get("fallback_intent_ratio")) if broker else 0.0,
        "batch_count": as_float(hybrid.get("batch_count")),
    }


def write_comparison(path: Path, rows: list[dict[str, object]]) -> None:
    fields = [
        "repeat",
        "shard_num",
        "broker_method",
        "relay_tps",
        "broker_tps",
        "hybrid_tps",
        "broker_tps_speedup",
        "tps_speedup",
        "hybrid_tps_speedup",
        "relay_avg_latency_s",
        "broker_avg_latency_s",
        "hybrid_avg_latency_s",
        "broker_avg_latency_reduction",
        "avg_latency_reduction",
        "hybrid_avg_latency_reduction",
        "relay_p95_latency_s",
        "broker_p95_latency_s",
        "hybrid_p95_latency_s",
        "matched_intent_ratio",
        "matched_value_ratio",
        "fallback_intent_ratio",
        "broker_fallback_intent_ratio",
        "batch_count",
    ]
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=fields)
        writer.writeheader()
        writer.writerows(rows)


def write_aggregate_comparison(path: Path, rows: list[dict[str, object]]) -> None:
    fields = [
        "shard_num",
        "samples",
        "broker_method",
        "relay_tps",
        "broker_tps",
        "hybrid_tps",
        "broker_tps_speedup",
        "tps_speedup",
        "hybrid_tps_speedup",
        "relay_avg_latency_s",
        "broker_avg_latency_s",
        "hybrid_avg_latency_s",
        "broker_avg_latency_reduction",
        "avg_latency_reduction",
        "hybrid_avg_latency_reduction",
        "relay_p95_latency_s",
        "broker_p95_latency_s",
        "hybrid_p95_latency_s",
        "matched_intent_ratio",
        "matched_value_ratio",
        "fallback_intent_ratio",
        "broker_fallback_intent_ratio",
        "batch_count",
    ]
    by_shard: dict[int, list[dict[str, object]]] = {}
    for row in rows:
        by_shard.setdefault(int(row["shard_num"]), []).append(row)
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=fields)
        writer.writeheader()
        for shard_num in sorted(by_shard):
            shard_rows = by_shard[shard_num]
            broker_methods = sorted({str(row.get("broker_method", "")) for row in shard_rows if row.get("broker_method")})
            out: dict[str, object] = {
                "shard_num": shard_num,
                "samples": len(shard_rows),
                "broker_method": ",".join(broker_methods),
            }
            for field in fields:
                if field in {"shard_num", "samples", "broker_method"}:
                    continue
                out[field] = mean(as_float(row.get(field)) for row in shard_rows)
            writer.writerow(out)


def ratio(numerator: float, denominator: float) -> float:
    return numerator / denominator if denominator > 0 else 0.0


def reduction(baseline: float, candidate: float) -> float:
    return (baseline - candidate) / baseline if baseline > 0 else 0.0


def mean(values: Iterable[float]) -> float:
    vals = [value for value in values if value == value]
    return sum(vals) / len(vals) if vals else 0.0


def parse_ints(value: str) -> list[int]:
    ret = [int(part) for part in value.split(",") if part.strip()]
    if not ret or any(item <= 0 for item in ret):
        raise argparse.ArgumentTypeError("expected comma-separated positive integers")
    return ret


def positive_int(value: str) -> int:
    ret = int(value)
    if ret <= 0:
        raise argparse.ArgumentTypeError("value must be positive")
    return ret


def non_negative_int(value: str) -> int:
    ret = int(value)
    if ret < 0:
        raise argparse.ArgumentTypeError("value must be non-negative")
    return ret


def as_float(value: object) -> float:
    try:
        return float(value)
    except (TypeError, ValueError):
        return 0.0


if __name__ == "__main__":
    raise SystemExit(main())
