#!/usr/bin/env python3
"""Unit tests for plot_matching_windows.py."""

from __future__ import annotations

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

from plot_matching_windows import resolve_output_path, weighted_average, window_rates


class PlotMatchingWindowsTest(unittest.TestCase):
    def test_window_rates_sort_by_window_id(self) -> None:
        rows = [
            {"WindowID": "2", "MatchedIntentRatio": "0.25", "IntentCount": "2"},
            {"WindowID": "1", "MatchedIntentRatio": "0.75", "IntentCount": "6"},
        ]

        windows = window_rates(rows, "MatchedIntentRatio")

        self.assertEqual([1, 2], [item.window_id for item in windows])
        self.assertEqual([0.75, 0.25], [item.rate for item in windows])

    def test_weighted_average_uses_intent_count(self) -> None:
        windows = window_rates(
            [
                {"WindowID": "1", "MatchedIntentRatio": "1.0", "IntentCount": "1"},
                {"WindowID": "2", "MatchedIntentRatio": "0.0", "IntentCount": "3"},
            ],
            "MatchedIntentRatio",
        )

        self.assertAlmostEqual(0.25, weighted_average(windows))

    def test_resolve_output_path_supports_directory_and_file(self) -> None:
        self.assertEqual(
            Path("figures/netting/fig_window_matching_run42_intent_20260914-151500.png"),
            resolve_output_path(Path("figures/netting"), "run42", "intent", "20260914-151500"),
        )
        self.assertEqual(
            Path("custom_20260914-151500.png"),
            resolve_output_path(Path("custom.png"), "run42", "intent", "20260914-151500"),
        )


if __name__ == "__main__":
    unittest.main()
