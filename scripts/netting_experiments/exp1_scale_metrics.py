"""Load derived CTX metrics from the five repeated exp1 scale runs."""

from __future__ import annotations

import csv
import math
from dataclasses import dataclass
from pathlib import Path
from typing import Iterable, Optional


DEFAULT_SUMMARIES = [
    Path("exp/netting-paper/repeated-exp1-scale/sessions/20261014-112256/summary.csv"),
    Path("exp/netting-paper/repeated-exp1-scale/sessions/repeat2-5-timeout3600/summary.csv"),
]
DEFAULT_OUT_DIR = Path("figures/netting/exp1-scale-repeated")
SHARDS = [4, 8, 16, 32]


@dataclass(frozen=True)
class ScaleSample:
    seed: int
    shard_num: int
    original_ctx: int
    netted_ctx: int
    fallback_ctx: int
    settlement_tx: int
    match_time_ms: float


def load_scale_samples(paths: Iterable[Path]) -> list[ScaleSample]:
    rows = load_summary_rows(paths)
    index = {
        (integer(row, "seed"), integer(row, "shard_num"), row.get("method", "")): row
        for row in rows
    }
    samples: list[ScaleSample] = []
    for seed, shard_num, method in sorted(index):
        if method != "netting_static_relay" or shard_num not in SHARDS:
            continue
        netting_row = index[(seed, shard_num, method)]
        baseline_row = index.get((seed, shard_num, "static_relay"))
        if baseline_row is None:
            raise SystemExit(f"missing static_relay row for seed={seed}, shards={shard_num}")
        original_ctx = integer(baseline_row, "stage_latency_tx_count")
        converted_ctx, netted_ctx = count_netting_intents(run_dir(netting_row))
        if converted_ctx > original_ctx or netted_ctx > converted_ctx:
            raise SystemExit(
                f"invalid CTX counts for seed={seed}, shards={shard_num}: "
                f"original={original_ctx}, converted={converted_ctx}, netted={netted_ctx}"
            )
        fallback_ctx = original_ctx - netted_ctx
        if netting_row.get("settlement_tx_count", "").strip():
            settlement_tx = integer(netting_row, "settlement_tx_count")
        else:
            physical_tx = count_leader_block_transactions(run_dir(netting_row), shard_num)
            input_tx = integer(netting_row, "tx_number")
            unconverted_ctx = original_ctx - converted_ctx
            settlement_tx = physical_tx - input_tx - unconverted_ctx
        if settlement_tx < 0:
            raise SystemExit(
                f"negative settlement count for seed={seed}, shards={shard_num}: {settlement_tx}"
            )
        samples.append(
            ScaleSample(
                seed=seed,
                shard_num=shard_num,
                original_ctx=original_ctx,
                netted_ctx=netted_ctx,
                fallback_ctx=fallback_ctx,
                settlement_tx=settlement_tx,
                match_time_ms=number(netting_row, "match_time_ms"),
            )
        )
    if not samples:
        raise SystemExit("no repeated exp1 netting rows found")
    return samples


def load_summary_rows(paths: Iterable[Path]) -> list[dict[str, str]]:
    rows: list[dict[str, str]] = []
    seen: set[tuple[str, str, str]] = set()
    for path in paths:
        if not path.is_file():
            raise SystemExit(f"summary not found: {path}")
        with path.open(newline="", encoding="utf-8") as fp:
            for row in csv.DictReader(fp):
                if row.get("experiment") != "exp1_scale_repeated":
                    continue
                key = (row.get("seed", ""), row.get("shard_num", ""), row.get("method", ""))
                if key in seen:
                    raise SystemExit(f"duplicate seed/shard/method row: {key}")
                seen.add(key)
                row["_summary_path"] = str(path.resolve())
                rows.append(row)
    return rows


def run_dir(row: dict[str, str]) -> Path:
    summary_path = Path(row["_summary_path"])
    repeat = integer(row, "repeat")
    candidate = summary_path.parent / "runs" / f"repeat_{repeat:02d}" / row["run_id"]
    if candidate.is_dir():
        return candidate
    candidate = summary_path.parent / "runs" / row["run_id"]
    if candidate.is_dir():
        return candidate
    raise SystemExit(f"run directory not found for {row.get('run_id', '')}")


def count_netting_intents(path: Path) -> tuple[int, int]:
    metrics = path / "results" / "netting_intent_metrics.csv"
    if not metrics.is_file():
        raise SystemExit(f"netting intent metrics not found: {metrics}")
    converted = 0
    netted = 0
    with metrics.open(newline="", encoding="utf-8") as fp:
        for row in csv.DictReader(fp):
            converted += 1
            if row.get("UsedFallback", "").strip().lower() != "true":
                netted += 1
    return converted, netted


def count_leader_block_transactions(path: Path, shard_num: int) -> int:
    total = 0
    for shard_id in range(shard_num):
        record = path / "block_record" / f"shard={shard_id}_node=0" / "block_record.csv"
        if not record.is_file():
            raise SystemExit(f"leader block record not found: {record}")
        with record.open(newline="", encoding="utf-8") as fp:
            for row in csv.DictReader(fp):
                total += integer(row, "TxBodyLen")
    return total


def validate_repeats(samples: list[ScaleSample], expected: Optional[int] = 5) -> None:
    problems: list[str] = []
    for shard_num in SHARDS:
        shard_samples = [sample for sample in samples if sample.shard_num == shard_num]
        if not shard_samples:
            problems.append(f"{shard_num} shards: no samples")
        elif expected is not None and len(shard_samples) != expected:
            problems.append(f"{shard_num} shards: expected n={expected}, got n={len(shard_samples)}")
    if problems:
        raise SystemExit("incomplete scale data:\n  " + "\n  ".join(problems))


def integer(row: dict[str, str], key: str) -> int:
    value = number(row, key)
    if not value.is_integer():
        raise SystemExit(f"expected integer {key}, got {row.get(key, '')!r}")
    return int(value)


def number(row: dict[str, str], key: str) -> float:
    try:
        value = float(row[key])
    except (KeyError, TypeError, ValueError) as exc:
        raise SystemExit(f"invalid {key}: {row.get(key, '')!r}") from exc
    if not math.isfinite(value):
        raise SystemExit(f"non-finite {key}: {value}")
    return value


def configure_matplotlib(dpi: int) -> None:
    import matplotlib.pyplot as plt

    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "Times", "DejaVu Serif"],
            "font.size": 9,
            "axes.labelsize": 9.5,
            "axes.titlesize": 10,
            "legend.fontsize": 8,
            "xtick.labelsize": 8.5,
            "ytick.labelsize": 8.5,
            "figure.dpi": dpi,
            "savefig.dpi": dpi,
            "savefig.bbox": "tight",
            "axes.spines.top": False,
            "axes.spines.right": False,
            "axes.grid": True,
            "grid.alpha": 0.18,
            "grid.linestyle": "-",
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
        }
    )


def save_figure(fig: object, out_stem: Path) -> list[Path]:
    import matplotlib.pyplot as plt

    out_stem.parent.mkdir(parents=True, exist_ok=True)
    outputs = [out_stem.with_suffix(".pdf"), out_stem.with_suffix(".png")]
    for output in outputs:
        fig.savefig(output)
    plt.close(fig)
    return outputs


def write_samples(samples: list[ScaleSample], path: Path) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=list(ScaleSample.__dataclass_fields__))
        writer.writeheader()
        for sample in samples:
            writer.writerow(sample.__dict__)
