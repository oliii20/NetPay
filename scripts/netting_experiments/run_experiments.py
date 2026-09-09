#!/usr/bin/env python3
"""Run the seven netting experiments and aggregate their metrics.

The runner is intentionally dependency-free: it uses the standard library to
derive workloads from selectedTxs_300K.csv, generate BlockEmulator-X configs,
launch local processes, and summarize CSV metrics. Plotting is handled by
plot_experiments.py.
"""

from __future__ import annotations

import argparse
import csv
import json
import math
import os
import random
import shutil
import signal
import subprocess
import sys
import time
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path
from typing import Iterable


DEFAULT_DATASET = Path(
    "/Users/ljn/Desktop/Newidea2026July/block-emulator-main-画预实验的图/selectedTxs_300K.csv"
)
BEACON_SHARD_ID = 2147483646
SOLVER_SHARD_ID = 2147483645
SUPERVISOR_SHARD_ID = 2147483647
CSV_HEADER = [f"col{i}" for i in range(17)]


@dataclass(frozen=True)
class RunSpec:
    experiment: str
    variable: str
    value: str
    method: str = "netting_static_relay"
    consensus_type: str = "static_relay"
    netting_enabled: bool = True
    matcher_mode: str = "full"
    workload: str = "dataset"
    tx_number: int = 120
    tx_speed: int = 120
    shard_num: int = 4
    node_num: int = 4
    batch_size: int = 20
    max_window_ms: int = 1000
    block_interval_ms: int = 500
    network_latency_ms: int = 0
    balance_ratio: float = 1.0
    shard_block_intervals_ms: dict[int, int] = field(default_factory=dict)
    seed: int = 1

    @property
    def run_id(self) -> str:
        safe = "_".join(
            [
                self.experiment,
                self.method,
                self.variable,
                self.value,
                f"seed{self.seed}",
            ]
        )
        return "".join(ch if ch.isalnum() or ch in "._-" else "_" for ch in safe)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", choices=["smoke", "pilot", "full"], default="pilot")
    parser.add_argument("--experiments", default="all", help="comma list from exp1..exp7 or all")
    parser.add_argument("--dataset", type=Path, default=DEFAULT_DATASET)
    parser.add_argument("--out", type=Path, default=Path(".exp/netting-paper"))
    parser.add_argument("--seeds", default="", help="override seeds, e.g. 1,2,3")
    parser.add_argument("--skip-build", action="store_true")
    parser.add_argument("--dry-run", action="store_true")
    parser.add_argument("--timeout", type=int, default=500)
    parser.add_argument(
        "--progress-interval",
        type=int,
        default=1,
        help="seconds between per-run elapsed-time progress refreshes; use 0 to disable",
    )
    args = parser.parse_args()

    repo = Path.cwd()
    selected = selected_experiments(args.experiments)
    seeds = parse_seeds(args.seeds) or default_seeds(args.profile)
    specs = build_specs(args.profile, selected, seeds)
    args.out.mkdir(parents=True, exist_ok=True)

    if args.dry_run:
        for spec in specs:
            print(spec.run_id)
        return 0

    dataset_rows = load_dataset(args.dataset)
    if not args.skip_build:
        build_binaries(repo, args.out / "bin")

    summary_path = args.out / "summary.csv"
    runs_path = args.out / "runs.jsonl"
    write_summary_header(summary_path)
    with runs_path.open("a", encoding="utf-8") as runs_fp:
        for idx, spec in enumerate(specs):
            run_dir = args.out / "runs" / spec.run_id
            base_port = 24000 + (idx % 40) * 900
            print(f"[{idx + 1}/{len(specs)}] {spec.run_id}", flush=True)
            result = run_one(
                repo,
                args.out / "bin",
                run_dir,
                spec,
                dataset_rows,
                base_port,
                args.timeout,
                args.progress_interval,
            )
            append_summary(summary_path, result)
            runs_fp.write(json.dumps(result, sort_keys=True) + "\n")
            runs_fp.flush()

    print(f"summary: {summary_path}")
    print(f"runs: {runs_path}")
    return 0


def selected_experiments(raw: str) -> set[str]:
    if raw == "all":
        return {f"exp{i}" for i in range(1, 8)}
    selected = {part.strip() for part in raw.split(",") if part.strip()}
    allowed = {f"exp{i}" for i in range(1, 8)}
    unknown = selected - allowed
    if unknown:
        raise SystemExit(f"unknown experiments: {sorted(unknown)}")
    return selected


