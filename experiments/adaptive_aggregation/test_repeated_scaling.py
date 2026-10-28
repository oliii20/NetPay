import argparse
from pathlib import Path
import unittest

from run_repeated_scaling import commands


class RepeatedPresetTest(unittest.TestCase):
    def test_preserves_requested_conditions_and_same_suite_build(self):
        a = argparse.Namespace(
            tx_number=300000,
            dataset=None,
            output=Path("/tmp/suite-test"),
            timeout=3600,
            port_base=42000,
            dry_run=False,
        )
        run, plot = commands(a)
        for flag, value in [
            ("--methods", "adaptive,full,monoxide,brokerchain,clpa"),
            ("--shard-counts", "4,8,16,24"),
            ("--per-shard-rate", "700"),
            ("--block-limit", "2000"),
            ("--repeats", "5"),
            ("--broker-hot", "0"),
        ]:
            self.assertEqual(run[run.index(flag) + 1], value)
        self.assertNotIn("--bins", run)
        self.assertEqual(plot[plot.index("--repeats") + 1], "5")
        self.assertIn(str(a.output.resolve()), plot)

    def test_dry_run_and_explicit_dataset(self):
        a = argparse.Namespace(
            tx_number=1000000,
            dataset=Path("/tmp/data.csv"),
            output=Path("/tmp/suite-test"),
            timeout=3600,
            port_base=42000,
            dry_run=True,
        )
        run, _ = commands(a)
        self.assertIn("--dry-run", run)
        self.assertEqual(run[run.index("--dataset") + 1], str(a.dataset.resolve()))

    def test_without_clpa_uses_same_four_methods_for_run_and_plot(self):
        a = argparse.Namespace(
            tx_number=1000000,
            dataset=None,
            output=Path("/tmp/suite-test"),
            timeout=3600,
            port_base=42000,
            dry_run=False,
            methods="adaptive,full,monoxide,brokerchain",
        )
        run, plot = commands(a)
        self.assertEqual(run.count("--methods"), 1)
        self.assertEqual(run[run.index("--methods") + 1], a.methods)
        self.assertEqual(plot[plot.index("--methods") + 1], a.methods)


if __name__ == "__main__":
    unittest.main()
