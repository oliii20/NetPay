#!/usr/bin/env python3
"""Standalone data generator for pre-experiment 1.

This script is intentionally self-contained: it does not import BlockEmulator-X
Go packages. It reproduces the experiment's hash account placement and FIFO
split netting logic directly from the Ethereum transaction CSV.
"""

from __future__ import annotations

import argparse
import csv
from collections import defaultdict, deque
from dataclasses import dataclass
from pathlib import Path
from typing import Optional


DEFAULT_RAW_CSV = "25250000to25499999_BlockTransaction.csv"


@dataclass
class QueuedTx:
    source: int
    order: int
    destination: Optional[int] = None
    value: int = 0


@dataclass
class WorkItem:
    source: int
    destination: int
    value: int
    remaining: int
    matched: int = 0


@dataclass
class CountStats:
    total_ctx: int = 0
    fully_matched: int = 0
    partial_matched: int = 0
    total_value: int = 0
    matched_value: int = 0


def parse_args() -> argparse.Namespace:
    script_dir = Path(__file__).resolve().parent
    root_dir = script_dir.parent
    parser = argparse.ArgumentParser(
        description="Generate experiment-1 FIFO netting CSVs from raw Ethereum transactions.",
    )
    parser.add_argument(
        "--input",
        type=Path,
        default=root_dir / "Raw_data" / DEFAULT_RAW_CSV,
        help="Raw Ethereum transaction CSV path.",
    )
    parser.add_argument(
        "--output-dir",
        type=Path,
        default=root_dir / "画图",
        help="Directory for generated CSV files.",
    )
    parser.add_argument("--shards", type=int, default=8, help="Number of shards.")
    parser.add_argument("--inject-size", type=int, default=10_000, help="Valid transactions per injection.")
    parser.add_argument("--block-size", type=int, default=2_000, help="Transactions per shard per block height.")
    parser.add_argument("--max-valid-txs", type=int, default=300_000, help="Number of valid transactions to collect.")
    return parser.parse_args()


def parse_address(value: str) -> Optional[bytes]:
    value = value.strip()
    if value.lower().startswith("0x"):
        value = value[2:]
    try:
        decoded = bytes.fromhex(value)
    except ValueError:
        return None
    if len(decoded) != 20:
        return None
    return decoded


def shard_of(address: bytes, shard_count: int) -> int:
    return int.from_bytes(address[-4:], "big") % shard_count


def pair_key(source: int, destination: int) -> tuple[int, int]:
    return (source, destination) if source < destination else (destination, source)


def all_pair_stats(shard_count: int) -> dict[tuple[int, int], CountStats]:
    return {
        (lower, higher): CountStats()
        for lower in range(shard_count)
        for higher in range(lower + 1, shard_count)
    }


def all_direction_stats(shard_count: int) -> dict[tuple[int, int], CountStats]:
    return {
        (source, destination): CountStats()
        for source in range(shard_count)
        for destination in range(shard_count)
        if source != destination
    }


def match_fifo_split(txs: list[QueuedTx]) -> list[WorkItem]:
    items: list[WorkItem] = []
    groups: dict[tuple[int, int], dict[str, deque[WorkItem]]] = defaultdict(
        lambda: {"lower_to_higher": deque(), "higher_to_lower": deque()}
    )

    for tx in txs:
        if tx.destination is None or tx.value <= 0:
            continue
        item = WorkItem(
            source=tx.source,
            destination=tx.destination,
            value=tx.value,
            remaining=tx.value,
        )
        items.append(item)

        lower, higher = pair_key(tx.source, tx.destination)
        group = groups[(lower, higher)]
        if tx.source < tx.destination:
            match_item_against_queue(item, group["higher_to_lower"])
            if item.remaining > 0:
                group["lower_to_higher"].append(item)
        else:
            match_item_against_queue(item, group["lower_to_higher"])
            if item.remaining > 0:
                group["higher_to_lower"].append(item)

    return items


def match_item_against_queue(item: WorkItem, counters: deque[WorkItem]) -> None:
    while item.remaining > 0 and counters:
        counter = counters[0]
        amount = min(item.remaining, counter.remaining)
        item.remaining -= amount
        item.matched += amount
        counter.remaining -= amount
        counter.matched += amount
        if counter.remaining == 0:
            counters.popleft()


