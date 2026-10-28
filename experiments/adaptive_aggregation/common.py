"""Shared, serial experiment runtime and original-transaction audit."""

import csv
import fcntl
import hashlib
import importlib.util
import json
import math
from pathlib import Path
import re
import socket
import statistics
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]
BEACON, SUPERVISOR = 2147483646, 2147483647
MODES = ("monoxide", "brokerchain", "clpa", "full", "adaptive")
PROPOSED = ("full", "adaptive")


def read_rows(path):
    with Path(path).open(newline="", encoding="utf-8") as handle:
        yield from csv.DictReader(handle)


def write_json(path, value):
    Path(path).write_text(
        json.dumps(value, indent=2, ensure_ascii=False) + "\n", encoding="utf-8"
    )


def write_csv(path, records):
    records = list(records)
    if not records:
        return
    with Path(path).open("w", newline="", encoding="utf-8") as handle:
        writer = csv.DictWriter(handle, fieldnames=list(records[0]))
        writer.writeheader()
        writer.writerows(records)


def sha256_file(path):
    digest = hashlib.sha256()
    with Path(path).open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def build_binaries(bins):
    bins = Path(bins).resolve()
    bins.mkdir(parents=True, exist_ok=True)
    for binary in ("consensusnode", "beaconnode", "supervisor"):
        subprocess.run(
            ["go", "build", "-o", str(bins / binary), f"./cmd/{binary}"],
            cwd=ROOT,
            check=True,
        )
    return bins


def provenance(dataset, bins):
    return dict(
        dataset_sha256=sha256_file(dataset),
        binary_sha256={
            name: sha256_file(Path(bins) / name)
            for name in ("consensusnode", "beaconnode", "supervisor")
        },
        git_revision=subprocess.check_output(
            ["git", "rev-parse", "HEAD"], cwd=ROOT, text=True
        ).strip(),
        git_dirty=bool(
            subprocess.check_output(
                ["git", "status", "--porcelain"], cwd=ROOT, text=True
            ).strip()
        ),
    )


def network_table(shards, aggregation, port_base):
    table, port = {}, port_base
    for shard in list(range(shards)) + ([BEACON] if aggregation else []) + [SUPERVISOR]:
        table[str(shard)] = {}
        for node in range(1 if shard == SUPERVISOR else 4):
            table[str(shard)][str(node)] = f"127.0.0.1:{port}"
            port += 1
    return table


def check_ports(table):
    sockets = []
    try:
        for nodes in table.values():
            for address in nodes.values():
                sock = socket.socket()
                sockets.append(sock)
                # Match Go's TCP listener: TIME_WAIT from our previous case
                # must not masquerade as a live listener on macOS.
                sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
                sock.bind(("127.0.0.1", int(address.rsplit(":", 1)[1])))
                sock.listen(1)
    finally:
        for sock in sockets:
            sock.close()


def launch_and_wait(output, config, table, bins, timeout, port_base):
    """Only children started here are terminated; no global pkill or result removal."""
    output, bins = Path(output), Path(bins)
    logs = output / "process_logs"
    logs.mkdir()
    processes, handles = [], []
    lock_path = (
        Path("/private/tmp" if Path("/private/tmp").exists() else "/tmp")
        / f"block-emulator-{port_base}.lock"
    )
    with lock_path.open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        check_ports(table)

        def launch(binary, shard, node):
            handle = (logs / f"{binary}-{shard}-{node}.log").open("w")
            handles.append(handle)
            process = subprocess.Popen(
                [
                    str(bins / binary),
                    f"-config={output/'config.yaml'}",
                    f"-ip_table={output/'ip_table.json'}",
                    f"-shard_id={shard}",
                    f"-node_id={node}",
                ],
                cwd=ROOT,
                stdout=handle,
                stderr=subprocess.STDOUT,
            )
            processes.append(process)
            return process

        try:
            for shard in range(config["system"]["shard_num"]):
                for node in range(4):
                    launch("consensusnode", shard, node)
            if config["aggregation"]["enabled"]:
                for node in range(4):
                    launch("beaconnode", BEACON, node)
            supervisor = launch("supervisor", SUPERVISOR, 0)
            deadline = time.monotonic() + timeout
            while supervisor.poll() is None:
                if time.monotonic() > deadline:
                    raise TimeoutError(f"experiment timeout; inspect {logs}")
                for process in processes[:-1]:
                    if process.poll() not in (None, 0):
                        raise RuntimeError(
                            f"node exited with {process.returncode}; inspect {logs}"
                        )
                time.sleep(0.5)
            if supervisor.returncode:
                raise RuntimeError(
                    f"supervisor exited with {supervisor.returncode}; inspect {logs}"
                )
            # Let recipients consume Supervisor's stop message before cleanup.
            for process in processes[:-1]:
                try:
                    process.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    break
        finally:
            for process in processes:
                if process.poll() is None:
                    process.terminate()
            for process in processes:
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
            for handle in handles:
                handle.close()


def time_parser():
    spec = importlib.util.spec_from_file_location(
        "original_analysis", ROOT / "experiments/aggregation_4shard_300k/analyze.py"
    )
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module.parse_time


def percentile(values, q):
    ordered = sorted(values)
    return ordered[max(0, math.ceil(len(ordered) * q) - 1)]


