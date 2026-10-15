from __future__ import annotations

import csv
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import plot_exp1_scale_matching_rate_boxplot as matching
import plot_exp1_scale_tps_latency_boxplots as performance


FIELDS = [
    "experiment",
    "method",
    "seed",
    "shard_num",
    "throughput_tps",
    "avg_latency_s",
    "matched_intent_ratio",
]


class Exp1ScaleBoxplotTest(unittest.TestCase):
    def test_performance_loader_merges_summaries(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            first = Path(tmp) / "first.csv"
            second = Path(tmp) / "second.csv"
            write_summary(first, [row("static_broker", 1, 4, 100.0, 2.0, 0.0)])
            write_summary(second, [row("static_broker", 2, 4, 110.0, 1.8, 0.0)])

            rows = performance.load_rows([first, second])
            values = performance.values_by_shard_method(rows, "throughput_tps")

            self.assertEqual([100.0, 110.0], values[(4, "static_broker")])

    def test_matching_rate_is_transaction_count_percentage(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            summary = Path(tmp) / "summary.csv"
            write_summary(summary, [row("netting_static_relay", 1, 8, 200.0, 1.0, 0.625)])

            values = matching.load_matching_rates([summary])

            self.assertEqual([62.5], values[8])


def row(method: str, seed: int, shard: int, tps: float, latency: float, rate: float) -> dict[str, object]:
    return {
        "experiment": "exp1_scale_repeated",
        "method": method,
        "seed": seed,
        "shard_num": shard,
        "throughput_tps": tps,
        "avg_latency_s": latency,
        "matched_intent_ratio": rate,
    }


def write_summary(path: Path, rows: list[dict[str, object]]) -> None:
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=FIELDS)
        writer.writeheader()
        writer.writerows(rows)


if __name__ == "__main__":
    unittest.main()
