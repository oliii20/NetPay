#!/usr/bin/env python3
"""Generate hotspot cross-shard workloads by re-encoding hot account addresses.

The source CSV is read-only. The script scans it in order until it collects the
requested number of effective positive-value transfers. Output CSVs preserve
transaction order and values, normalize rows into plain transfers, and map
selected hot accounts one-to-one to synthetic addresses whose last four bytes
make DefaultAccountLoc(addr, shard_num) return the assigned shard.
"""

from __future__ import annotations

import argparse
import csv
import hashlib
import json
import random
from collections import Counter, defaultdict
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_DATASET = REPO_ROOT / "data" / "25250000to25499999_BlockTransaction.csv"
DEFAULT_OUT_DIR = REPO_ROOT / "data" / "hotspot_workloads"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--dataset", type=Path, default=DEFAULT_DATASET)
    parser.add_argument("--out-dir", type=Path, default=DEFAULT_OUT_DIR)
    parser.add_argument("--shard-nums", default="8,16,32,64")
    parser.add_argument("--hot-shard-ratio", type=ratio, default=0.5)
    parser.add_argument("--min-hot-shards", type=positive_int, default=1)
    parser.add_argument("--hot-account-ratio", type=ratio, default=0.5)
    parser.add_argument(
        "--max-rows",
        type=positive_int,
        default=300_000,
        help="collect the first N effective positive-value transfer rows",
    )
    parser.add_argument("--seed", type=int, default=1)
    parser.add_argument("--write-mapping", action="store_true")
    args = parser.parse_args()

    dataset = args.dataset.expanduser().resolve()
    out_dir = args.out_dir.expanduser().resolve()
    shard_nums = parse_ints(args.shard_nums)
    out_dir.mkdir(parents=True, exist_ok=True)

    header, rows, scanned_rows = load_effective_rows(dataset, args.max_rows)
    accounts, directed = build_graph(rows)
    bidirectional_edges, hot_score = bidirectional_strength(directed)
    hot_accounts = select_hot_accounts(accounts, hot_score, args.hot_account_ratio)

    effective_pool_path = out_dir / f"effective_25250000to25499999_rows{len(rows)}.csv"
    write_workload(effective_pool_path, header, rows, {})
    print(f"wrote {effective_pool_path}")

    for shard_num in shard_nums:
        hot_shards = list(range(hot_shard_count(shard_num, args.hot_shard_ratio, args.min_hot_shards)))
        assignment = balanced_max_k_cut(
            hot_accounts=hot_accounts,
            hot_shards=hot_shards,
            hot_score=hot_score,
            edges=bidirectional_edges,
            seed=args.seed + shard_num,
        )
        address_map = build_address_map(assignment, shard_num, args.seed)
        prefix = (
            f"hotspot_25250000to25499999"
            f"_rows{len(rows)}"
            f"_shard{shard_num}"
            f"_hotshards{len(hot_shards)}"
            f"_hotacct{ratio_tag(args.hot_account_ratio)}"
        )
        csv_path = out_dir / f"{prefix}.csv"
        mapping_path = out_dir / f"{prefix}_account_mapping.csv"
        stats_path = out_dir / f"{prefix}_stats.json"

        write_workload(csv_path, header, rows, address_map)
        if args.write_mapping:
            write_mapping(mapping_path, assignment, address_map, shard_num)
        write_stats(
            stats_path,
            dataset,
            scanned_rows,
            len(rows),
            accounts,
            hot_accounts,
            shard_num,
            hot_shards,
            assignment,
            rows,
            address_map,
        )
        print(f"wrote {csv_path}")
        if args.write_mapping:
            print(f"wrote {mapping_path}")
        print(f"wrote {stats_path}")

    return 0


def load_effective_rows(path: Path, max_rows: int | None) -> tuple[list[str], list[list[str]], int]:
    if not path.exists():
        raise SystemExit(f"dataset not found: {path}")

    rows: list[list[str]] = []
    scanned_rows = 0
    with path.open(newline="", encoding="utf-8") as fp:
        reader = csv.reader(fp)
        header = next(reader, None)
        if header is None:
            raise SystemExit(f"dataset is empty: {path}")
        for line in reader:
            scanned_rows += 1
            if not valid_transfer(line):
                continue
            rows.append(normalize_transfer_row(line))
            if max_rows is not None and len(rows) >= max_rows:
                break

    if not rows:
        raise SystemExit(f"dataset has no effective transfer rows: {path}")
    if max_rows is not None and len(rows) < max_rows:
        raise SystemExit(f"dataset has only {len(rows)} effective transfer rows; need {max_rows}: {path}")

    return header, rows, scanned_rows


