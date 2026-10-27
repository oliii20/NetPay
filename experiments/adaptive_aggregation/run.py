#!/usr/bin/env python3
"""Run and audit adaptive post-commit routing (24 shards by default)."""

import argparse
import csv
import json
from pathlib import Path
import re
import socket
import subprocess
import time

import yaml


ROOT = Path(__file__).resolve().parents[2]
BEACON, SUPERVISOR = 2147483646, 2147483647


def rows(path):
    with path.open(newline="") as handle:
        return list(csv.DictReader(handle))


def audit(output, expected, pressure):
    ordinary = rows(output / "results/relay_stats_detail_tx_info.csv")
    aggregated = rows(output / "results/aggregation_tx_lifecycle.csv")
    ids = [r["OriginalHash"] for r in ordinary] + [r["PaymentID"] for r in aggregated]
    if len(ids) != expected or len(set(ids)) != expected:
        raise ValueError(f"not exactly {expected} unique completed originals: rows={len(ids)}, unique={len(set(ids))}")
    policies = rows(output / "results/aggregation_routing_policy.csv")
    if pressure and (not policies or not aggregated or not any(r["Is cross-shard tx or not"] == "true" for r in ordinary)):
        raise ValueError("pressure run did not exercise both routes and a policy change")
    totals = {key: 0 for key in ("Relay1TxCount", "Relay2TxCount", "CrossShardPaymentTxCount", "AggregationSettlementTxCount")}
    for path in (output / "block_record").glob("shard=*_node=0/block_record.csv"):
        for row in rows(path):
            for key in totals:
                totals[key] += int(row[key])
    if totals["CrossShardPaymentTxCount"] != 0:
        raise ValueError("adaptive injection changed the original transaction type")
    if totals["Relay1TxCount"] != totals["Relay2TxCount"] + len(aggregated):
        raise ValueError("source Relay1 partition does not conserve original transactions")
    errors = []
    for folder in ("logs", "process_logs"):
        for path in (output / folder).rglob("*.log"):
            for line in path.read_text(errors="replace").splitlines():
                if re.search(r"level=(ERROR|WARN)", line):
                    errors.append(f"{path.relative_to(output)}: {line}")
    if errors:
        raise ValueError("experiment warnings/errors:\n" + "\n".join(errors[:10]))
    report = dict(expected=expected, completed_originals=len(ids), duplicate_originals=len(ids)-len(set(ids)),
                  aggregation_originals=len(aggregated), relay_and_inner_originals=len(ordinary),
                  policy_versions=len(policies), block_counters=totals, warning_error_count=0)
    (output / "validation.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, indent=2), flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--shards", type=int, default=24)
    parser.add_argument("--tx-number", type=int, default=300000)
    parser.add_argument("--per-shard-rate", type=int, default=600)
    parser.add_argument("--pressure-test", action="store_true", help="small-batch integration test; not a performance benchmark")
    parser.add_argument("--timeout", type=int, default=600)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    if args.shards < 2 or args.tx_number <= 0 or args.per_shard_rate <= 0:
        parser.error("shards >= 2 and positive workload/rate required")
    output = args.output or ROOT / f".exp/adaptive-routing-{time.strftime('%Y%m%d-%H%M%S')}"
    output = output.resolve()
    output.mkdir(parents=True, exist_ok=False)
    config = yaml.safe_load((ROOT / "config.aggregation-e2e.yaml").read_text())
    config["system"].update(shard_num=args.shards, node_num=4, limit=100 if args.pressure_test else 2000)
    config["system"]["log"]["log_dir"] = str(output / "logs")
    node = config["consensus_node"]
    node["block_interval"] = 500 if args.pressure_test else 2000
    node["shard_block_intervals_ms"] = {BEACON: 500 if args.pressure_test else 1000}
    node["block_record_dir"] = str(output / "block_record")
    node["blockchain"]["storage"]["bolt"]["file_path_dir"] = str(output / "boltdb")
    node["blockchain"]["storage"]["eth_storage"]["level_file_path_dir"] = str(output / "trie_db")
    node["blockchain"]["vm"]["vm_state_dir"] = str(output / "vm_state")
    config["aggregation"].update(source_aggregate_abstracts=True, credit_chunk_size=512,
                                 batch_size=20 if args.pressure_test else 0,
                                 max_window_duration_ms=500 if args.pressure_test else 2000,
                                 beacon_store_path=str(output / "beacon/beacon.db"),
                                 adaptive_routing=dict(enabled=True, freeze_high_ms=100 if args.pressure_test else 2000,
                                                       freeze_low_ms=25 if args.pressure_test else 500,
                                                       observation_window_ms=10000, cooldown_ms=2000 if args.pressure_test else 10000,
                                                       reduction_percent=25, min_destination_percent=25))
    config["supervisor"].update(tx_number=args.tx_number, tx_injection_speed=args.shards*args.per_shard_rate,
                                 result_output_dir=str(output / "results"))
    config["supervisor"]["tx_source"].update(tx_source_type="csv_source", tx_source_file="./data/25250000to25499999_BlockTransaction.csv")
    config_path, table_path = output / "config.yaml", output / "ip_table.json"
    config_path.write_text(yaml.safe_dump(config, sort_keys=False))
    # Only validate/reserve addresses for this experiment. Existing process groups
    # and prior results are never killed or overwritten.
    sockets, table, port = [], {}, 42000
    try:
        for shard in list(range(args.shards)) + [BEACON, SUPERVISOR]:
            table[str(shard)] = {}
            for node_id in range(1 if shard == SUPERVISOR else 4):
                sock = socket.socket()
                sock.bind(("127.0.0.1", port))
                sockets.append(sock)
                table[str(shard)][str(node_id)] = f"127.0.0.1:{port}"
                port += 1
    finally:
        for sock in sockets:
            sock.close()
    table_path.write_text(json.dumps(table, indent=2) + "\n")
    bins, logs = output / "bin", output / "process_logs"
    bins.mkdir(); logs.mkdir()
    for command in ("consensusnode", "beaconnode", "supervisor"):
        subprocess.run(["go", "build", "-o", str(bins / command), f"./cmd/{command}"], cwd=ROOT, check=True)
    processes, handles = [], []
    def launch(binary, shard, node_id):
        handle = (logs / f"{binary}-{shard}-{node_id}.log").open("w")
        handles.append(handle)
        process = subprocess.Popen([str(bins / binary), f"-config={config_path}", f"-ip_table={table_path}",
                                    f"-shard_id={shard}", f"-node_id={node_id}"], cwd=ROOT, stdout=handle, stderr=subprocess.STDOUT)
        processes.append(process)
        return process
    print(f"Starting {args.shards}x4 + 4 Beacon replicas; output: {output}", flush=True)
    try:
        for shard in range(args.shards):
            for node_id in range(4):
                launch("consensusnode", shard, node_id)
        for node_id in range(4):
            launch("beaconnode", BEACON, node_id)
        supervisor = launch("supervisor", SUPERVISOR, 0)
        deadline = time.monotonic() + args.timeout
        while supervisor.poll() is None:
            if time.monotonic() > deadline:
                raise TimeoutError(f"experiment did not finish; inspect {logs}")
            for process in processes[:-1]:
                if process.poll() not in (None, 0):
                    raise RuntimeError(f"node exited with {process.returncode}; inspect {logs}")
            time.sleep(.5)
        if supervisor.returncode:
            raise RuntimeError(f"supervisor failed; inspect {logs}")
        audit(output, args.tx_number, args.pressure_test)
    finally:
        for process in processes:
            if process.poll() is None:
                process.terminate()
        for process in processes:
            try:
                process.wait(timeout=10)
            except subprocess.TimeoutExpired:
                process.kill(); process.wait()
        for handle in handles:
            handle.close()


if __name__ == "__main__":
    main()
