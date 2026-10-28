#!/usr/bin/env python3
"""Run one isolated, audited experiment using normal source transactions."""
import argparse
import json
from pathlib import Path
import time
import yaml
from common import (
    ROOT,
    BEACON,
    MODES,
    PROPOSED,
    build_binaries,
    network_table,
    launch_and_wait,
    provenance,
    sha256_file,
    validate_and_summarize,
    write_json,
)


def parser():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--mode", choices=MODES, default="adaptive")
    p.add_argument("--shards", type=int, default=24)
    p.add_argument("--tx-number", type=int, default=300000)
    p.add_argument("--per-shard-rate", type=int, default=600)
    p.add_argument("--freeze-high-ms", type=int, default=500)
    p.add_argument("--freeze-low-ms", type=int, default=100)
    p.add_argument("--cooldown-ms", type=int, default=10000)
    p.add_argument("--recovery-window-ms", type=int, default=2000)
    p.add_argument("--recovery-windows", type=int, default=3)
    p.add_argument("--recovery-change-percent", type=int, default=75)
    p.add_argument("--min-destination-percent", type=int, default=25)
    p.add_argument("--beacon-work-us", type=int, default=0)
    p.add_argument("--block-interval-ms", type=int, default=2000)
    p.add_argument("--beacon-interval-ms", type=int, default=1000)
    p.add_argument("--window-ms", type=int, default=2000)
    p.add_argument("--block-limit", type=int, default=2000)
    p.add_argument("--clpa-interval", type=int, default=20)
    p.add_argument("--timeout", type=int, default=1800)
    p.add_argument("--port-base", type=int, default=42000)
    p.add_argument(
        "--dataset",
        type=Path,
        default=ROOT / "data/25250000to25499999_BlockTransaction.csv",
    )
    p.add_argument("--broker-file", type=Path, default=ROOT / "pkg/broker/broker")
    p.add_argument("--bins", type=Path)
    p.add_argument("--output", type=Path)
    p.add_argument("--dry-run", action="store_true")
    p.add_argument(
        "--pressure-test",
        action="store_true",
        help="compatibility smoke preset; not a benchmark",
    )
    return p


def make_config(a, output):
    proposed = a.mode in PROPOSED
    c = yaml.safe_load((ROOT / "config.aggregation-e2e.yaml").read_text())
    c["system"].update(
        shard_num=a.shards,
        node_num=4,
        limit=a.block_limit,
        consensus_type={"brokerchain": "static_broker", "clpa": "clpa_relay"}.get(
            a.mode, "static_relay"
        ),
    )
    c["system"]["log"]["log_dir"] = str(output / "logs")
    n = c["consensus_node"]
    n.update(
        block_interval=a.block_interval_ms,
        block_record_dir=str(output / "block_record"),
        shard_block_intervals_ms={BEACON: a.beacon_interval_ms} if proposed else {},
    )
    n["blockchain"]["storage"]["bolt"]["file_path_dir"] = str(output / "boltdb")
    n["blockchain"]["storage"]["eth_storage"]["level_file_path_dir"] = str(
        output / "trie_db"
    )
    n["blockchain"]["vm"]["vm_state_dir"] = str(output / "vm_state")
    c["aggregation"].update(
        enabled=proposed,
        metrics_enabled=proposed,
        source_aggregate_abstracts=True,
        credit_chunk_size=512,
        batch_size=0,
        max_window_duration_ms=a.window_ms,
        beacon_store_path=str(output / "beacon/beacon.db"),
        experiment_work_us_per_payment=a.beacon_work_us if proposed else 0,
        adaptive_routing=dict(
            enabled=proposed,
            freeze_high_ms=a.freeze_high_ms,
            freeze_low_ms=a.freeze_low_ms,
            observation_window_ms=10000,
            cooldown_ms=a.cooldown_ms,
            reduction_percent=25,
            min_destination_percent=100
            if a.mode == "full"
            else a.min_destination_percent,
            recovery_window_ms=getattr(a, "recovery_window_ms", 2000),
            recovery_windows=getattr(a, "recovery_windows", 3),
            recovery_change_percent=getattr(a, "recovery_change_percent", 75),
        ),
    )
    c["supervisor"].update(
        tx_number=a.tx_number,
        tx_injection_speed=a.shards * a.per_shard_rate,
        result_output_dir=str(output / "results"),
        epoch_duration=a.clpa_interval,
    )
    c["supervisor"]["tx_source"].update(
        tx_source_type="csv_source",
        tx_source_file=str(a.dataset.resolve()),
        exclude_contract_txs=True,
        positive_value_only=True,
    )
    c["supervisor"]["broker_module"]["broker_file_path"] = str(a.broker_file.resolve())
    return c