def valid_transfer(line: list[str]) -> bool:
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


def normalize_transfer_row(line: list[str]) -> list[str]:
    out = list(line)
    if len(out) < 13:
        out.extend([""] * (13 - len(out)))
    out[3] = out[3].lower()
    out[4] = out[4].lower()
    out[5] = ""
    out[6] = "0"
    out[7] = "0"
    out[12] = ""
    return out


def is_hex_address(value: str) -> bool:
    if not value.startswith("0x") or len(value) != 42:
        return False
    try:
        int(value[2:], 16)
        return True
    except ValueError:
        return False


def build_graph(rows: list[list[str]]) -> tuple[set[str], Counter[tuple[str, str]]]:
    accounts: set[str] = set()
    directed: Counter[tuple[str, str]] = Counter()
    for row in rows:
        if not valid_transfer(row):
            continue
        source = row[3].lower()
        dest = row[4].lower()
        accounts.add(source)
        accounts.add(dest)
        directed[(source, dest)] += 1
    return accounts, directed


def bidirectional_strength(
    directed: Counter[tuple[str, str]],
) -> tuple[dict[tuple[str, str], int], dict[str, int]]:
    edges: dict[tuple[str, str], int] = {}
    hot_score: defaultdict[str, int] = defaultdict(int)
    for (source, dest), forward in directed.items():
        if source >= dest:
            continue
        reverse = directed.get((dest, source), 0)
        strength = 2 * min(forward, reverse)
        if strength <= 0:
            continue
        edge = (source, dest)
        edges[edge] = strength
        hot_score[source] += strength
        hot_score[dest] += strength
    return edges, dict(hot_score)


def select_hot_accounts(
    accounts: set[str],
    hot_score: dict[str, int],
    hot_account_ratio: float,
) -> list[str]:
    hot_count = max(1, round(len(accounts) * hot_account_ratio))
    return sorted(accounts, key=lambda addr: (-hot_score.get(addr, 0), addr))[:hot_count]


def balanced_max_k_cut(
    hot_accounts: list[str],
    hot_shards: list[int],
    hot_score: dict[str, int],
    edges: dict[tuple[str, str], int],
    seed: int,
) -> dict[str, int]:
    rng = random.Random(seed)
    neighbors: defaultdict[str, list[tuple[str, int]]] = defaultdict(list)
    hot_set = set(hot_accounts)
    for (left, right), strength in edges.items():
        if left in hot_set and right in hot_set:
            neighbors[left].append((right, strength))
            neighbors[right].append((left, strength))

    capacities = shard_capacities(len(hot_accounts), len(hot_shards))
    order = sorted(hot_accounts, key=lambda addr: (-hot_score.get(addr, 0), addr))
    loads = {shard: 0 for shard in hot_shards}
    assigned: dict[str, int] = {}

    for addr in order:
        candidates = list(hot_shards)
        rng.shuffle(candidates)
        best_shard = None
        best_score = None
        for shard in candidates:
            capacity = capacities[hot_shards.index(shard)]
            if loads[shard] >= capacity:
                continue
            cut_gain = 0
            for neighbor, strength in neighbors.get(addr, []):
                neighbor_shard = assigned.get(neighbor)
                if neighbor_shard is not None and neighbor_shard != shard:
                    cut_gain += strength
            balance_bonus = -loads[shard] / max(1, capacity)
            score = cut_gain + balance_bonus
            if best_score is None or score > best_score:
                best_score = score
                best_shard = shard
        if best_shard is None:
            best_shard = min(hot_shards, key=lambda shard: loads[shard])
        assigned[addr] = best_shard
        loads[best_shard] += 1

    return assigned


def shard_capacities(account_count: int, shard_count: int) -> list[int]:
    base, remainder = divmod(account_count, shard_count)
    return [base + (1 if idx < remainder else 0) for idx in range(shard_count)]


def build_address_map(assignment: dict[str, int], shard_num: int, seed: int) -> dict[str, str]:
    used = set(assignment)
    address_map: dict[str, str] = {}
    for original, shard in sorted(assignment.items()):
        attempt = 0
        while True:
            digest = hashlib.sha256(f"{seed}:{shard_num}:{shard}:{attempt}:{original}".encode()).digest()
            suffix = shard.to_bytes(4, "big")
            synthetic = "0x" + (digest[:16] + suffix).hex()
            if synthetic not in used:
                used.add(synthetic)
                address_map[original] = synthetic
                break
            attempt += 1
    return address_map


