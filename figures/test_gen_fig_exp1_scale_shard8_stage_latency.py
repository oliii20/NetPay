from __future__ import annotations

import csv
import importlib.util
import tempfile
import unittest
from pathlib import Path
from unittest import mock


SCRIPT = Path(__file__).with_name("gen_fig_exp1_scale_shard8_stage_latency.py")
SPEC = importlib.util.spec_from_file_location("stage_latency_figure", SCRIPT)
assert SPEC and SPEC.loader
FIGURE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(FIGURE)


class StageLatencyFigureTest(unittest.TestCase):
    def test_collect_means_filters_shards_and_averages_repeats(self) -> None:
        with tempfile.TemporaryDirectory() as temp_dir:
            summary = Path(temp_dir) / "summary.csv"
            with summary.open("w", newline="", encoding="utf-8") as handle:
                writer = csv.DictWriter(handle, fieldnames=["shard_num", "method", "repeat"])
                writer.writeheader()
                for method, _label in FIGURE.METHODS:
                    writer.writerow({"shard_num": "8", "method": method, "repeat": "1"})
                    writer.writerow({"shard_num": "8", "method": method, "repeat": "2"})
                    writer.writerow({"shard_num": "4", "method": method, "repeat": "3"})

            fields = [field for field, _label, _color, _pattern in FIGURE.STAGES]

            def fake_stages(_summary: Path, row: dict[str, str]) -> dict[str, float]:
                value = float(row["repeat"])
                return {field: value for field in fields}

            with mock.patch.object(FIGURE, "stages_for_row", side_effect=fake_stages):
                means, counts = FIGURE.collect_means(summary, 8)

            for method, _label in FIGURE.METHODS:
                self.assertEqual(2, counts[method])
                self.assertEqual({field: 1.5 for field in fields}, means[method])

    def test_nice_limit_leaves_headroom(self) -> None:
        self.assertEqual(35, FIGURE.nice_limit(29.5))
        self.assertEqual([0, 10, 20, 30], FIGURE.axis_ticks(35))

    def test_clpa_stages_keep_full_source_and_target_durations(self) -> None:
        rows = [
            {
                "Tx create time": "2026-10-15T10:00:00+08:00",
                "Relay1 tx commit time": "2026-10-15T10:00:10+08:00",
                "Tx finally commit time": "2026-10-15T10:00:25+08:00",
                "Is cross-shard tx or not": "true",
            },
            {
                "Tx create time": "2026-10-15T10:00:00+08:00",
                "Tx finally commit time": "2026-10-15T10:00:20+08:00",
                "Is cross-shard tx or not": "false",
            },
        ]
        with mock.patch.object(FIGURE.stage_plotter, "resolve_run_dir", return_value=Path("/run")), mock.patch.object(
            FIGURE.stage_plotter,
            "read_rows_if_exists",
            return_value=rows,
        ):
            stages = FIGURE.clpa_transaction_stages(Path("summary.csv"), {"method": "clpa_relay"})

        self.assertEqual(15.0, stages["source_s"])
        self.assertEqual(7.5, stages["target_s"])
        self.assertNotIn("clpa_s", stages)

    def test_netting_stages_split_settlement_and_fallback(self) -> None:
        intents = [
            {
                "BatchID": "batch",
                "EndToEndLatencyNs": "10000000000",
                "ReservationLatencyNs": "2000000000",
                "UsedFallback": "false",
            },
            {
                "BatchID": "batch",
                "EndToEndLatencyNs": "12000000000",
                "ReservationLatencyNs": "2000000000",
                "UsedFallback": "true",
            },
        ]
        batches = [
            {
                "BatchID": "batch",
                "WindowOpenDurationNs": "1000000000",
                "MatchTimeNs": "1000000000",
                "MerkleBuildTimeNs": "0",
                "BeaconConsensusLatencyNs": "1000000000",
            }
        ]

        def fake_rows(path: Path) -> list[dict[str, str]]:
            if path.name == "netting_intent_metrics.csv":
                return intents
            if path.name == "netting_batch_metrics.csv":
                return batches
            return []

        empty_relay = FIGURE.stage_plotter.StageTotals(
            0, FIGURE.stage_plotter.empty_stage_totals()
        )
        with mock.patch.object(FIGURE.stage_plotter, "resolve_run_dir", return_value=Path("/run")), mock.patch.object(
            FIGURE.stage_plotter, "read_rows_if_exists", side_effect=fake_rows
        ), mock.patch.object(
            FIGURE.stage_plotter, "relay_detail_stage_totals", return_value=empty_relay
        ):
            stages = FIGURE.netting_transaction_stages(
                Path("summary.csv"), {"method": "netting_static_relay"}
            )

        self.assertEqual(2.0, stages["source_s"])
        self.assertEqual(3.0, stages["matching_s"])
        self.assertEqual(2.5, stages["settlement_s"])
        self.assertEqual(3.5, stages["fallback_s"])


if __name__ == "__main__":
    unittest.main()