def parse_seeds(raw: str) -> list[int]:
    if not raw:
        return []
    return [int(part) for part in raw.split(",") if part.strip()]


def default_seeds(profile: str) -> list[int]:
    if profile == "full":
        return [1, 2, 3, 4, 5]
    return [1]


def build_specs(profile: str, selected: set[str], seeds: list[int]) -> list[RunSpec]:
    if profile == "smoke":
        tx_number, tx_speed = 32, 160
        balance_points = [0.0, 1.0]
        batch_points = [10, 20]
        window_points = [250, 1000]
        scale_points = [4]
        latency_points = [0, 50]
    elif profile == "pilot":
        tx_number, tx_speed = 120, 160
        balance_points = [0.0, 0.25, 0.5, 0.75, 1.0]
        batch_points = [10, 20, 50, 100, 200]
        window_points = [100, 500, 1000, 2000, 5000]
        scale_points = [4, 8, 16]
        latency_points = [0, 25, 50, 100]
    else:
        tx_number, tx_speed = 50000, 2000
        balance_points = [0.0, 0.25, 0.5, 0.75, 1.0]
        batch_points = [10, 20, 50, 100, 200]
        window_points = [100, 500, 1000, 2000, 5000]
        scale_points = [4, 8, 16]
        latency_points = [0, 25, 50, 100, 200]

    specs: list[RunSpec] = []
    for seed in seeds:
        if "exp1" in selected:
            specs.extend(
                [
                    RunSpec(
                        "exp1_baseline",
                        "method",
                        "static_relay",
                        method="static_relay",
                        consensus_type="static_relay",
                        netting_enabled=False,
                        workload="dataset",
                        tx_number=tx_number,
                        tx_speed=tx_speed,
                        seed=seed,
                    ),
                    RunSpec(
                        "exp1_baseline",
                        "method",
                        "static_broker",
                        method="static_broker",
                        consensus_type="static_broker",
                        netting_enabled=False,
                        workload="dataset",
                        tx_number=tx_number,
                        tx_speed=tx_speed,
                        seed=seed,
                    ),
                    RunSpec(
                        "exp1_baseline",
                        "method",
                        "netting_static_relay",
                        method="netting_static_relay",
                        consensus_type="static_relay",
                        netting_enabled=True,
                        workload="dataset",
                        tx_number=tx_number,
                        tx_speed=tx_speed,
                        seed=seed,
                    ),
                ]
            )
        if "exp2" in selected:
            for ratio in balance_points:
                specs.append(
                    RunSpec(
                        "exp2_balance",
                        "reverse_ratio",
                        f"{ratio:.2f}",
                        workload="balance",
                        tx_number=tx_number,
                        tx_speed=tx_speed,
                        balance_ratio=ratio,
                        seed=seed,
                    )
                )
        if "exp3" in selected:
            for batch_size in batch_points:
                specs.append(
                    RunSpec(
                        "exp3_batch_size",
                        "batch_size",
                        str(batch_size),
                        workload="balance",
                        tx_number=tx_number,
                        tx_speed=tx_speed,
                        batch_size=batch_size,
                        balance_ratio=1.0,
                        seed=seed,
                    )
                )
        if "exp4" in selected:
            for window_ms in window_points:
                specs.append(
                    RunSpec(
                        "exp4_window_duration",
                        "max_window_ms",
                        str(window_ms),
                        workload="balance",
                        tx_number=tx_number,
                        tx_speed=tx_speed,
                        max_window_ms=window_ms,
                        balance_ratio=0.75,
                        seed=seed,
                    )
                )
        if "exp5" in selected:
            for mode in ["exact_only", "best_fit", "full"]:
                specs.append(
                    RunSpec(
                        "exp5_matcher_ablation",
                        "matcher_mode",
                        mode,
                        workload="ablation",
                        tx_number=tx_number,
                        tx_speed=tx_speed,
                        batch_size=max(200, tx_number),
                        max_window_ms=max(5000, window_points[-1]),
                        matcher_mode=mode,
                        seed=seed,
                    )
                )
        if "exp6" in selected:
            for shard_num in scale_points:
                specs.append(
                    RunSpec(
                        "exp6_scale",
                        "shard_num",
                        str(shard_num),
                        workload="balance",
                        tx_number=max(tx_number, shard_num * 24),
                        tx_speed=tx_speed,
                        shard_num=shard_num,
                        balance_ratio=1.0,
                        seed=seed,
                    )
                )
        if "exp7" in selected:
            for latency in latency_points:
                intervals = {0: 300, 1: 700, 2: 500, 3: 900}
                specs.append(
                    RunSpec(
                        "exp7_async_latency",
                        "network_latency_ms",
                        str(latency),
                        workload="balance",
                        tx_number=tx_number,
                        tx_speed=tx_speed,
                        network_latency_ms=latency,
                        balance_ratio=0.75,
                        shard_block_intervals_ms=intervals,
                        seed=seed,
                    )
                )
    return specs


