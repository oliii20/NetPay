#!/usr/bin/env python3
"""Summarize logical transaction performance for Monoxide and aggregation runs."""

from __future__ import annotations

import argparse
import csv
import math
import re
import statistics
from collections import defaultdict
from datetime import datetime
from pathlib import Path

METHODS = ("monoxide", "aggregation")


def parse_time(value: str) -> int:
    if not value:
        raise ValueError("empty timestamp")
    match = re.fullmatch(r"(.{19})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})", value)
    if match is None:
        raise ValueError(f"invalid RFC3339 timestamp: {value}")
    seconds, fraction, offset = match.groups()
    parsed = datetime.fromisoformat(seconds + ("+00:00" if offset == "Z" else offset))
    return int(parsed.timestamp()) * 1_000_000_000 + int((fraction or "0").ljust(9, "0"))


def percentile(values: list[float], quantile: float) -> float:
    ordered = sorted(values)
    return ordered[max(0, math.ceil(len(ordered) * quantile) - 1)]


def relay_lifecycles(path: Path) -> list[tuple[int, int, bool]]:
    rows = []
    with path.open(newline="") as source:
        for row in csv.DictReader(source):
            rows.append((
                parse_time(row["Tx create time"]),
                parse_time(row["Tx finally commit time"]),
                row["Is cross-shard tx or not"].lower() == "true",
            ))
    return rows


def aggregation_lifecycles(path: Path) -> list[tuple[int, int, bool]]:
    rows = []
    with path.open(newline="") as source:
        for row in csv.DictReader(source):
            rows.append((
                int(row["CreatedAtUnixNano"]),
                int(row["FinalCommittedAtUnixNano"]),
                True,
            ))
    return rows


def txpool_stats(run_dir: Path, start_ns: int, end_ns: int) -> dict[str, float]:
    series = []
    for path in sorted((run_dir / "block_record").glob("shard=*_node=0/txpool_record.csv")):
        values = []
        with path.open(newline="") as source:
            for row in csv.DictReader(source):
                timestamp = parse_time(row["Timestamp"])
                if start_ns <= timestamp <= end_ns:
                    values.append(int(row["TxCount"]))
        if values:
            series.append(values)
    if len(series) != 4:
        raise ValueError(f"expected four leader txpool series in {run_dir}, got {len(series)}")
    total_samples = [sum(items) for items in zip(*series)]
    return {
        "txpool_mean_total": statistics.fmean(total_samples),
        "txpool_p95_total": percentile(total_samples, 0.95),
        "txpool_peak_total": max(total_samples),
    }


def block_stats(run_dir: Path) -> dict[str, int]:
    totals: defaultdict[str, int] = defaultdict(int)
    columns = (
        "TxBodyLen", "Relay1TxCount", "Relay2TxCount",
        "CrossShardPaymentTxCount", "AggregationSettlementTxCount",
    )
    for path in sorted((run_dir / "block_record").glob("shard=*_node=0/block_record.csv")):
        with path.open(newline="") as source:
            for row in csv.DictReader(source):
                for column in columns:
                    totals[column] += int(row[column])
    return {f"block_{key}": value for key, value in totals.items()}


def compression_stats(run_dir: Path) -> dict[str, float]:
    path = run_dir / "results" / "aggregation_batch_metrics.csv"
    receipts = source_abstracts = credits = settlement_txs = 0
    with path.open(newline="") as source:
        for row in csv.DictReader(source):
            receipts += int(row["ReceiptCount"])
            source_abstracts += int(row.get("SourceAbstractCount") or row["ReceiptCount"])
            credits += int(row["CreditCount"])
            settlement_txs += int(row["SettlementTxCount"])
    return {
        "aggregation_receipts": receipts,
        "aggregation_source_abstracts": source_abstracts,
        "aggregation_credits": credits,
        "aggregation_settlement_txs": settlement_txs,
        "receipts_per_source_abstract": receipts / source_abstracts if source_abstracts else math.nan,
        "receipts_per_credit": receipts / credits if credits else math.nan,
        "receipts_per_settlement_tx": receipts / settlement_txs if settlement_txs else math.nan,
    }


