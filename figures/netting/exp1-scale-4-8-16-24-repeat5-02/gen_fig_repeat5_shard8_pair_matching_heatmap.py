#!/usr/bin/env python3
"""Plot five-repeat mean full-match rates for every undirected 8-shard pair."""

from __future__ import annotations

import argparse
import csv
import hashlib
import math
import re
import struct
from collections import defaultdict
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[3]
DEFAULT_SESSIONS_ROOT = REPO_ROOT / "exp/netting-paper/repeated-exp1-scale-4-8-16-24/sessions"
DEFAULT_OUT = Path(__file__).resolve().parent
METHOD = "netting_static_relay"
SHARD_COUNT = 8
SEEDS = (1, 2, 3, 4, 5)
INTENT_DOMAIN = b"BLOCKEMULATOR_NETTING_INTENT_V1"
MAX_UINT64 = 2**64 - 1


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sessions-root", type=Path, default=DEFAULT_SESSIONS_ROOT)
    parser.add_argument("--out", type=Path, default=DEFAULT_OUT)
    parser.add_argument("--dpi", type=int, default=300)
    return parser.parse_args()


def discover_run(sessions_root: Path, seed: int) -> Path:
    run_name = f"exp1_scale_repeated_{METHOD}_shard_num_{SHARD_COUNT}_seed{seed}"
    candidates = [
        path
        for path in sessions_root.glob(f"*/runs/repeat_*/{run_name}")
        if (path / "workload.csv").exists()
        and (path / "results" / "netting_intent_metrics.csv").exists()
    ]
    if len(candidates) != 1:
        raise SystemExit(
            f"expected one complete run for seed={seed}; found {len(candidates)}: {candidates}"
        )
    return candidates[0]


def read_chain_id(config_path: Path) -> int:
    pattern = re.compile(r"^\s*chain_id:\s*(\d+)\s*$")
    for line in config_path.read_text(encoding="utf-8").splitlines():
        match = pattern.match(line)
        if match:
            return int(match.group(1))
    raise SystemExit(f"chain_id not found in {config_path}")


def parse_address(raw: str) -> bytes:
    value = raw.strip()
    if value.lower().startswith("0x"):
        value = value[2:]
    address = bytes.fromhex(value)
    if len(address) != 20:
        raise ValueError(f"address must contain 20 bytes: {raw}")
    return address


def shard_of(address: bytes) -> int:
    return int.from_bytes(address[-4:], "big") % SHARD_COUNT