def load_dataset(path: Path) -> list[list[str]]:
    if not path.exists():
        raise SystemExit(f"dataset not found: {path}")
    rows: list[list[str]] = []
    with path.open(newline="", encoding="utf-8") as fp:
        reader = csv.reader(fp)
        for line in reader:
            if valid_tx_line(line):
                rows.append(line[:17] + [""] * max(0, 17 - len(line)))
    if not rows:
        raise SystemExit(f"dataset has no usable transfer rows: {path}")
    return rows


def valid_tx_line(line: list[str]) -> bool:
    if len(line) < 13:
        return False
    if not (is_hex_address(line[3]) and is_hex_address(line[4])):
        return False
    if line[3].lower() == line[4].lower():
        return False
    try:
        return int(line[8]) > 0
    except ValueError:
        return False


def is_hex_address(value: str) -> bool:
    if not value.startswith("0x") or len(value) != 42:
        return False
    try:
        int(value[2:], 16)
        return True
    except ValueError:
        return False


def build_binaries(repo: Path, bin_dir: Path) -> None:
    bin_dir.mkdir(parents=True, exist_ok=True)
    commands = [
        ["go", "mod", "download"],
        ["go", "build", "-o", str(bin_dir / "consensusnode"), "./cmd/consensusnode"],
        ["go", "build", "-o", str(bin_dir / "beaconnode"), "./cmd/beaconnode"],
        ["go", "build", "-o", str(bin_dir / "solver"), "./cmd/solver"],
        ["go", "build", "-o", str(bin_dir / "supervisor"), "./cmd/supervisor"],
    ]
    for cmd in commands:
        subprocess.run(cmd, cwd=repo, check=True)


def run_one(
    repo: Path,
    bin_dir: Path,
    run_dir: Path,
    spec: RunSpec,
    dataset_rows: list[list[str]],
    base_port: int,
    timeout_s: int,
    progress_interval_s: int,
) -> dict[str, object]:
    if run_dir.exists():
        shutil.rmtree(run_dir)
    run_dir.mkdir(parents=True)
    workload_path = run_dir / "workload.csv"
    config_path = run_dir / "config.yaml"
    ip_table_path = run_dir / "ip_table.json"
    write_workload(workload_path, spec, dataset_rows)
    write_config(config_path, run_dir, workload_path, spec)
    write_ip_table(ip_table_path, spec, base_port)

    processes: list[subprocess.Popen[bytes]] = []
    process_log_dir = run_dir / "process_logs"
    process_log_dir.mkdir()
    started_at = time.time()
    try:
        launch_cluster(repo, bin_dir, process_log_dir, config_path, ip_table_path, spec, processes)
        supervisor = processes[-1]
        wait_with_progress(supervisor, spec.run_id, started_at, timeout_s, progress_interval_s)
        if supervisor.returncode != 0:
            raise RuntimeError(f"supervisor exited with {supervisor.returncode}")
    finally:
        terminate_all(processes)
    elapsed = time.time() - started_at
    print(f"  done {spec.run_id} in {format_duration(elapsed)}", flush=True)
    log_hits = scan_logs(run_dir)
    if log_hits:
        raise RuntimeError(f"{spec.run_id} has ERROR/WARN logs: {log_hits[:3]}")
    return summarize_run(run_dir, spec, elapsed)