def produce_height(
    queues: list[list[QueuedTx]],
    block_size: int,
    shard_count: int,
    height: int,
    injection: int,
    raw_rows_seen: int,
) -> tuple[dict[str, int], dict[tuple[int, int], CountStats]]:
    block_txs: list[QueuedTx] = []
    total_txs = 0
    for shard in range(shard_count):
        take = min(block_size, len(queues[shard]))
        selected = queues[shard][:take]
        queues[shard] = queues[shard][take:]
        total_txs += len(selected)
        block_txs.extend(tx for tx in selected if tx.destination is not None)

    block_txs.sort(key=lambda tx: tx.order)
    results = match_fifo_split(block_txs)
    pair_stats = all_pair_stats(shard_count)
    global_stats = CountStats()

    for item in results:
        key = pair_key(item.source, item.destination)
        stat = pair_stats[key]
        stat.total_ctx += 1
        stat.total_value += item.value
        stat.matched_value += item.matched
        global_stats.total_ctx += 1
        global_stats.total_value += item.value
        global_stats.matched_value += item.matched
        if 0 < item.matched < item.value:
            stat.partial_matched += 1
            global_stats.partial_matched += 1
        if item.matched == item.value:
            stat.fully_matched += 1
            global_stats.fully_matched += 1

    row = {
        "block_height": height,
        "injection_round": injection,
        "raw_rows_seen": raw_rows_seen,
        "total_txs_in_blocks": total_txs,
        "total_ctx": global_stats.total_ctx,
        "fully_matched_ctx": global_stats.fully_matched,
        "matching_rate": rate(global_stats.fully_matched, global_stats.total_ctx),
        "total_ctx_value": global_stats.total_value,
        "matched_value": global_stats.matched_value,
        "value_matching_rate": rate(global_stats.matched_value, global_stats.total_value),
    }
    return row, pair_stats


def rate(numerator: int, denominator: int) -> float:
    if denominator == 0:
        return 0.0
    return numerator / denominator


def format_rate(value: float) -> str:
    return f"{value:.6f}"


def enqueue_batch(queues: list[list[QueuedTx]], batch: list[QueuedTx]) -> None:
    for tx in batch:
        queues[tx.source].append(tx)


