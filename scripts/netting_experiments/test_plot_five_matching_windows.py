#!/usr/bin/env python3

from __future__ import annotations

import csv
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import plot_five_matching_windows as plotter


class PlotFiveMatchingWindowsTest(unittest.TestCase):
    def test_select_runs_locates_plain_session_batch_metrics(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            run_id = "exp1_baseline_netting_static_relay_method_netting_static_relay_seed1"
            summary = root / "summary.csv"
            write_summary(summary, [{"run_id": run_id, "method": "netting_static_relay", "batch_count": "2"}])
            batch_metrics = root / "runs" / run_id / "results" / "netting_batch_metrics.csv"
            batch_metrics.parent.mkdir(parents=True)
            batch_metrics.write_text("WindowID,MatchedIntentRatio,IntentCount\n1,0.5,10\n", encoding="utf-8")

            selected = plotter.select_runs(summary, 5)

        self.assertEqual(run_id, selected[0].run_id)
        self.assertEqual("run_01", selected[0].label)

    def test_select_runs_locates_repeated_session_batch_metrics(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            run_id = "exp1_baseline_netting_static_relay_method_netting_static_relay_seed1"
            summary = root / "summary.csv"
            write_summary(
                summary,
                [{"repeat": "3", "run_id": run_id, "method": "netting_static_relay", "batch_count": "2"}],
            )
            batch_metrics = root / "runs" / "repeat_03" / run_id / "results" / "netting_batch_metrics.csv"
            batch_metrics.parent.mkdir(parents=True)
            batch_metrics.write_text("WindowID,MatchedIntentRatio,IntentCount\n1,0.5,10\n", encoding="utf-8")

            selected = plotter.select_runs(summary, 5)

        self.assertEqual("repeat_03", selected[0].label)

    def test_select_runs_honors_run_ids_filter(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            summary = root / "summary.csv"
            rows = [
                {"run_id": "run_a", "method": "netting_static_relay", "batch_count": "2"},
                {"run_id": "run_b", "method": "netting_static_relay", "batch_count": "2"},
            ]
            write_summary(summary, rows)
            for run_id in ["run_a", "run_b"]:
                path = root / "runs" / run_id / "results" / "netting_batch_metrics.csv"
                path.parent.mkdir(parents=True)
                path.write_text("WindowID,MatchedIntentRatio,IntentCount\n1,0.5,10\n", encoding="utf-8")

            selected = plotter.select_runs(summary, 5, ["run_b"])

        self.assertEqual(["run_b"], [item.run_id for item in selected])


def write_summary(path: Path, rows: list[dict[str, str]]) -> None:
    fieldnames = sorted({key for row in rows for key in row})
    with path.open("w", newline="", encoding="utf-8") as fp:
        writer = csv.DictWriter(fp, fieldnames=fieldnames)
        writer.writeheader()
        writer.writerows(rows)


if __name__ == "__main__":
    unittest.main()
