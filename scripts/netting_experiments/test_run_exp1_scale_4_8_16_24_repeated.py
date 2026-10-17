from __future__ import annotations

import argparse
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))

import run_exp1_scale_4_8_16_24_repeated as scale_repeated


class RunExp1Scale481624RepeatedTest(unittest.TestCase):
    def test_parse_shard_nums_accepts_all_configured_scales(self) -> None:
        self.assertEqual({4, 8, 16, 24}, scale_repeated.parse_shard_nums("4,8,16,24"))

    def test_parse_shard_nums_rejects_unsupported_scale(self) -> None:
        with self.assertRaisesRegex(SystemExit, "unsupported"):
            scale_repeated.parse_shard_nums("4,32")

    def test_build_profiles_preserves_old_profiles_and_adds_shard_24(self) -> None:
        args = argparse.Namespace(
            tx_speed_24=24000,
            max_window_ms_24=2000,
            pair_rate_threshold_24=0.96,
        )
        pair_rate_file = Path("pair_matching_rate_ranking.csv")

        profiles = scale_repeated.build_profiles(args, pair_rate_file)

        self.assertEqual(
            [
                (4, 3000, 1500, 0.0, ""),
                (8, 6000, 1500, 0.0, ""),
                (16, 12000, 1500, 0.0, ""),
                (24, 24000, 2000, 0.96, str(pair_rate_file)),
            ],
            [
                (
                    profile.shard_num,
                    profile.tx_speed,
                    profile.max_window_ms,
                    profile.pair_rate_threshold,
                    profile.pair_rate_file,
                )
                for profile in profiles
            ],
        )

    def test_build_specs_uses_four_independent_methods(self) -> None:
        args = argparse.Namespace(
            tx_number=300000,
            node_num=4,
            block_limit=2000,
            batch_size=0,
            settlement_chunk_size=500,
            block_interval_ms=2000,
            beacon_block_interval_ms=500,
            network_latency_ms=0,
        )
        profile = scale_repeated.repeated.ShardProfile(
            24,
            24000,
            2000,
            pair_rate_threshold=0.96,
            pair_rate_file="pair_rates.csv",
        )

        specs = scale_repeated.build_specs(args, [profile], [1])

        self.assertEqual(
            [
                ("netting_static_relay", "static_relay", True),
                ("static_relay", "static_relay", False),
                ("static_broker", "static_broker", False),
                ("clpa_relay", "clpa_relay", False),
            ],
            [(spec.method, spec.consensus_type, spec.netting_enabled) for spec in specs],
        )
        self.assertEqual("pair_rates.csv", specs[0].pair_rate_file)
        self.assertTrue(all(not spec.pair_rate_file for spec in specs[1:]))


if __name__ == "__main__":
    unittest.main()