def write_csv(path: Path, fieldnames: list[str], rows: list[dict[str, object]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="") as file:
        writer = csv.DictWriter(file, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(rows)


def main() -> None:
    args = parse_args()
    if args.shards <= 1:
        raise ValueError("--shards must be greater than 1")

    queues: list[list[QueuedTx]] = [[] for _ in range(args.shards)]
    directions = all_direction_stats(args.shards)
    height_rows: list[dict[str, object]] = []
    pair_height_rows: list[dict[str, object]] = []
    batch: list[QueuedTx] = []

    stats = {
        "raw_rows_processed": 0,
        "valid_txs_queued": 0,
        "ctx_created": 0,
        "invalid_rows": 0,
        "invalid_from": 0,
        "invalid_to": 0,
        "invalid_value": 0,
        "no_recipient": 0,
        "zero_value": 0,
        "inner_shard_positive_value": 0,
        "same_participant": 0,
    }

    injection = 0
    valid_seen = 0
    raw_seen = 0

    with args.input.open(newline="") as file:
        reader = csv.reader(file)
        next(reader)
        for row in reader:
            if valid_seen >= args.max_valid_txs:
                break
            raw_seen += 1
            stats["raw_rows_processed"] += 1
            if len(row) <= 8:
                stats["invalid_rows"] += 1
                continue

            sender = parse_address(row[3])
            if sender is None:
                stats["invalid_from"] += 1
                continue
            source = shard_of(sender, args.shards)

            try:
                value = int(row[8].strip())
            except ValueError:
                stats["invalid_value"] += 1
                continue

            to_field = row[4].strip()
            if not to_field or to_field.lower() == "none":
                stats["no_recipient"] += 1
                continue
            recipient = parse_address(to_field)
            if recipient is None:
                stats["invalid_to"] += 1
                continue
            if sender == recipient:
                stats["same_participant"] += 1
                continue
            if value <= 0:
                stats["zero_value"] += 1
                continue

            destination = shard_of(recipient, args.shards)
            if source == destination:
                stats["inner_shard_positive_value"] += 1
                tx = QueuedTx(source=source, order=valid_seen)
            else:
                stats["ctx_created"] += 1
                direction = directions[(source, destination)]
                direction.total_ctx += 1
                direction.total_value += value
                tx = QueuedTx(source=source, order=valid_seen, destination=destination, value=value)

            batch.append(tx)
            valid_seen += 1
            stats["valid_txs_queued"] += 1

            if len(batch) == args.inject_size:
                injection += 1
                enqueue_batch(queues, batch)
                batch = []
                row_out, pair_stats = produce_height(
                    queues, args.block_size, args.shards, len(height_rows) + 1, injection, raw_seen
                )
                height_rows.append(row_out)
                append_pair_height_rows(pair_height_rows, row_out["block_height"], pair_stats)

    if batch:
        injection += 1
        enqueue_batch(queues, batch)
        row_out, pair_stats = produce_height(
            queues, args.block_size, args.shards, len(height_rows) + 1, injection, raw_seen
        )
        height_rows.append(row_out)
        append_pair_height_rows(pair_height_rows, row_out["block_height"], pair_stats)

    while any(queues):
        row_out, pair_stats = produce_height(
            queues, args.block_size, args.shards, len(height_rows) + 1, injection, raw_seen
        )
        height_rows.append(row_out)
        append_pair_height_rows(pair_height_rows, row_out["block_height"], pair_stats)

    pair_aggregate = aggregate_pair_rows(args.shards, pair_height_rows)
    direction_rows, bidirectional_rows = direction_output_rows(args.shards, directions)
    summary_rows = summary_output_rows(args, stats, height_rows, pair_aggregate)

    out = args.output_dir
    write_csv(
        out / "height_matching_rate.csv",
        [
            "block_height",
            "injection_round",
            "raw_rows_seen",
            "total_txs_in_blocks",
            "total_ctx",
            "fully_matched_ctx",
            "matching_rate",
            "total_ctx_value",
            "matched_value",
            "value_matching_rate",
        ],
        normalize_rate_rows(height_rows, ["matching_rate", "value_matching_rate"]),
    )
    write_csv(
        out / "pair_height_matching_rate.csv",
        [
            "block_height",
            "lower_shard",
            "higher_shard",
            "total_ctx",
            "fully_matched_ctx",
            "matching_rate",
            "partial_matched_ctx",
            "total_ctx_value",
            "matched_value",
            "value_matching_rate",
        ],
        pair_height_rows,
    )
    write_csv(
        out / "pair_aggregate_matching_rate.csv",
        [
            "lower_shard",
            "higher_shard",
            "total_ctx",
            "fully_matched_ctx",
            "matching_rate",
            "partial_matched_ctx",
            "total_ctx_value",
            "matched_value",
            "value_matching_rate",
        ],
        pair_aggregate,
    )
    write_csv(
        out / "directional_pair_ctx_counts.csv",
        ["source_shard", "destination_shard", "direction", "total_ctx", "total_ctx_value"],
        direction_rows,
    )
    write_csv(
        out / "bidirectional_pair_ctx_balance.csv",
        [
            "lower_shard",
            "higher_shard",
            "lower_to_higher_ctx",
            "higher_to_lower_ctx",
            "total_ctx",
            "min_direction_ctx",
            "max_direction_ctx",
            "count_balance_ratio",
            "lower_to_higher_value",
            "higher_to_lower_value",
            "total_value",
            "min_direction_value",
            "max_direction_value",
            "value_balance_ratio",
        ],
        bidirectional_rows,
    )
    write_csv(out / "run_summary.csv", ["metric", "value"], summary_rows)

    print("exp1 fifo data completed")
    print(f"output_dir={out}")
    print(
        f"raw_rows={stats['raw_rows_processed']} "
        f"valid_txs={stats['valid_txs_queued']} "
        f"ctx={stats['ctx_created']} "
        f"heights={len(height_rows)}"
    )


def append_pair_height_rows(
    rows: list[dict[str, object]], block_height: int, pair_stats: dict[tuple[int, int], CountStats]
) -> None:
    for lower, higher in sorted(pair_stats):
        stat = pair_stats[(lower, higher)]
        rows.append(pair_row(block_height, lower, higher, stat))


def pair_row(block_height: int, lower: int, higher: int, stat: CountStats) -> dict[str, object]:
    return {
        "block_height": block_height,
        "lower_shard": lower,
        "higher_shard": higher,
        "total_ctx": stat.total_ctx,
        "fully_matched_ctx": stat.fully_matched,
        "matching_rate": format_rate(rate(stat.fully_matched, stat.total_ctx)),
        "partial_matched_ctx": stat.partial_matched,
        "total_ctx_value": stat.total_value,
        "matched_value": stat.matched_value,
        "value_matching_rate": format_rate(rate(stat.matched_value, stat.total_value)),
    }


def aggregate_pair_rows(shard_count: int, pair_height_rows: list[dict[str, object]]) -> list[dict[str, object]]:
    aggregates = all_pair_stats(shard_count)
    for row in pair_height_rows:
        key = (int(row["lower_shard"]), int(row["higher_shard"]))
        stat = aggregates[key]
        stat.total_ctx += int(row["total_ctx"])
        stat.fully_matched += int(row["fully_matched_ctx"])
        stat.partial_matched += int(row["partial_matched_ctx"])
        stat.total_value += int(row["total_ctx_value"])
        stat.matched_value += int(row["matched_value"])

    rows: list[dict[str, object]] = []
    for (lower, higher), stat in sorted(aggregates.items()):
        row = pair_row(0, lower, higher, stat)
        row.pop("block_height")
        rows.append(row)
    return rows


def direction_output_rows(
    shard_count: int,
    directions: dict[tuple[int, int], CountStats],
) -> tuple[list[dict[str, object]], list[dict[str, object]]]:
    direction_rows: list[dict[str, object]] = []
    bidirectional_rows: list[dict[str, object]] = []
    for source, destination in sorted(directions):
        stat = directions[(source, destination)]
        direction_rows.append(
            {
                "source_shard": source,
                "destination_shard": destination,
                "direction": f"S{source}->S{destination}",
                "total_ctx": stat.total_ctx,
                "total_ctx_value": stat.total_value,
            }
        )

    for lower in range(shard_count):
        for higher in range(lower + 1, shard_count):
            forward = directions[(lower, higher)]
            reverse = directions[(higher, lower)]
            min_count = min(forward.total_ctx, reverse.total_ctx)
            max_count = max(forward.total_ctx, reverse.total_ctx)
            min_value = min(forward.total_value, reverse.total_value)
            max_value = max(forward.total_value, reverse.total_value)
            bidirectional_rows.append(
                {
                    "lower_shard": lower,
                    "higher_shard": higher,
                    "lower_to_higher_ctx": forward.total_ctx,
                    "higher_to_lower_ctx": reverse.total_ctx,
                    "total_ctx": forward.total_ctx + reverse.total_ctx,
                    "min_direction_ctx": min_count,
                    "max_direction_ctx": max_count,
                    "count_balance_ratio": format_rate(rate(min_count, max_count)),
                    "lower_to_higher_value": forward.total_value,
                    "higher_to_lower_value": reverse.total_value,
                    "total_value": forward.total_value + reverse.total_value,
                    "min_direction_value": min_value,
                    "max_direction_value": max_value,
                    "value_balance_ratio": format_rate(rate(min_value, max_value)),
                }
            )
    return direction_rows, bidirectional_rows


def summary_output_rows(
    args: argparse.Namespace,
    stats: dict[str, int],
    height_rows: list[dict[str, object]],
    pair_aggregate_rows: list[dict[str, object]],
) -> list[dict[str, object]]:
    total_ctx = sum(int(row["total_ctx"]) for row in pair_aggregate_rows)
    fully_matched = sum(int(row["fully_matched_ctx"]) for row in pair_aggregate_rows)
    total_value = sum(int(row["total_ctx_value"]) for row in pair_aggregate_rows)
    matched_value = sum(int(row["matched_value"]) for row in pair_aggregate_rows)
    rows = [
        ("input", args.input),
        ("output_dir", args.output_dir),
        ("shards", args.shards),
        ("inject_size", args.inject_size),
        ("block_size", args.block_size),
        ("max_valid_txs", args.max_valid_txs),
        ("matcher_mode", "fifo_split"),
        ("block_heights", len(height_rows)),
        ("total_ctx_in_blocks", total_ctx),
        ("fully_matched_ctx", fully_matched),
        ("global_matching_rate", format_rate(rate(fully_matched, total_ctx))),
        ("total_ctx_value", total_value),
        ("matched_value", matched_value),
        ("value_matching_rate", format_rate(rate(matched_value, total_value))),
    ]
    rows.extend(stats.items())
    return [{"metric": key, "value": value} for key, value in rows]


def normalize_rate_rows(rows: list[dict[str, object]], rate_fields: list[str]) -> list[dict[str, object]]:
    normalized: list[dict[str, object]] = []
    for row in rows:
        copy = row.copy()
        for field in rate_fields:
            copy[field] = format_rate(float(copy[field]))
        normalized.append(copy)
    return normalized


if __name__ == "__main__":
    main()