def wait_with_progress(
    proc: subprocess.Popen[bytes],
    run_id: str,
    started_at: float,
    timeout_s: int,
    progress_interval_s: int,
) -> None:
    if progress_interval_s <= 0:
        proc.wait(timeout=timeout_s)
        return

    deadline = started_at + timeout_s
    interactive = sys.stdout.isatty()
    printed_progress = False
    last_progress_len = 0
    next_progress_at = started_at
    while True:
        now = time.time()
        remaining = deadline - now
        if remaining <= 0:
            if interactive and printed_progress:
                sys.stdout.write("\n")
                sys.stdout.flush()
            raise subprocess.TimeoutExpired(proc.args, timeout_s)
        if now >= next_progress_at:
            last_progress_len = write_progress(
                run_id,
                time.time() - started_at,
                timeout_s,
                interactive,
                last_progress_len,
            )
            printed_progress = True
            next_progress_at = now + progress_interval_s
        wait_s = min(max(0.1, next_progress_at - time.time()), remaining)
        try:
            proc.wait(timeout=wait_s)
            if interactive and printed_progress:
                sys.stdout.write("\n")
                sys.stdout.flush()
            return
        except subprocess.TimeoutExpired:
            continue


def write_progress(
    run_id: str,
    elapsed_s: float,
    timeout_s: int,
    interactive: bool,
    last_len: int,
) -> int:
    message = f"  running {run_id}: elapsed {format_duration(elapsed_s)} / timeout {format_duration(timeout_s)}"
    if interactive:
        padding = " " * max(0, last_len - len(message))
        sys.stdout.write(f"\r{message}{padding}")
        sys.stdout.flush()
    else:
        print(message, flush=True)
    return len(message)


def format_duration(seconds: float) -> str:
    total = max(0, int(round(seconds)))
    hours, remainder = divmod(total, 3600)
    minutes, secs = divmod(remainder, 60)
    if hours:
        return f"{hours:d}h{minutes:02d}m{secs:02d}s"
    if minutes:
        return f"{minutes:d}m{secs:02d}s"
    return f"{secs:d}s"


def launch_cluster(
    repo: Path,
    bin_dir: Path,
    log_dir: Path,
    config_path: Path,
    ip_table_path: Path,
    spec: RunSpec,
    processes: list[subprocess.Popen[bytes]],
) -> None:
    def launch(name: str, binary: str, shard_id: int, node_id: int) -> None:
        fp = (log_dir / f"{name}.log").open("wb")
        proc = subprocess.Popen(
            [
                str(bin_dir / binary),
                f"-config={config_path}",
                f"-ip_table={ip_table_path}",
                f"-shard_id={shard_id}",
                f"-node_id={node_id}",
            ],
            cwd=repo,
            stdout=fp,
            stderr=subprocess.STDOUT,
        )
        processes.append(proc)

    for shard_id in range(spec.shard_num):
        for node_id in range(spec.node_num):
            launch(f"shard-{shard_id}-node-{node_id}", "consensusnode", shard_id, node_id)
    if spec.netting_enabled:
        for node_id in range(spec.node_num):
            launch(f"beacon-node-{node_id}", "beaconnode", BEACON_SHARD_ID, node_id)
        launch("solver", "solver", SOLVER_SHARD_ID, 0)
    launch("supervisor", "supervisor", SUPERVISOR_SHARD_ID, 0)


def terminate_all(processes: Iterable[subprocess.Popen[bytes]]) -> None:
    for proc in processes:
        if proc.poll() is None:
            proc.send_signal(signal.SIGTERM)
    deadline = time.time() + 5
    for proc in processes:
        if proc.poll() is None:
            remaining = max(0.1, deadline - time.time())
            try:
                proc.wait(timeout=remaining)
            except subprocess.TimeoutExpired:
                proc.kill()


def scan_logs(run_dir: Path) -> list[str]:
    hits: list[str] = []
    for root in [run_dir / "logs", run_dir / "process_logs"]:
        if not root.exists():
            continue
        for path in root.rglob("*.log"):
            text = path.read_text(errors="ignore")
            for line in text.splitlines():
                if "level=ERROR" in line or "level=WARN" in line:
                    hits.append(f"{path.name}: {line[:180]}")
    return hits


def write_workload(path: Path, spec: RunSpec, dataset_rows: list[list[str]]) -> None:
    if spec.workload == "dataset":
        rows = dataset_sample(dataset_rows, spec)
    elif spec.workload == "balance":
        rows = synthetic_balance(spec)
    elif spec.workload == "ablation":
        rows = synthetic_ablation(spec)
    else:
        raise ValueError(f"unknown workload {spec.workload}")
    if len(rows) < spec.tx_number:
        raise ValueError(f"{spec.run_id} generated {len(rows)} rows for tx_number={spec.tx_number}")
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.writer(fp)
        writer.writerow(CSV_HEADER)
        writer.writerows(rows)