def intent_id(
    chain_id: int,
    sender: bytes,
    recipient: bytes,
    source_shard: int,
    destination_shard: int,
    amount: int,
    nonce: int,
) -> str:
    amount_bytes = amount.to_bytes((amount.bit_length() + 7) // 8, "big")
    payload = b"".join(
        [
            INTENT_DOMAIN,
            bytes([1]),
            struct.pack(">Q", chain_id),
            sender,
            recipient,
            struct.pack(">Q", source_shard),
            struct.pack(">Q", destination_shard),
            bytes(32),
            struct.pack(">I", len(amount_bytes)),
            amount_bytes,
            struct.pack(">Q", nonce),
            struct.pack(">Q", MAX_UINT64),
        ]
    )
    return hashlib.sha256(payload).hexdigest()


def map_intents_to_pairs(run_dir: Path) -> dict[str, tuple[int, int]]:
    chain_id = read_chain_id(run_dir / "config.yaml")
    nonces: dict[bytes, int] = defaultdict(int)
    mapping = {}
    with (run_dir / "workload.csv").open(newline="", encoding="utf-8") as fp:
        for row in csv.DictReader(fp):
            try:
                sender = parse_address(row["col3"])
                recipient_raw = row["col4"].strip()
                if not recipient_raw or recipient_raw.lower() == "none":
                    continue
                recipient = parse_address(recipient_raw)
                amount = int(row["col8"].strip())
            except (KeyError, TypeError, ValueError):
                continue
            if sender == recipient or amount <= 0:
                continue
            source, destination = shard_of(sender), shard_of(recipient)
            if source == destination:
                continue
            nonce = nonces[sender]
            nonces[sender] += 1
            identifier = intent_id(
                chain_id,
                sender,
                recipient,
                source,
                destination,
                amount,
                nonce,
            )
            mapping[identifier] = tuple(sorted((source, destination)))
    return mapping


def load_seed_rates(run_dir: Path) -> dict[tuple[int, int], dict[str, float]]:
    pair_by_intent = map_intents_to_pairs(run_dir)
    counts: dict[tuple[int, int], list[int]] = defaultdict(lambda: [0, 0])
    observed = set()
    metrics_path = run_dir / "results" / "netting_intent_metrics.csv"
    with metrics_path.open(newline="", encoding="utf-8") as fp:
        for row in csv.DictReader(fp):
            identifier = row["IntentID"].lower()
            pair = pair_by_intent.get(identifier)
            if pair is None:
                raise SystemExit(f"intent {identifier} in {metrics_path} is absent from workload mapping")
            observed.add(identifier)
            counts[pair][1] += 1
            if row["UsedFallback"].strip().lower() != "true":
                counts[pair][0] += 1

    if observed != set(pair_by_intent):
        missing = len(set(pair_by_intent) - observed)
        raise SystemExit(f"{metrics_path} is missing {missing} workload intents")

    rates = {}
    for lower in range(SHARD_COUNT):
        for higher in range(lower + 1, SHARD_COUNT):
            full_matches, total = counts[(lower, higher)]
            if total == 0:
                raise SystemExit(f"no intents for shard pair {lower}-{higher} in {run_dir}")
            rates[(lower, higher)] = {
                "full_matches": float(full_matches),
                "total": float(total),
                "rate": full_matches / total,
            }
    return rates


def aggregate_rates(
    sessions_root: Path,
) -> tuple[
    dict[tuple[int, int], dict[str, float]],
    list[dict[str, float | int]],
]:
    per_pair_rates: dict[tuple[int, int], list[float]] = defaultdict(list)
    details: list[dict[str, float | int]] = []
    for seed in SEEDS:
        rates = load_seed_rates(discover_run(sessions_root, seed))
        for (lower, higher), values in rates.items():
            rate = values["rate"]
            per_pair_rates[(lower, higher)].append(rate)
            details.append(
                {
                    "seed": seed,
                    "lower": lower,
                    "higher": higher,
                    "full_matches": int(values["full_matches"]),
                    "total": int(values["total"]),
                    "rate": rate,
                }
            )

    aggregate = {}
    for pair, rates in per_pair_rates.items():
        if len(rates) != len(SEEDS):
            raise SystemExit(f"expected {len(SEEDS)} rates for pair {pair}, got {len(rates)}")
        mean = sum(rates) / len(rates)
        variance = sum((rate - mean) ** 2 for rate in rates) / len(rates)
        aggregate[pair] = {"mean": mean, "std": math.sqrt(variance)}
    return aggregate, details


def write_csvs(
    stem: Path,
    aggregate: dict[tuple[int, int], dict[str, float]],
    details: list[dict[str, float | int]],
) -> None:
    with stem.with_suffix(".csv").open("w", newline="", encoding="utf-8") as fp:
        writer = csv.writer(fp)
        writer.writerow(
            ["LowerShard", "HigherShard", "RepeatCount", "MeanMatchingRate", "StdMatchingRate"]
        )
        for pair in sorted(aggregate):
            row = aggregate[pair]
            writer.writerow([pair[0], pair[1], len(SEEDS), f'{row["mean"]:.9f}', f'{row["std"]:.9f}'])

    detail_path = stem.with_name(f"{stem.name}_per_repeat").with_suffix(".csv")
    with detail_path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.writer(fp)
        writer.writerow(
            ["Seed", "LowerShard", "HigherShard", "FullyMatchedIntents", "TotalIntents", "MatchingRate"]
        )
        for row in details:
            writer.writerow(
                [
                    row["seed"],
                    row["lower"],
                    row["higher"],
                    row["full_matches"],
                    row["total"],
                    f'{row["rate"]:.9f}',
                ]
            )


def plot_heatmap(
    stem: Path,
    aggregate: dict[tuple[int, int], dict[str, float]],
    dpi: int,
) -> None:
    import matplotlib

    matplotlib.use("Agg")
    import matplotlib.pyplot as plt
    import numpy as np
    from matplotlib.colors import LinearSegmentedColormap
    from matplotlib.ticker import PercentFormatter

    matrix = np.full((SHARD_COUNT, SHARD_COUNT), np.nan)
    for (lower, higher), values in aggregate.items():
        matrix[lower, higher] = values["mean"]
        matrix[higher, lower] = values["mean"]
    masked = np.ma.masked_invalid(matrix)

    # Use a fixed 70--100% domain: preserve visible variation among the
    # 90%+ measurements without stretching their narrow observed range.
    vmin, vmax = 0.7, 1.0

    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "Times", "DejaVu Serif"],
            "font.size": 10,
            "axes.labelsize": 10.5,
            "figure.dpi": dpi,
            "savefig.dpi": dpi,
            "savefig.bbox": "tight",
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
        }
    )

    # Use the light-to-medium portion of YlGnBu and blend it with a neutral
    # paper gray. This keeps the scale monotonic while reducing saturation.
    base_cmap = plt.get_cmap("YlGnBu")
    colors = base_cmap(np.linspace(0.02, 0.58, 256))
    neutral = np.array([0.96, 0.97, 0.98])
    colors[:, :3] = 0.30 * colors[:, :3] + 0.70 * neutral
    cmap = LinearSegmentedColormap.from_list(
        "YlGnBu_pastel", colors
    )
    cmap.set_bad("#ECEFF1")
    fig, ax = plt.subplots(figsize=(4.1, 3.25))
    image = ax.imshow(masked, cmap=cmap, vmin=vmin, vmax=vmax, interpolation="nearest", aspect="equal")
    ax.set_xticks(range(SHARD_COUNT))
    ax.set_yticks(range(SHARD_COUNT))
    ax.set_xlabel("Shard #")
    ax.set_ylabel("Shard #")
    ax.tick_params(axis="both", which="both", length=0)
    for spine in ax.spines.values():
        spine.set_visible(False)

    colorbar = fig.colorbar(image, ax=ax, fraction=0.047, pad=0.025)
    colorbar.set_label("Average Matching Rate", rotation=90, labelpad=8)
    colorbar.set_ticks([0.7, 0.8, 0.9, 1.0])
    colorbar.ax.yaxis.set_major_formatter(PercentFormatter(xmax=1.0, decimals=0))
    colorbar.outline.set_linewidth(0.5)
    fig.tight_layout(pad=0.4)
    fig.savefig(stem.with_suffix(".pdf"))
    fig.savefig(stem.with_suffix(".png"))
    plt.close(fig)


def main() -> int:
    args = parse_args()
    aggregate, details = aggregate_rates(args.sessions_root)
    args.out.mkdir(parents=True, exist_ok=True)
    stem = args.out / "fig_repeat5_shard8_pair_matching_rate_heatmap"
    write_csvs(stem, aggregate, details)
    plot_heatmap(stem, aggregate, args.dpi)
    print(f"generated: {stem.with_suffix('.pdf')}")
    print(f"generated: {stem.with_suffix('.png')}")
    print(f"generated: {stem.with_suffix('.csv')}")
    print(f"range: {min(row['mean'] for row in aggregate.values()):.6f}--{max(row['mean'] for row in aggregate.values()):.6f}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