def execute(a, shared=None):
    if (
        a.shards < 2
        or min(
            a.tx_number,
            a.per_shard_rate,
            a.block_limit,
            a.window_ms,
            a.cooldown_ms,
            a.block_interval_ms,
            a.beacon_interval_ms,
            a.clpa_interval,
            a.timeout,
            getattr(a, "recovery_window_ms", 2000),
            getattr(a, "recovery_windows", 3),
        )
        <= 0
    ):
        raise ValueError(
            "positive workload, rate, limits and intervals required; shards >= 2"
        )
    if not 0 < a.freeze_low_ms < a.freeze_high_ms or a.beacon_work_us < 0:
        raise ValueError(
            "require 0 < freeze-low-ms < freeze-high-ms and nonnegative beacon work"
        )
    if (
        not 1 <= a.min_destination_percent <= 100
        or not 1024 <= a.port_base <= 65000 - a.shards * 4
    ):
        raise ValueError("invalid minimum destination percentage or port range")
    if (
        not 1 <= getattr(a, "recovery_change_percent", 75) <= 100
        or getattr(a, "recovery_windows", 3) > 1000
    ):
        raise ValueError("invalid recovery percentage or window count")
    if a.pressure_test:
        a.block_limit = 100
        a.block_interval_ms = a.beacon_interval_ms = a.window_ms = 500
        a.cooldown_ms = 2000
        a.beacon_work_us = a.beacon_work_us or 8000
    output = (
        a.output or ROOT / f"exp/{a.mode}-{a.shards}-{time.strftime('%Y%m%d-%H%M%S')}"
    ).resolve()
    output.mkdir(parents=True, exist_ok=False)
    config = make_config(a, output)
    table = network_table(a.shards, a.mode in PROPOSED, a.port_base)
    (output / "config.yaml").write_text(yaml.safe_dump(config, sort_keys=False))
    write_json(output / "ip_table.json", table)
    if a.dry_run:
        write_json(
            output / "manifest.json",
            dict(
                settings={
                    k: str(v) if isinstance(v, Path) else v for k, v in vars(a).items()
                },
                dry_run=True,
            ),
        )
        return None
    bins = a.bins.resolve() if a.bins else build_binaries(output / "bin")
    write_json(
        output / "manifest.json",
        dict(
            shared or provenance(a.dataset, bins),
            config_sha256=sha256_file(output / "config.yaml"),
            settings={
                k: str(v) if isinstance(v, Path) else v for k, v in vars(a).items()
            },
        ),
    )
    print(f"Starting {a.mode} {a.shards}x4; output: {output}", flush=True)
    try:
        launch_and_wait(output, config, table, bins, a.timeout, a.port_base)
        result = validate_and_summarize(
            output, a.mode, a.tx_number, a.pressure_test and a.mode == "adaptive"
        )
        print(json.dumps(result, indent=2), flush=True)
        return result
    except Exception as error:
        write_json(output / "failure.json", dict(error=str(error)))
        raise


if __name__ == "__main__":
    execute(parser().parse_args())