def dataset_sample(dataset_rows: list[list[str]], spec: RunSpec) -> list[list[str]]:
    rng = random.Random(spec.seed)
    if len(dataset_rows) <= spec.tx_number:
        sample = list(dataset_rows)
    else:
        start = rng.randrange(0, len(dataset_rows) - spec.tx_number)
        sample = dataset_rows[start : start + spec.tx_number]
    return [normalize_csv_row(row, idx + 1) for idx, row in enumerate(sample)]


def synthetic_balance(spec: RunSpec) -> list[list[str]]:
    rng = random.Random(spec.seed)
    forward_count = max(1, math.ceil(spec.tx_number / (1.0 + spec.balance_ratio)))
    reverse_count = max(0, spec.tx_number - forward_count)
    rows: list[list[str]] = []
    for idx in range(forward_count):
        source = idx % spec.shard_num
        dest = (source + 1) % spec.shard_num
        amount = 100 + rng.randint(0, 40)
        rows.append(make_row(source, dest, idx, amount, spec.shard_num))
    for idx in range(reverse_count):
        dest = idx % spec.shard_num
        source = (dest + 1) % spec.shard_num
        amount = 100 + rng.randint(0, 40)
        rows.append(make_row(source, dest, forward_count + idx, amount, spec.shard_num))
    rng.shuffle(rows)
    return rows


def synthetic_ablation(spec: RunSpec) -> list[list[str]]:
    rows: list[list[str]] = []
    idx = 0
    for _ in range(max(1, math.ceil(spec.tx_number / 12))):
        rows.append(make_row(0, 1, idx, 100, spec.shard_num)); idx += 1
        rows.append(make_row(1, 0, idx, 100, spec.shard_num)); idx += 1
        rows.append(make_row(0, 1, idx, 90, spec.shard_num)); idx += 1
        rows.append(make_row(1, 0, idx, 95, spec.shard_num)); idx += 1
        rows.append(make_row(0, 1, idx, 120, spec.shard_num)); idx += 1
        rows.append(make_row(1, 0, idx, 70, spec.shard_num)); idx += 1
        rows.append(make_row(1, 0, idx, 50, spec.shard_num)); idx += 1
        rows.append(make_row(2 % spec.shard_num, 3 % spec.shard_num, idx, 80, spec.shard_num)); idx += 1
        rows.append(make_row(3 % spec.shard_num, 2 % spec.shard_num, idx, 80, spec.shard_num)); idx += 1
        rows.append(make_row(2 % spec.shard_num, 3 % spec.shard_num, idx, 110, spec.shard_num)); idx += 1
        rows.append(make_row(3 % spec.shard_num, 2 % spec.shard_num, idx, 60, spec.shard_num)); idx += 1
        rows.append(make_row(3 % spec.shard_num, 2 % spec.shard_num, idx, 55, spec.shard_num)); idx += 1
    return rows[: spec.tx_number]


def normalize_csv_row(row: list[str], nonce: int) -> list[str]:
    out = (row[:17] + [""] * 17)[:17]
    out[0] = out[0] or str(nonce)
    out[6] = "0"
    out[7] = "0"
    out[12] = ""
    return out


def make_row(source: int, dest: int, idx: int, amount: int, shard_num: int) -> list[str]:
    row = [""] * 17
    row[0] = str(idx)
    row[3] = address_for_shard(source, shard_num, idx * 2 + 1)
    row[4] = address_for_shard(dest, shard_num, idx * 2 + 2)
    row[6] = "0"
    row[7] = "0"
    row[8] = str(amount)
    row[12] = ""
    return row


def address_for_shard(shard: int, shard_num: int, nonce: int) -> str:
    data = bytearray(20)
    data[0:4] = nonce.to_bytes(4, "big", signed=False)
    suffix = shard + shard_num * (nonce + 1)
    data[-4:] = suffix.to_bytes(4, "big", signed=False)
    return "0x" + data.hex()


