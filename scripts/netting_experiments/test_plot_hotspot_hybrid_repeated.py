#!/usr/bin/env python3

from __future__ import annotations

import csv
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import plot_hotspot_hybrid_repeated as plotter
from plot_experiments import PNGFigure


class PlotHotspotHybridRepeatedTest(unittest.TestCase):
    def test_timestamped_stem_appends_suffix_before_extensions_are_added(self) -> None:
        self.assertEqual(
            Path("figures/fig_hotspot_repeated_tps_boxplot_20261002-120000"),
            plotter.timestamped_stem(Path("figures/fig_hotspot_repeated_tps_boxplot"), "20261002-120000"),
        )

    def test_timestamped_path_appends_suffix_before_existing_extension(self) -> None:
        self.assertEqual(
            Path("figures/fig_hotspot_ctx_stage_latency_20261002-120000.png"),
            plotter.timestamped_path(Path("figures/fig_hotspot_ctx_stage_latency.png"), "20261002-120000"),
        )

    def test_resolve_timestamp_can_disable_suffix(self) -> None:
        self.assertEqual("", plotter.resolve_timestamp("none"))

    def test_intent_violin_name_uses_netting_rate(self) -> None:
        self.assertEqual("fig_hotspot_window_netting_rate_violin", plotter.netting_violin_name("intent"))

    def test_tx_latency_violin_reads_three_methods(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            summary = root / "summary.csv"
            runs = root / "runs"
            rows = [
                {"repeat": "1", "run_id": "net", "method": "netting_static_relay"},
                {"repeat": "1", "run_id": "relay", "method": "static_relay"},
                {"repeat": "1", "run_id": "broker", "method": "clpa_broker"},
            ]
            with summary.open("w", newline="", encoding="utf-8") as fp:
                writer = csv.DictWriter(fp, fieldnames=["repeat", "run_id", "method"])
                writer.writeheader()
                writer.writerows(rows)
            write_netting_run(runs / "repeat_01" / "net" / "results")
            write_detail_run(runs / "repeat_01" / "relay" / "results", "relay_stats_detail_tx_info.csv")
            write_detail_run(runs / "repeat_01" / "broker" / "results", "broker_stats_detail_tx_info.csv")

            svg = plotter.plot_tx_latency_violin(summary, rows, root / "tx_latency", 12)
            stats = root / "tx_latency_stats.csv"
            plotter.write_tx_latency_stats(rows, summary, stats, 12)

            self.assertTrue(svg.exists())
            self.assertIn("All transaction confirmation latency distribution", svg.read_text(encoding="utf-8"))
            with stats.open(newline="", encoding="utf-8") as fp:
                stat_rows = list(csv.DictReader(fp))

        self.assertEqual(["Relay", "BrokerChain", "Netting"], [row["method"] for row in stat_rows])
        self.assertEqual(["2", "2", "2"], [row["tx_count"] for row in stat_rows])

    def test_tx_latency_selected_rows_filters_repeat(self) -> None:
        rows = [
            {"repeat": "1", "run_id": "a"},
            {"repeat": "2", "run_id": "b"},
            {"repeat": "1", "run_id": "c"},
        ]

        selected = plotter.tx_latency_selected_rows(rows, 1)

        self.assertEqual(["a", "c"], [row["run_id"] for row in selected])

    def test_total_netting_rate_values_use_all_ctx_count(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            summary = root / "summary.csv"
            run_id = "net"
            rows = [
                {
                    "repeat": "1",
                    "run_id": run_id,
                    "method": "netting_static_relay",
                    "batch_count": "2",
                    "tx_number": "40",
                }
            ]
            with summary.open("w", newline="", encoding="utf-8") as fp:
                writer = csv.DictWriter(fp, fieldnames=["repeat", "run_id", "method", "batch_count", "tx_number"])
                writer.writeheader()
                writer.writerows(rows)
            batch_metrics = root / "runs" / "repeat_01" / run_id / "results" / "netting_batch_metrics.csv"
            batch_metrics.parent.mkdir(parents=True)
            batch_metrics.write_text(
                "IntentCount,FullMatchedIntentCount,FullMatchedIntentRatio,MatchedIntentRatio\n"
                "10,2,0.2,0.7\n"
                "10,8,0.8,0.9\n",
                encoding="utf-8",
            )

            values = plotter.total_netting_rate_values(summary, rows)

        self.assertEqual([0.25], values)

    def test_total_matched_netting_rate_values_count_any_matched_ctx(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            summary = root / "summary.csv"
            run_id = "net"
            rows = [
                {
                    "repeat": "1",
                    "run_id": run_id,
                    "method": "netting_static_relay",
                    "batch_count": "2",
                    "tx_number": "40",
                }
            ]
            with summary.open("w", newline="", encoding="utf-8") as fp:
                writer = csv.DictWriter(fp, fieldnames=["repeat", "run_id", "method", "batch_count", "tx_number"])
                writer.writeheader()
                writer.writerows(rows)
            batch_metrics = root / "runs" / "repeat_01" / run_id / "results" / "netting_batch_metrics.csv"
            batch_metrics.parent.mkdir(parents=True)
            batch_metrics.write_text(
                "IntentCount,MatchedIntentRatio\n"
                "10,0.7\n"
                "10,0.9\n",
                encoding="utf-8",
            )

            values = plotter.total_matched_netting_rate_values(summary, rows)

        self.assertEqual([0.4], values)

    def test_stacked_bar_legend_can_match_visible_top_to_bottom_order(self) -> None:
        chart = CapturingFigure()

        chart.stacked_bar_panel(
            0,
            0,
            60,
            60,
            ["Sample"],
            [[1.0, 2.0, 3.0]],
            ["Bottom", "Middle", "Top"],
            ["#111111", "#222222", "#333333"],
            "Latency",
            90,
            12,
            legend_top_to_bottom=True,
        )

        self.assertEqual(["#333333", "#222222", "#111111"], chart.legend_colors)
        self.assertEqual(["Top", "Middle", "Bottom"], chart.legend_labels)

    def test_display_stage_stack_merges_hidden_stages(self) -> None:
        stack = {
            "stage_clpa_s": 1.0,
            "stage_source_s": 2.0,
            "stage_window_s": 3.0,
            "stage_match_s": 4.0,
            "stage_beacon_s": 5.0,
            "stage_coordination_s": 6.0,
            "stage_transfer_s": 7.0,
            "stage_target_s": 8.0,
            "stage_settlement_s": 9.0,
            "stage_fallback_s": 10.0,
        }

        display = plotter.display_stage_stack(stack)

        self.assertEqual(3.0, display["display_source_s"])
        self.assertEqual(12.0, display["display_netting_s"])
        self.assertEqual(6.0, display["stage_coordination_s"])
        self.assertEqual(15.0, display["display_target_s"])
        self.assertEqual(19.0, display["display_settlement_s"])

    def test_stage_groups_use_all_completed_transactions(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            summary = root / "summary.csv"
            rows = [
                {"repeat": "1", "run_id": "relay", "method": "static_relay", "stage_clpa_s": "0"},
                {"repeat": "1", "run_id": "net", "method": "netting_static_relay", "stage_clpa_s": "0"},
            ]
            with summary.open("w", newline="", encoding="utf-8") as fp:
                writer = csv.DictWriter(fp, fieldnames=["repeat", "run_id", "method", "stage_clpa_s"])
                writer.writeheader()
                writer.writerows(rows)

            relay_results = root / "runs" / "repeat_01" / "relay" / "results"
            relay_results.mkdir(parents=True)
            write_relay_detail(
                relay_results / "relay_stats_detail_tx_info.csv",
                [
                    {
                        "Tx create time": "2026-01-01T00:00:00+00:00",
                        "Tx finally commit time": "2026-01-01T00:00:04+00:00",
                        "Is cross-shard tx or not": "false",
                        "Inner shard tx block propose time": "",
                    },
                    {
                        "Tx create time": "2026-01-01T00:00:00+00:00",
                        "Tx finally commit time": "2026-01-01T00:00:06+00:00",
                        "Is cross-shard tx or not": "true",
                        "Relay1 block propose time": "",
                        "Relay1 tx commit time": "",
                        "Relay2 tx create time": "",
                        "Relay2 block propose time": "",
                        "Relay2 tx commit time": "",
                    },
                ],
            )

            net_results = root / "runs" / "repeat_01" / "net" / "results"
            net_results.mkdir(parents=True)
            write_relay_detail(
                net_results / "relay_stats_detail_tx_info.csv",
                [
                    {
                        "Tx create time": "2026-01-01T00:00:00+00:00",
                        "Tx finally commit time": "2026-01-01T00:00:02+00:00",
                        "Is cross-shard tx or not": "false",
                        "Inner shard tx block propose time": "",
                    }
                ],
            )
            with (net_results / "netting_intent_metrics.csv").open("w", newline="", encoding="utf-8") as fp:
                writer = csv.DictWriter(fp, fieldnames=["BatchID", "EndToEndLatencyNs", "ReservationLatencyNs", "UsedFallback"])
                writer.writeheader()
                writer.writerow(
                    {
                        "BatchID": "batch-1",
                        "EndToEndLatencyNs": "10000000000",
                        "ReservationLatencyNs": "3000000000",
                        "UsedFallback": "false",
                    }
                )

            groups = dict(plotter.stage_groups(summary, rows))

        self.assertAlmostEqual(5.0, sum(groups["Relay"]))
        self.assertAlmostEqual(6.0, sum(groups["Netting"]))


class CapturingFigure(PNGFigure):
    def __init__(self) -> None:
        super().__init__(180, 120)
        self.legend_colors: list[str] = []
        self.legend_labels: list[str] = []

    def rect(self, x: float, y: float, w: float, h: float, color: str) -> None:
        if int(round(x)) == 90:
            self.legend_colors.append(color)

    def draw_text(
        self,
        text: str,
        x: float,
        y: float,
        scale: int = 1,
        color: str = "#000000",
        anchor: str = "left",
        rotate_left: bool = False,
    ) -> None:
        if int(round(x)) == 118:
            self.legend_labels.append(text)


def write_netting_run(results: Path) -> None:
    results.mkdir(parents=True)
    with (results / "netting_intent_metrics.csv").open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=["EndToEndLatencyNs"])
        writer.writeheader()
        writer.writerow({"EndToEndLatencyNs": "1000000000"})
        writer.writerow({"EndToEndLatencyNs": "3000000000"})


def write_detail_run(results: Path, filename: str) -> None:
    results.mkdir(parents=True)
    with (results / filename).open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=["Tx create time", "Tx finally commit time"])
        writer.writeheader()
        writer.writerow({"Tx create time": "2026-01-01T00:00:00+00:00", "Tx finally commit time": "2026-01-01T00:00:02+00:00"})
        writer.writerow({"Tx create time": "2026-01-01T00:00:01+00:00", "Tx finally commit time": "2026-01-01T00:00:05+00:00"})


def write_relay_detail(path: Path, rows: list[dict[str, str]]) -> None:
    fieldnames = [
        "OriginalHash",
        "Tx create time",
        "Tx finally commit time",
        "Is cross-shard tx or not",
        "Inner shard tx block propose time",
        "Relay1 block propose time",
        "Relay1 tx commit time",
        "Relay2 tx create time",
        "Relay2 block propose time",
        "Relay2 tx commit time",
    ]
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=fieldnames)
        writer.writeheader()
        for idx, row in enumerate(rows):
            complete = {field: "" for field in fieldnames}
            complete["OriginalHash"] = f"tx-{idx}"
            complete.update(row)
            writer.writerow(complete)


if __name__ == "__main__":
    unittest.main()
