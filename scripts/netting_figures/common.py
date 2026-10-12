#!/usr/bin/env python3
"""Shared helpers for standalone netting figure scripts."""

from __future__ import annotations

import argparse
import csv
import sys
from datetime import datetime
from pathlib import Path
from typing import Iterable


FIGURE_DIR = Path(__file__).resolve().parent
EXPERIMENT_DIR = FIGURE_DIR.parent / "netting_experiments"
sys.path.insert(0, str(EXPERIMENT_DIR))

from plot_experiments import f  # noqa: E402
from plot_hotspot_hybrid_repeated import (  # noqa: E402
    plot_stage_latency,
    plot_tx_latency_violin,
    resolve_batch_metrics_path,
    total_matched_netting_rate_values,
    total_netting_rate_values,
    write_tx_latency_stats,
)
from plot_matching_rate_violin import (  # noqa: E402
    collect_matching_rates,
    plot_violin,
    write_samples_csv,
)
from run_exp1_repeated_boxplots import plot_metric_boxplot  # noqa: E402


DEFAULT_SUMMARY = Path("exp/hotspot-hybrid-shard8-repeat10-broker/summary.csv")
DEFAULT_OUT = Path("figures/netting")

METHOD_LABELS = [
    ("static_relay", "Relay"),
    ("clpa_broker", "BrokerChain"),
    ("static_broker", "BrokerChain"),
    ("netting_static_relay", "Netting"),
]


def positive_int(raw: str) -> int:
    value = int(raw)
    if value <= 0:
        raise argparse.ArgumentTypeError("must be a positive integer")
    return value


def read_rows(path: Path) -> list[dict[str, str]]:
    with path.open(newline="", encoding="utf-8") as fp:
        return list(csv.DictReader(fp))


def resolve_timestamp(raw: str) -> str:
    if raw.lower() == "none":
        return ""
    return raw or datetime.now().strftime("%Y%m%d-%H%M%S")


def timestamped_stem(path: Path, timestamp: str) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    if not timestamp:
        return path
    return path.with_name(f"{path.name}_{timestamp}")


def timestamped_path(path: Path, timestamp: str) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    if not timestamp:
        return path
    return path.with_name(f"{path.stem}_{timestamp}{path.suffix}")


def metric_groups(rows: list[dict[str, str]], metric: str) -> list[tuple[str, list[float]]]:
    grouped: dict[str, list[float]] = {}
    for method, label in METHOD_LABELS:
        values = grouped.setdefault(label, [])
        values.extend(f(row, metric) for row in rows if row.get("method") == method and f(row, metric) > 0)

    ordered: list[tuple[str, list[float]]] = []
    seen: set[str] = set()
    for _method, label in METHOD_LABELS:
        if label in seen or not grouped.get(label):
            continue
        ordered.append((label, grouped[label]))
        seen.add(label)
    return ordered


def select_rows_by_repeat(rows: list[dict[str, str]], repeat: int | None) -> list[dict[str, str]]:
    if repeat is None:
        return rows
    selected = [row for row in rows if int(f(row, "repeat")) == repeat]
    if not selected:
        raise SystemExit(f"no rows found for repeat_{repeat:02d}")
    return selected


def select_netting_run_ids(rows: Iterable[dict[str, str]], repeat: int | None, run_ids: list[str]) -> list[str]:
    wanted = set(run_ids)
    selected: list[str] = []
    for row in rows:
        if row.get("method") != "netting_static_relay" or f(row, "batch_count") <= 0:
            continue
        if repeat is not None and int(f(row, "repeat")) != repeat:
            continue
        run_id = row.get("run_id", "")
        if wanted and run_id not in wanted:
            continue
        selected.append(run_id)
    if wanted:
        missing = wanted - set(selected)
        if missing:
            raise SystemExit(f"run_id not found or has no batches: {sorted(missing)}")
    if not selected:
        suffix = f" repeat_{repeat:02d}" if repeat is not None else ""
        raise SystemExit(f"no netting runs found{suffix}")
    return selected


def parse_run_ids(raw: str) -> list[str]:
    return [part.strip() for part in raw.split(",") if part.strip()]
