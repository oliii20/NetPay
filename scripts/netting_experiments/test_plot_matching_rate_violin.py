#!/usr/bin/env python3

from __future__ import annotations

import csv
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import plot_matching_rate_violin as violin


class PlotMatchingRateViolinTest(unittest.TestCase):
    def test_collect_window_level_matching_rates_from_repeated_session(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            run_id = "exp1_baseline_netting_static_relay_method_netting_static_relay_seed1"
            summary = root / "summary.csv"
            write_summary(
                summary,
                [
                    {
                        "repeat": "1",
                        "run_id": run_id,
                        "method": "netting_static_relay",
                        "batch_count": "2",
                        "matched_intent_ratio": "0.8",
                    }
                ],
            )
            batch_metrics = root / "runs" / "repeat_01" / run_id / "results" / "netting_batch_metrics.csv"
            batch_metrics.parent.mkdir(parents=True)
            batch_metrics.write_text(
                "WindowID,MatchedIntentRatio,MatchedValueRatio,IntentCount\n"
                "1,0.50,0.40,10\n"
                "2,0.75,0.70,12\n",
                encoding="utf-8",
            )

            samples = violin.collect_matching_rates(summary, "intent", "window", 20)

        self.assertEqual([0.5, 0.75], [sample.rate for sample in samples])
        self.assertEqual(["1", "2"], [sample.window_id for sample in samples])

    def test_collect_run_level_matching_rates_from_summary(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            summary = root / "summary.csv"
            write_summary(
                summary,
                [
                    {
                        "run_id": "net_1",
                        "method": "netting_static_relay",
                        "batch_count": "2",
                        "matched_intent_ratio": "0.8",
                    },
                    {
                        "run_id": "relay_1",
                        "method": "static_relay",
                        "batch_count": "0",
                        "matched_intent_ratio": "0.0",
                    },
                ],
            )

            samples = violin.collect_matching_rates(summary, "intent", "run", 20)

        self.assertEqual(["net_1"], [sample.run_id for sample in samples])
        self.assertEqual([0.8], [sample.rate for sample in samples])

    def test_select_netting_rows_honors_count(self) -> None:
        rows = [
            {"run_id": "net_1", "method": "netting_static_relay", "batch_count": "1"},
            {"run_id": "net_2", "method": "netting_static_relay", "batch_count": "1"},
        ]

        selected = violin.select_netting_rows(rows, 1)

        self.assertEqual(["net_1"], [row["run_id"] for row in selected])


def write_summary(path: Path, rows: list[dict[str, str]]) -> None:
    fieldnames = sorted({key for row in rows for key in row})
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(rows)


if __name__ == "__main__":
    unittest.main()