def write_config(path: Path, run_dir: Path, workload_path: Path, spec: RunSpec) -> None:
    intervals = ""
    if spec.shard_block_intervals_ms:
        intervals = "  shard_block_intervals_ms:\n"
        for shard, interval in sorted(spec.shard_block_intervals_ms.items()):
            if shard < spec.shard_num:
                intervals += f"    {shard}: {interval}\n"
    content = f"""system:
  shard_num: {spec.shard_num}
  node_num: {spec.node_num}
  limit: 100
  consensus_type: "{spec.consensus_type}"
  log:
    log_dir: "{run_dir}/"
    log_level: "info"

consensus_node:
  blockchain:
    bloom_filter:
      bitset_len: 4096
      filter_hash_func: [sha256, sha512, sha1]
    storage:
      block_storage_type: "bolt"
      trie_storage_type: "eth_level_db"
      bolt:
        file_path_dir: "{run_dir}/boltdb/"
      eth_storage:
        is_memory_db: true
        level_file_path_dir: "{run_dir}/trie_db/"
        old_state_root: ""
    vm:
      chain_id: 11
      vm_state_dir: "{run_dir}/vm_state/"
  tx_pool:
    type: "number"
  block_interval: {spec.block_interval_ms}
{intervals}  block_record_dir: "{run_dir}/block_record/"

netting:
  enabled: {str(spec.netting_enabled).lower()}
  metrics_enabled: true
  batch_size: {spec.batch_size}
  max_window_duration_ms: {spec.max_window_ms}
  solver_tick_interval_ms: 100
  matcher_mode: "{spec.matcher_mode}"
  batch_store_path: "{run_dir}/netting/solver.db"
  beacon_store_path: "{run_dir}/netting/beacon.db"

supervisor:
  tx_number: {spec.tx_number}
  tx_injection_speed: {spec.tx_speed}
  result_output_dir: "{run_dir}/results/"
  epoch_duration: 50
  tx_source:
    tx_source_type: "csv_source"
    tx_source_file: "{workload_path}"
    exclude_contract_txs: true
  broker_module:
    broker_file_path: "./pkg/broker/broker"
    broker_num: 50

network:
  bandwidth: 1000000
  latency: {spec.network_latency_ms}
  communication_mode: "direct"
  libp2p:
    bootstrap_key_fp: "./pkg/network/connlibp2p/bootstrap.key"
    bootstrap_peer: "12D3KooWR6siPMZ2sMFKbgwaJFwQfnKczuPZnxHfyy1dHTzZSAUY"
    bootstrap_ip: "127.0.0.1"
    bootstrap_port: 12345
"""
    path.write_text(content, encoding="utf-8")


def write_ip_table(path: Path, spec: RunSpec, base_port: int) -> None:
    table: dict[str, dict[str, str]] = {}
    port = base_port
    for shard in range(spec.shard_num):
        table[str(shard)] = {}
        for node in range(spec.node_num):
            table[str(shard)][str(node)] = f"127.0.0.1:{port}"
            port += 1
    if spec.netting_enabled:
        table[str(SOLVER_SHARD_ID)] = {"0": f"127.0.0.1:{port}"}
        port += 1
        table[str(BEACON_SHARD_ID)] = {}
        for node in range(spec.node_num):
            table[str(BEACON_SHARD_ID)][str(node)] = f"127.0.0.1:{port}"
            port += 1
    table[str(SUPERVISOR_SHARD_ID)] = {"0": f"127.0.0.1:{port}"}
    path.write_text(json.dumps(table, indent=2, sort_keys=True), encoding="utf-8")


def summarize_run(run_dir: Path, spec: RunSpec, elapsed_s: float) -> dict[str, object]:
    result: dict[str, object] = {
        "run_id": spec.run_id,
        "experiment": spec.experiment,
        "variable": spec.variable,
        "value": spec.value,
        "method": spec.method,
        "seed": spec.seed,
        "shard_num": spec.shard_num,
        "node_num": spec.node_num,
        "tx_number": spec.tx_number,
        "batch_size": spec.batch_size,
        "max_window_ms": spec.max_window_ms,
        "matcher_mode": spec.matcher_mode,
        "network_latency_ms": spec.network_latency_ms,
        "elapsed_s": elapsed_s,
    }
    if spec.netting_enabled:
        result.update(summarize_netting(run_dir, spec, elapsed_s))
    elif spec.consensus_type == "static_relay":
        result.update(summarize_relay(run_dir, spec))
    else:
        result.update(summarize_broker(run_dir, spec))
    return result


