#!/usr/bin/env python3
"""Archive the current broker list and generate a new one from a CSV dataset."""

from __future__ import annotations

import argparse
import csv
import random
from datetime import datetime
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_DATASET = REPO_ROOT / "data" / "25250000to25499999_BlockTransaction.csv"
DEFAULT_BROKER_FILE = REPO_ROOT / "pkg" / "broker" / "broker"
DEFAULT_BROKER_COUNT = 100
DEFAULT_MAX_TRANSACTIONS = 300_000


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dataset", type=Path, default=DEFAULT_DATASET)
    parser.add_argument("--broker-file", type=Path, default=DEFAULT_BROKER_FILE)
    parser.add_argument("--count", type=positive_int, default=DEFAULT_BROKER_COUNT)
    parser.add_argument(
        "--max-transactions",
        type=positive_int,
        default=DEFAULT_MAX_TRANSACTIONS,
        help="sample from only the first N transaction rows in the dataset",
    )
    parser.add_argument("--seed", type=int, default=1)
    args = parser.parse_args()

    dataset = args.dataset.expanduser().resolve()
    broker_file = args.broker_file.expanduser().resolve()

    brokers = sample_unique_from_addresses(dataset, args.count, args.seed, args.max_transactions)
    archive = archive_broker_file(broker_file)
    write_broker_file(broker_file, brokers)

    print(f"archived: {archive}")
    print(f"generated: {broker_file}")
    print(f"brokers: {len(brokers)}")

    return 0


def positive_int(raw: str) -> int:
    value = int(raw)
    if value <= 0:
        raise argparse.ArgumentTypeError("must be a positive integer")
    return value


def sample_unique_from_addresses(dataset: Path, count: int, seed: int, max_transactions: int) -> list[str]:
    if not dataset.exists():
        raise SystemExit(f"dataset not found: {dataset}")

    rng = random.Random(seed)
    reservoir: list[str] = []
    selected = set()
    seen = set()
    unique_count = 0

    with dataset.open(newline="", encoding="utf-8") as fp:
        reader = csv.reader(fp)
        scanned_transactions = 0
        for row in reader:
            if len(row) <= 3:
                continue
            if is_header(row):
                continue

            scanned_transactions += 1
            if scanned_transactions > max_transactions:
                break

            addr = normalize_address(row[3])
            if addr == "" or addr in seen:
                continue

            seen.add(addr)
            unique_count += 1

            if len(reservoir) < count:
                reservoir.append(addr)
                selected.add(addr)
                continue

            idx = rng.randrange(unique_count)
            if idx < count:
                selected.remove(reservoir[idx])
                reservoir[idx] = addr
                selected.add(addr)

    if len(reservoir) < count:
        raise SystemExit(f"dataset has only {len(reservoir)} unique from addresses; need {count}")

    return reservoir


def is_header(row: list[str]) -> bool:
    return len(row) > 3 and row[3].strip().lower() in {"from", "sender"}


def normalize_address(raw: str) -> str:
    value = raw.strip().lower()
    if value.startswith("0x"):
        value = value[2:]
    if len(value) != 40:
        return ""
    try:
        int(value, 16)
    except ValueError:
        return ""
    return value


def archive_broker_file(broker_file: Path) -> Path:
    if not broker_file.exists():
        raise SystemExit(f"broker file not found: {broker_file}")

    timestamp = datetime.now().strftime("%Y%m%d-%H%M%S")
    archive = broker_file.with_name(f"{broker_file.name}.archive-{timestamp}")
    broker_file.rename(archive)

    return archive


def write_broker_file(broker_file: Path, brokers: list[str]) -> None:
    broker_file.parent.mkdir(parents=True, exist_ok=True)
    broker_file.write_text("\n".join(brokers) + "\n", encoding="utf-8")


if __name__ == "__main__":
    raise SystemExit(main())
