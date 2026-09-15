#!/usr/bin/env python3

from __future__ import annotations

import csv
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import run_exp1_repeated_boxplots as repeated


class RepeatedExp1BoxplotTest(unittest.TestCase):
    def test_metric_groups_uses_three_exp1_system_labels(self) -> None:
        rows = [
            {"experiment": "exp1_baseline", "method": "static_relay", "throughput_tps": "100"},
            {"experiment": "exp1_baseline", "method": "clpa_broker", "throughput_tps": "120"},
            {"experiment": "exp1_baseline", "method": "netting_static_relay", "throughput_tps": "160"},
            {"experiment": "exp2_balance", "method": "netting_static_relay", "throughput_tps": "999"},
        ]

        groups = repeated.metric_groups(rows, "throughput_tps")

        self.assertEqual(
            [("Relay", [100.0]), ("BrokerChain", [120.0]), ("Netting", [160.0])],
            groups,
        )

    def test_metric_groups_ignores_deprecated_method_level_flag(self) -> None:
        rows = [
            {"experiment": "exp1_baseline", "method": "static_relay", "avg_latency_s": "40"},
            {"experiment": "exp1_baseline", "method": "clpa_broker", "avg_latency_s": "35"},
            {"experiment": "exp1_baseline", "method": "netting_static_relay", "avg_latency_s": "18"},
        ]

        groups = repeated.metric_groups(rows, "avg_latency_s", method_level=True)

        self.assertEqual(
            [
                ("Relay", [40.0]),
                ("BrokerChain", [35.0]),
                ("Netting", [18.0]),
            ],
            groups,
        )

    def test_read_repeated_summary(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "summary.csv"
            with path.open("w", newline="", encoding="utf-8") as fp:
                writer = csv.DictWriter(fp, fieldnames=["repeat", "method", "throughput_tps"])
                writer.writeheader()
                writer.writerow({"repeat": "1", "method": "static_relay", "throughput_tps": "100"})

            rows = repeated.read_repeated_summary(path)

        self.assertEqual("1", rows[0]["repeat"])
        self.assertEqual("static_relay", rows[0]["method"])


if __name__ == "__main__":
    unittest.main()