def summarize_netting(run_dir: Path, spec: RunSpec, elapsed_s: float) -> dict[str, object]:
    batches = read_dicts(run_dir / "results" / "netting_batch_metrics.csv")
    intents = read_dicts(run_dir / "results" / "netting_intent_metrics.csv")
    completed = [row for row in intents if float_or_zero(row.get("EndToEndLatencyNs")) > 0]
    batch_count = len(batches)
    intent_count = len(intents)
    e2e = [float_or_zero(row.get("EndToEndLatencyNs")) / 1e9 for row in completed]
    capital = [float_or_zero(row.get("CapitalLockDurationNs")) / 1e9 for row in completed]
    starts = [parse_timestamp(row.get("CreatedAt", "")) for row in completed]
    ends = [parse_timestamp(row.get("CompletedAt", "")) for row in completed]
    pairs = [(start, end) for start, end in zip(starts, ends) if start > 0 and end > 0]
    if pairs:
        duration = max(end for _, end in pairs) - min(start for start, _ in pairs)
    else:
        duration = elapsed_s
    duration = max(1e-9, duration)
    throughput = len(completed) / duration
    beacon_bytes = [
        float_or_zero(row.get("PreprepareBytes"))
        + float_or_zero(row.get("PrepareBytes"))
        + float_or_zero(row.get("CommitBytes"))
        for row in batches
    ]
    intent_total = sum(float_or_zero(row.get("IntentCount")) for row in batches)
    return {
        "tx_committed": len(completed),
        "throughput_tps": throughput,
        "avg_latency_s": mean(e2e),
        "p50_latency_s": percentile(e2e, 50),
        "p95_latency_s": percentile(e2e, 95),
        "matched_value_ratio": weighted_ratio(batches, "MatchedValueRatio", "IntentCount"),
        "fallback_value_ratio": weighted_ratio(batches, "FallbackValueRatio", "IntentCount"),
        "matched_intent_ratio": weighted_ratio(batches, "MatchedIntentRatio", "IntentCount"),
        "fallback_intent_ratio": weighted_ratio(batches, "FallbackIntentRatio", "IntentCount"),
        "split_allocations": sum(float_or_zero(row.get("SplitAllocationCount")) for row in batches),
        "match_time_ms": mean([float_or_zero(row.get("MatchTimeNs")) / 1e6 for row in batches]),
        "proof_time_ms": mean([float_or_zero(row.get("ProofVerificationTimeNs")) / 1e6 for row in batches]),
        "beacon_bytes_per_intent": sum(beacon_bytes) / max(1.0, intent_total),
        "cross_messages_per_tx": (batch_count + spec.shard_num * batch_count) / max(1.0, intent_count),
        "capital_lock_s": mean(capital),
        "watermark_skew": mean([float_or_zero(row.get("WatermarkSkew")) for row in batches]),
        "batch_count": batch_count,
    }


def summarize_relay(run_dir: Path, spec: RunSpec) -> dict[str, object]:
    brief = read_dicts(run_dir / "results" / "relay_stats_brief_info.csv")
    detail = read_dicts(run_dir / "results" / "relay_stats_detail_tx_info.csv")
    e2e = [
        parse_duration_from_times(row.get("Tx create time", ""), row.get("Tx finally commit time", ""))
        for row in detail
        if row.get("Tx finally commit time")
    ]
    cross_count = sum(1 for row in detail if row.get("Is cross-shard tx or not") == "true")
    return {
        "tx_committed": len(detail),
        "throughput_tps": mean([float_or_zero(row.get("Avg. TPS of this epoch (txs per second)")) for row in brief]),
        "avg_latency_s": mean([x for x in e2e if x > 0]),
        "p50_latency_s": percentile(e2e, 50),
        "p95_latency_s": percentile(e2e, 95),
        "matched_value_ratio": 0.0,
        "fallback_value_ratio": 1.0,
        "matched_intent_ratio": 0.0,
        "fallback_intent_ratio": 1.0,
        "split_allocations": 0.0,
        "match_time_ms": 0.0,
        "proof_time_ms": 0.0,
        "beacon_bytes_per_intent": 0.0,
        "cross_messages_per_tx": 2.0 * cross_count / max(1.0, len(detail)),
        "capital_lock_s": 0.0,
        "watermark_skew": 0.0,
        "batch_count": 0,
    }


