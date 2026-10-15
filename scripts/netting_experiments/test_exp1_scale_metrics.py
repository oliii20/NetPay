from __future__ import annotations

import csv
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from exp1_scale_metrics import load_scale_samples


SUMMARY_FIELDS = [
    "repeat",
    "run_id",
    "experiment",
    "method",
    "seed",
    "shard_num",
    "stage_latency_tx_count",
    "tx_number",
    "match_time_ms",
    "settlement_tx_count",
]


class Exp1ScaleMetricsTest(unittest.TestCase):
    def test_load_scale_samples_derives_ctx_and_settlement_counts(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            summary = root / "summary.csv"
            run_id = "netting_seed1"
            write_csv(
                summary,
                SUMMARY_FIELDS,
                [
                    {
                        "repeat": 1,
                        "run_id": run_id,
                        "experiment": "exp1_scale_repeated",
                        "method": "netting_static_relay",
                        "seed": 1,
                        "shard_num": 4,
                        "stage_latency_tx_count": 60,
                        "tx_number": 100,
                        "match_time_ms": 2.5,
                    },
                    {
                        "repeat": 1,
                        "run_id": "relay_seed1",
                        "experiment": "exp1_scale_repeated",
                        "method": "static_relay",
                        "seed": 1,
                        "shard_num": 4,
                        "stage_latency_tx_count": 80,
                        "tx_number": 100,
                        "match_time_ms": 0,
                    },
                ],
            )
            run = root / "runs" / "repeat_01" / run_id
            intents = [
                {"UsedFallback": "false"} for _ in range(50)
            ] + [{"UsedFallback": "true"} for _ in range(10)]
            write_csv(run / "results" / "netting_intent_metrics.csv", ["UsedFallback"], intents)
            write_csv(
                run / "results" / "netting_batch_metrics.csv",
                ["IntentCount"],
                [{"IntentCount": 60}],
            )
            # 100 input tx + 20 unconverted CTX second hops + 4 settlement tx.
            for shard_id, tx_count in enumerate([31, 31, 31, 31]):
                write_csv(
                    run / "block_record" / f"shard={shard_id}_node=0" / "block_record.csv",
                    ["TxBodyLen"],
                    [{"TxBodyLen": tx_count}],
                )

            samples = load_scale_samples([summary])

            self.assertEqual(1, len(samples))
            sample = samples[0]
            self.assertEqual(80, sample.original_ctx)
            self.assertEqual(60, sample.matching_ctx)
            self.assertEqual(50, sample.netted_ctx)
            self.assertEqual(10, sample.fallback_ctx)
            self.assertAlmostEqual(1 / 6, sample.fallback_ratio)
            self.assertEqual(4, sample.settlement_tx)
            self.assertEqual(2.5, sample.match_time_ms)

    def test_load_scale_samples_prefers_native_settlement_count(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            summary = root / "summary.csv"
            run_id = "netting_seed1"
            write_csv(
                summary,
                SUMMARY_FIELDS,
                [
                    {
                        "repeat": 1,
                        "run_id": run_id,
                        "experiment": "exp1_scale_repeated",
                        "method": "netting_static_relay",
                        "seed": 1,
                        "shard_num": 4,
                        "stage_latency_tx_count": 60,
                        "tx_number": 100,
                        "match_time_ms": 2.5,
                        "settlement_tx_count": 7,
                    },
                    {
                        "repeat": 1,
                        "run_id": "relay_seed1",
                        "experiment": "exp1_scale_repeated",
                        "method": "static_relay",
                        "seed": 1,
                        "shard_num": 4,
                        "stage_latency_tx_count": 80,
                        "tx_number": 100,
                        "match_time_ms": 0,
                        "settlement_tx_count": 0,
                    },
                ],
            )
            run = root / "runs" / "repeat_01" / run_id
            intents = [{"UsedFallback": "false"} for _ in range(50)] + [
                {"UsedFallback": "true"} for _ in range(10)
            ]
            write_csv(run / "results" / "netting_intent_metrics.csv", ["UsedFallback"], intents)
            write_csv(
                run / "results" / "netting_batch_metrics.csv",
                ["IntentCount"],
                [{"IntentCount": 60}],
            )

            samples = load_scale_samples([summary])

            self.assertEqual(7, samples[0].settlement_tx)


def write_csv(path: Path, fields: list[str], rows: list[dict[str, object]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=fields)
        writer.writeheader()
        writer.writerows(rows)


if __name__ == "__main__":
    unittest.main()
