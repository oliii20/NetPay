#!/usr/bin/env python3

from __future__ import annotations

import csv
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import run_hotspot_hybrid_experiment as hotspot


class HotspotHybridExperimentTest(unittest.TestCase):
    def test_allocate_base_port_wraps_after_occupied_high_range(self) -> None:
        original = hotspot.ports_are_available
        checked: list[int] = []

        def fake_ports_are_available(start: int, count: int) -> bool:
            self.assertEqual(33, count)
            checked.append(start)
            return start == 26000

        hotspot.ports_are_available = fake_ports_are_available
        try:
            base_port = hotspot.allocate_base_port(64400, 26000, 1200, 33)
        finally:
            hotspot.ports_are_available = original

        self.assertEqual(26000, base_port)
        self.assertEqual([64400, 26000], checked)

    def test_allocate_base_port_rejects_base_too_close_to_limit(self) -> None:
        with self.assertRaises(SystemExit):
            hotspot.allocate_base_port(65520, 65520, 1200, 33)

    def test_read_workload_rows_keeps_rows_beyond_requested_tx_count(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "workload.csv"
            with path.open("w", newline="", encoding="utf-8") as fp:
                writer = csv.writer(fp)
                writer.writerow([f"col{i}" for i in range(13)])
                for idx in range(5):
                    writer.writerow(
                        [
                            "",
                            "",
                            "",
                            f"0x{idx + 1:040x}",
                            f"0x{idx + 101:040x}",
                            "",
                            "",
                            "",
                            "1",
                            "",
                            "",
                            "",
                            "",
                        ]
                    )

            rows = hotspot.read_workload_rows(path, 3)

        self.assertEqual(5, len(rows))


if __name__ == "__main__":
    unittest.main()