def summarize_broker(run_dir: Path, spec: RunSpec) -> dict[str, object]:
    brief = read_dicts(run_dir / "results" / "broker_stats_brief_info.csv")
    detail = read_dicts(run_dir / "results" / "broker_stats_detail_tx_info.csv")
    e2e = [
        parse_duration_from_times(row.get("Tx create time", ""), row.get("Tx finally commit time", ""))
        for row in detail
        if row.get("Tx finally commit time")
    ]
    broker_count = sum(1 for row in detail if row.get("Mechanism") == "Broker")
    fallback_count = sum(1 for row in detail if row.get("Mechanism") == "FallbackToRelay")
    return {
        "tx_committed": len(detail),
        "throughput_tps": mean([float_or_zero(row.get("Avg. TPS of this epoch (txs per second)")) for row in brief]),
        "avg_latency_s": mean([x for x in e2e if x > 0]),
        "p50_latency_s": percentile(e2e, 50),
        "p95_latency_s": percentile(e2e, 95),
        "matched_value_ratio": 0.0,
        "fallback_value_ratio": 1.0,
        "matched_intent_ratio": 0.0,
        "fallback_intent_ratio": 1.0,
        "split_allocations": 0.0,
        "match_time_ms": 0.0,
        "proof_time_ms": 0.0,
        "beacon_bytes_per_intent": 0.0,
        "cross_messages_per_tx": (2.0 * broker_count + 2.0 * fallback_count) / max(1.0, len(detail)),
        "capital_lock_s": 0.0,
        "watermark_skew": 0.0,
        "batch_count": 0,
    }


def read_dicts(path: Path) -> list[dict[str, str]]:
    if not path.exists():
        return []
    with path.open(newline="", encoding="utf-8") as fp:
        return list(csv.DictReader(fp))


def weighted_ratio(rows: list[dict[str, str]], ratio_key: str, weight_key: str) -> float:
    total = sum(float_or_zero(row.get(weight_key)) for row in rows)
    if total <= 0:
        return 0.0
    return sum(float_or_zero(row.get(ratio_key)) * float_or_zero(row.get(weight_key)) for row in rows) / total


def float_or_zero(value: object) -> float:
    try:
        if value is None or value == "":
            return 0.0
        return float(value)
    except (TypeError, ValueError):
        return 0.0


def mean(values: Iterable[float]) -> float:
    vals = [v for v in values if math.isfinite(v)]
    return sum(vals) / len(vals) if vals else 0.0


def percentile(values: Iterable[float], pct: float) -> float:
    vals = sorted(v for v in values if math.isfinite(v) and v >= 0)
    if not vals:
        return 0.0
    idx = (len(vals) - 1) * pct / 100.0
    lo = math.floor(idx)
    hi = math.ceil(idx)
    if lo == hi:
        return vals[int(idx)]
    return vals[lo] * (hi - idx) + vals[hi] * (idx - lo)


def parse_duration_from_times(start: str, end: str) -> float:
    start_ts = parse_timestamp(start)
    end_ts = parse_timestamp(end)
    if start_ts <= 0 or end_ts <= 0:
        return 0.0
    return max(0.0, end_ts - start_ts)


def parse_timestamp(value: str) -> float:
    if not value:
        return 0.0
    try:
        return datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()
    except ValueError:
        pass
    return 0.0


SUMMARY_FIELDS = [
    "run_id", "experiment", "variable", "value", "method", "seed", "shard_num", "node_num",
    "tx_number", "batch_size", "max_window_ms", "matcher_mode", "network_latency_ms",
    "elapsed_s", "tx_committed", "throughput_tps", "avg_latency_s", "p50_latency_s",
    "p95_latency_s", "matched_value_ratio", "fallback_value_ratio", "matched_intent_ratio",
    "fallback_intent_ratio", "split_allocations", "match_time_ms", "proof_time_ms",
    "beacon_bytes_per_intent", "cross_messages_per_tx", "capital_lock_s", "watermark_skew",
    "batch_count",
]


def write_summary_header(path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="", encoding="utf-8") as fp:
        csv.DictWriter(fp, fieldnames=SUMMARY_FIELDS).writeheader()


def append_summary(path: Path, row: dict[str, object]) -> None:
    with path.open("a", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=SUMMARY_FIELDS, extrasaction="ignore")
        writer.writerow(row)


if __name__ == "__main__":
    raise SystemExit(main())