def validate_and_summarize(output, mode, expected, pressure=False):
    output = Path(output)
    parse_time = time_parser()
    seen, latencies, ordinary, aggregated = set(), [], 0, 0
    start, end, last_created = None, None, None

    def add(key, created, committed):
        nonlocal start, end, last_created
        if not re.fullmatch(r"[0-9a-f]{64}", key) or key in seen or committed < created:
            raise ValueError(f"invalid/duplicate original lifecycle: {key}")
        seen.add(key)
        latencies.append((committed - created) / 1e6)
        start = created if start is None else min(start, created)
        end = committed if end is None else max(end, committed)
        last_created = created if last_created is None else max(last_created, created)

    detail = (
        "broker_stats_detail_tx_info.csv"
        if mode == "brokerchain"
        else "relay_stats_detail_tx_info.csv"
    )
    relay_cross = 0
    for row in read_rows(output / "results" / detail):
        add(
            row["OriginalHash"],
            parse_time(row["Tx create time"]),
            parse_time(row["Tx finally commit time"]),
        )
        ordinary += 1
        if row.get("Is cross-shard tx or not") == "true":
            relay_cross += 1
    if mode in PROPOSED:
        for row in read_rows(output / "results/aggregation_tx_lifecycle.csv"):
            created, committed = int(row["CreatedAtUnixNano"]), int(
                row["FinalCommittedAtUnixNano"]
            )
            if int(row["LatencyNs"]) != committed - created:
                raise ValueError("inconsistent aggregation lifecycle")
            add(row["PaymentID"], created, committed)
            aggregated += 1
    if len(seen) != expected:
        raise ValueError(f"expected {expected} unique originals; completed {len(seen)}")
    counters = {
        key: 0
        for key in (
            "Relay1TxCount",
            "Relay2TxCount",
            "CrossShardPaymentTxCount",
            "AggregationSettlementTxCount",
            "Broker2TxCount",
            "ZeroValueRelay1TxCount",
            "ZeroValueRelay2TxCount",
        )
    }
    for path in (output / "block_record").glob("shard=*_node=0/block_record.csv"):
        for row in read_rows(path):
            for key in counters:
                counters[key] += int(row.get(key, 0))
    if counters["CrossShardPaymentTxCount"]:
        raise ValueError(
            "new-code experiment accidentally used legacy CrossShardPayment transactions"
        )
    policies = (
        list(read_rows(output / "results/aggregation_routing_policy.csv"))
        if mode in PROPOSED
        else []
    )
    if (
        mode in PROPOSED
        and counters["Relay1TxCount"] != counters["Relay2TxCount"] + aggregated
    ):
        raise ValueError("source Relay1 partition does not conserve originals")
    eligible_relay2 = counters["Relay2TxCount"] - counters["ZeroValueRelay2TxCount"]
    if mode in PROPOSED and (
        counters["ZeroValueRelay1TxCount"] != counters["ZeroValueRelay2TxCount"]
        or eligible_relay2 < 0
    ):
        raise ValueError(
            "zero-value Relay1/Relay2 partition does not conserve originals"
        )
    if mode == "full" and (
        policies or eligible_relay2 or relay_cross != counters["ZeroValueRelay2TxCount"]
    ):
        raise ValueError("always-full control changed routes")
    if pressure and (not policies or not aggregated or eligible_relay2 <= 0):
        raise ValueError("pressure run did not exercise a switch and both routes")
    warnings = []
    for folder in ("logs", "process_logs"):
        for path in (output / folder).rglob("*.log"):
            with path.open(errors="replace") as handle:
                for line in handle:
                    if re.search(r"level=(ERROR|WARN)", line):
                        warnings.append(f"{path.relative_to(output)}: {line.strip()}")
    if warnings:
        raise ValueError("experiment warnings/errors:\n" + "\n".join(warnings[:10]))
    duration = (end - start) / 1e9
    summary = dict(
        mode=mode,
        completed_txs=len(seen),
        duplicate_originals=0,
        duration_s=duration,
        logical_tps=len(seen) / duration,
        latency_mean_ms=statistics.fmean(latencies),
        latency_p95_ms=percentile(latencies, 0.95),
        latency_p99_ms=percentile(latencies, 0.99),
        start_ns=start,
        end_ns=end,
        last_created_ns=last_created,
        injection_span_s=(last_created - start) / 1e9,
        aggregation_originals=aggregated,
        relay_completed_ctx=relay_cross,
        relay_and_inner_originals=ordinary,
        policy_versions=len(policies),
        warning_error_count=0,
        eligible_relay2_count=eligible_relay2,
    )
    summary.update(counters)
    if mode in PROPOSED:
        batches = list(read_rows(output / "results/aggregation_batch_metrics.csv"))
        waits = [int(r["ServiceQueueWaitNs"]) / 1e6 for r in batches]
        receiver = counters["Relay2TxCount"] + sum(
            int(r["ReceiverUpdateCount"])
            for r in read_rows(output / "results/aggregation_execution_metrics.csv")
            if r["Phase"] == "settlement"
        )
        summary.update(
            batches=len(batches),
            receiver_updates=receiver,
            destination_entries=counters["Relay2TxCount"]
            + counters["AggregationSettlementTxCount"],
            service_queue_wait_mean_ms=statistics.fmean(waits) if waits else None,
            service_queue_wait_p95_ms=percentile(waits, 0.95) if waits else None,
            service_queue_wait_max_ms=max(waits) if waits else None,
        )
    else:
        summary.update(
            batches=0,
            receiver_updates=None,
            destination_entries=counters["Relay2TxCount"] + counters["Broker2TxCount"],
            service_queue_wait_mean_ms=None,
            service_queue_wait_p95_ms=None,
            service_queue_wait_max_ms=None,
        )
    report = dict(
        expected=expected,
        completed_originals=len(seen),
        duplicate_originals=0,
        aggregation_originals=aggregated,
        relay_and_inner_originals=ordinary,
        policy_versions=len(policies),
        block_counters=counters,
        warning_error_count=0,
    )
    write_json(output / "validation.json", report)
    write_json(output / "summary.json", summary)
    return summary