def summarize_run(run_dir: Path, method: str, expected_txs: int) -> dict[str, float | int | str]:
    relay = relay_lifecycles(run_dir / "results" / "relay_stats_detail_tx_info.csv")
    logical = relay
    if method == "aggregation":
        logical = relay + aggregation_lifecycles(run_dir / "results" / "aggregation_tx_lifecycle.csv")
    if len(logical) != expected_txs:
        raise ValueError(f"{run_dir}: completed {len(logical)} logical txs, expected {expected_txs}")

    start_ns = min(item[0] for item in logical)
    end_ns = max(item[1] for item in logical)
    latencies_ms = [(end - start) / 1_000_000 for start, end, _ in logical]
    duration_s = (end_ns - start_ns) / 1_000_000_000
    result: dict[str, float | int | str] = {
        "method": method,
        "completed_txs": len(logical),
        "inner_txs": sum(not cross for _, _, cross in logical),
        "cross_txs": sum(cross for _, _, cross in logical),
        "duration_s": duration_s,
        "logical_tps": len(logical) / duration_s,
        "latency_mean_ms": statistics.fmean(latencies_ms),
        "latency_p50_ms": percentile(latencies_ms, 0.50),
        "latency_p95_ms": percentile(latencies_ms, 0.95),
        "latency_p99_ms": percentile(latencies_ms, 0.99),
    }
    result.update(txpool_stats(run_dir, start_ns, end_ns))
    result.update(block_stats(run_dir))
    if method == "aggregation":
        result.update(compression_stats(run_dir))
    return result


def mean_sd(rows: list[dict[str, float | int | str]], metric: str) -> tuple[float, float]:
    values = [float(row[metric]) for row in rows]
    return statistics.fmean(values), statistics.stdev(values) if len(values) > 1 else 0.0


def write_outputs(root: Path, rows: list[dict[str, float | int | str]], expected_txs: int) -> None:
    columns = sorted({key for row in rows for key in row}, key=lambda key: (key != "repeat", key != "method", key))
    with (root / "comparison_runs.csv").open("w", newline="") as target:
        writer = csv.DictWriter(target, fieldnames=columns)
        writer.writeheader()
        writer.writerows(rows)

    grouped = {method: [row for row in rows if row["method"] == method] for method in METHODS}
    metrics = ("logical_tps", "latency_mean_ms", "latency_p95_ms", "txpool_mean_total", "txpool_peak_total", "block_TxBodyLen")
    lines = [
        f"# 4-shard / {expected_txs:,}-transaction comparison", "",
        "All TPS and latency values count logical user transactions; aggregation settlement transactions are not counted as extra user transactions.", "",
        "| Metric | Monoxide mean ± SD | Aggregation mean ± SD | Mean change |", "|---|---:|---:|---:|",
    ]
    for metric in metrics:
        baseline, baseline_sd = mean_sd(grouped["monoxide"], metric)
        aggregation, aggregation_sd = mean_sd(grouped["aggregation"], metric)
        change = (aggregation / baseline - 1) * 100 if baseline else math.nan
        lines.append(
            f"| {metric} | {baseline:.3f} ± {baseline_sd:.3f} | "
            f"{aggregation:.3f} ± {aggregation_sd:.3f} | {change:+.2f}% |"
        )
    lines.extend(["", "## Aggregation compression", ""])
    for metric in (
        "aggregation_receipts", "aggregation_source_abstracts", "aggregation_credits",
        "aggregation_settlement_txs", "receipts_per_source_abstract",
        "receipts_per_credit", "receipts_per_settlement_tx",
    ):
        value = statistics.fmean(float(row[metric]) for row in grouped["aggregation"])
        lines.append(f"- {metric}: {value:.3f}")
    (root / "comparison.md").write_text("\n".join(lines) + "\n")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--input", type=Path, required=True)
    parser.add_argument("--expected-txs", type=int, default=300_000)
    args = parser.parse_args()
    rows = []
    repeat_dirs = sorted(path for path in args.input.glob("repeat-*") if path.is_dir())
    if not repeat_dirs:
        raise SystemExit(f"no repeat-* directories found under {args.input}")
    for repeat_dir in repeat_dirs:
        for method in METHODS:
            row = summarize_run(repeat_dir / method, method, args.expected_txs)
            row["repeat"] = repeat_dir.name
            rows.append(row)
    write_outputs(args.input, rows, args.expected_txs)


if __name__ == "__main__":
    main()
