#!/usr/bin/env python3

import csv
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import run_experiments as runner


def ts(second: int) -> str:
    return f"2026-01-01T00:00:{second:02d}+00:00"


class RunExperimentSummaryTest(unittest.TestCase):
    def test_relay_stage_latencies_split_queue_source_transfer_target(self) -> None:
        stages = runner.relay_stage_latencies(
            [
                {
                    "Tx create time": ts(0),
                    "Is cross-shard tx or not": "true",
                    "Relay1 block propose time": ts(1),
                    "Relay1 tx commit time": ts(4),
                    "Relay2 tx create time": ts(5),
                    "Relay2 tx commit time": ts(9),
                }
            ]
        )

        self.assertAlmostEqual(stages["stage_queue_s"], 1.0)
        self.assertAlmostEqual(stages["stage_source_s"], 3.0)
        self.assertAlmostEqual(stages["stage_transfer_s"], 1.0)
        self.assertAlmostEqual(stages["stage_target_s"], 4.0)
        self.assertAlmostEqual(stages["stage_second_hop_s"], 5.0)

    def test_broker_stage_latencies_split_queue_source_coordination_target(self) -> None:
        stages = runner.broker_stage_latencies(
            [
                {
                    "Mechanism": "Broker",
                    "Tx create time": ts(0),
                    "Broker1 tx create time": ts(1),
                    "Broker1 tx commit time": ts(4),
                    "Broker2 tx create time": ts(6),
                    "Broker2 tx commit time": ts(10),
                }
            ]
        )

        self.assertAlmostEqual(stages["stage_queue_s"], 1.0)
        self.assertAlmostEqual(stages["stage_source_s"], 3.0)
        self.assertAlmostEqual(stages["stage_coordination_s"], 2.0)
        self.assertAlmostEqual(stages["stage_target_s"], 4.0)
        self.assertAlmostEqual(stages["stage_second_hop_s"], 6.0)

    def test_summarize_broker_reports_actual_relay_fallback_ratio(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            run_dir = Path(tmp)
            results = run_dir / "results"
            results.mkdir()
            with (results / "broker_stats_brief_info.csv").open("w", newline="", encoding="utf-8") as fp:
                writer = csv.writer(fp)
                writer.writerow(["Total tx # in this epoch", "Avg. TPS of this epoch (txs per second)"])
                writer.writerow(["2", "1"])
            fieldnames = [
                "OriginalHash",
                "Mechanism",
                "Tx create time",
                "Tx finally commit time",
                "Is broker tx or not",
                "Inner shard tx block propose time",
                "Broker1 tx create time",
                "Broker1 block propose time",
                "Broker1 tx commit time",
                "Broker2 tx create time",
                "Broker2 block propose time",
                "Broker2 tx commit time",
            ]
            with (results / "broker_stats_detail_tx_info.csv").open("w", newline="", encoding="utf-8") as fp:
                writer = csv.DictWriter(fp, fieldnames=fieldnames)
                writer.writeheader()
                writer.writerow(
                    {
                        "Mechanism": "Broker",
                        "Tx create time": ts(0),
                        "Tx finally commit time": ts(8),
                        "Broker1 tx create time": ts(1),
                        "Broker1 tx commit time": ts(3),
                        "Broker2 tx create time": ts(4),
                        "Broker2 tx commit time": ts(8),
                    }
                )
                writer.writerow(
                    {
                        "Mechanism": "FallbackToRelay",
                        "Tx create time": ts(0),
                        "Tx finally commit time": ts(5),
                    }
                )

            summary = runner.summarize_broker(run_dir, runner.RunSpec("exp", "method", "clpa_broker"))

        self.assertAlmostEqual(summary["fallback_value_ratio"], 0.5)
        self.assertAlmostEqual(summary["fallback_intent_ratio"], 0.5)

    def test_summarize_broker_amortizes_clpa_repartition_latency(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            run_dir = Path(tmp)
            results = run_dir / "results"
            results.mkdir()
            with (results / "broker_stats_brief_info.csv").open("w", newline="", encoding="utf-8") as fp:
                writer = csv.writer(fp)
                writer.writerow(["Total tx # in this epoch", "Avg. TPS of this epoch (txs per second)"])
                writer.writerow(["2", "1"])
            with (results / "broker_stats_detail_tx_info.csv").open("w", newline="", encoding="utf-8") as fp:
                fieldnames = [
                    "Mechanism",
                    "Tx create time",
                    "Tx finally commit time",
                    "Broker1 tx create time",
                    "Broker1 tx commit time",
                    "Broker2 tx create time",
                    "Broker2 tx commit time",
                ]
                writer = csv.DictWriter(fp, fieldnames=fieldnames)
                writer.writeheader()
                for _ in range(2):
                    writer.writerow(
                        {
                            "Mechanism": "Broker",
                            "Tx create time": ts(0),
                            "Tx finally commit time": ts(8),
                            "Broker1 tx create time": ts(1),
                            "Broker1 tx commit time": ts(3),
                            "Broker2 tx create time": ts(4),
                            "Broker2 tx commit time": ts(8),
                        }
                    )
            with (results / "clpa_repartition_metrics.csv").open("w", newline="", encoding="utf-8") as fp:
                writer = csv.writer(fp)
                writer.writerow(
                    [
                        "Epoch",
                        "Start time",
                        "Partition end time",
                        "Broadcast end time",
                        "Sync end time",
                        "Migrated account count",
                        "Partition latency ns",
                        "Broadcast latency ns",
                        "Migration sync latency ns",
                        "Total latency ns",
                    ]
                )
                writer.writerow(["1", ts(0), ts(1), ts(2), ts(10), "7", "1000000000", "1000000000", "8000000000", "10000000000"])

            spec = runner.RunSpec(
                "exp",
                "method",
                "clpa_broker",
                method="clpa_broker",
                consensus_type="clpa_broker",
                netting_enabled=False,
            )
            summary = runner.summarize_broker(run_dir, spec)

        self.assertAlmostEqual(summary["clpa_rounds"], 1.0)
        self.assertAlmostEqual(summary["clpa_migrated_accounts"], 7.0)
        self.assertAlmostEqual(summary["clpa_partition_s"], 1.0)
        self.assertAlmostEqual(summary["clpa_broadcast_s"], 1.0)
        self.assertAlmostEqual(summary["clpa_migration_wait_s"], 8.0)
        self.assertAlmostEqual(summary["clpa_total_s"], 10.0)
        self.assertAlmostEqual(summary["stage_clpa_s"], 5.0)

    def test_validate_specs_rejects_method_consensus_mismatch(self) -> None:
        spec = runner.RunSpec(
            "exp1_baseline",
            "method",
            "clpa_broker",
            method="clpa_broker",
            consensus_type="static_broker",
            netting_enabled=False,
        )

        with self.assertRaises(SystemExit):
            runner.validate_specs([spec])


if __name__ == "__main__":
    unittest.main()