def write_workload(
    path: Path,
    header: list[str],
    rows: list[list[str]],
    address_map: dict[str, str],
) -> None:
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.writer(fp)
        writer.writerow(header)
        for row in rows:
            out = list(row)
            if len(out) > 4:
                out[3] = address_map.get(out[3].lower(), out[3].lower())
                out[4] = address_map.get(out[4].lower(), out[4].lower())
            writer.writerow(out)


def write_mapping(
    path: Path,
    assignment: dict[str, int],
    address_map: dict[str, str],
    shard_num: int,
) -> None:
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.writer(fp)
        writer.writerow(["original_address", "synthetic_address", "assigned_shard", "verified_shard"])
        for original, shard in sorted(assignment.items()):
            synthetic = address_map[original]
            writer.writerow([original, synthetic, shard, default_account_loc(synthetic, shard_num)])


def write_stats(
    path: Path,
    dataset: Path,
    scanned_rows: int,
    row_count: int,
    accounts: set[str],
    hot_accounts: list[str],
    shard_num: int,
    hot_shards: list[int],
    assignment: dict[str, int],
    rows: list[list[str]],
    address_map: dict[str, str],
) -> None:
    hot_set = set(hot_accounts)
    shard_loads = Counter(assignment.values())
    cross_shard = 0
    hot_cross_shard = 0
    graph_rows = 0
    address_rows = 0
    for row in rows:
        if len(row) < 5 or not (is_hex_address(row[3]) and is_hex_address(row[4])):
            continue
        address_rows += 1
        if valid_transfer(row):
            graph_rows += 1
        source = address_map.get(row[3].lower(), row[3].lower())
        dest = address_map.get(row[4].lower(), row[4].lower())
        source_shard = default_account_loc(source, shard_num)
        dest_shard = default_account_loc(dest, shard_num)
        if source_shard != dest_shard:
            cross_shard += 1
            if row[3].lower() in hot_set and row[4].lower() in hot_set:
                hot_cross_shard += 1

    stats = {
        "source_dataset": str(dataset),
        "source_rows_scanned": scanned_rows,
        "effective_rows": row_count,
        "address_rows": address_rows,
        "graph_rows_positive_value": graph_rows,
        "account_count": len(accounts),
        "hot_account_count": len(hot_accounts),
        "hot_account_ratio": len(hot_accounts) / len(accounts),
        "shard_num": shard_num,
        "hot_shards": hot_shards,
        "hot_shard_count": len(hot_shards),
        "hot_shard_ratio": len(hot_shards) / shard_num,
        "hot_shard_account_loads": {str(k): shard_loads[k] for k in hot_shards},
        "cross_shard_tx_count": cross_shard,
        "cross_shard_tx_ratio_among_address_rows": cross_shard / max(1, address_rows),
        "hot_account_cross_shard_tx_count": hot_cross_shard,
        "hot_account_cross_shard_tx_ratio_among_address_rows": hot_cross_shard / max(1, address_rows),
    }
    path.write_text(json.dumps(stats, indent=2, sort_keys=True), encoding="utf-8")


def default_account_loc(address: str, shard_num: int) -> int:
    return int(address[-8:], 16) % shard_num


def hot_shard_count(shard_num: int, hot_shard_ratio: float, min_hot_shards: int) -> int:
    return min(shard_num, max(min_hot_shards, round(hot_shard_ratio * shard_num)))


def ratio_tag(value: float) -> str:
    return f"{round(value * 100):02d}"


def parse_ints(value: str) -> list[int]:
    ret = [int(part) for part in value.split(",") if part.strip()]
    if not ret or any(item <= 0 for item in ret):
        raise argparse.ArgumentTypeError("expected comma-separated positive integers")
    return ret


def ratio(value: str) -> float:
    ret = float(value)
    if ret <= 0 or ret > 1:
        raise argparse.ArgumentTypeError("ratio must be in (0, 1]")
    return ret


def positive_int(value: str) -> int:
    ret = int(value)
    if ret <= 0:
        raise argparse.ArgumentTypeError("value must be positive")
    return ret


if __name__ == "__main__":
    raise SystemExit(main())
