"""Check lifecycle conservation and asynchronous pool alignment."""

import unittest

import numpy as np

from gen_fig_aggregation_diagnostics import aligned_pool, stage_partition


class DiagnosticsTest(unittest.TestCase):
    def test_stage_intervals_conserve_end_to_end_latency(self):
        for name, times, expected in (
            ("aggregation", (1, 3, 7, 12), [2, 4, 5]),
            ("relay", (1, 3, 3, 12), [2, 0, 9]),
            ("inner", (1, 12, 12, 12), [11, 0, 0]),
        ):
            with self.subTest(name=name):
                stages = stage_partition(*(value * 10**9 for value in times))
                np.testing.assert_allclose(stages, expected)
                self.assertAlmostEqual(stages.sum(), times[-1] - times[0])

    def test_invalid_stage_order_is_rejected(self):
        for times in ((1, 0, 2, 3), (0, 2, 1, 3), (0, 1, 3, 2)):
            with self.subTest(times=times), self.assertRaises(ValueError):
                stage_partition(*times)

    def test_pool_alignment_uses_previous_timestamp_per_shard(self):
        series = [
            (np.array([0, 10, 20]), np.array([2, 5, 0])),
            (np.array([0, 15, 25]), np.array([3, 7, 0])),
        ]
        np.testing.assert_array_equal(aligned_pool(series, np.array([0, 10, 20])), [5, 8, 7])

    def test_pool_missing_observation_coverage_is_rejected(self):
        series = [(np.array([1, 2]), np.array([2, 3]))]
        for grid in (np.array([0, 1]), np.array([1, 3])):
            with self.subTest(grid=grid), self.assertRaises(ValueError):
                aligned_pool(series, grid)

    def test_pool_negative_counts_and_unsorted_times_are_rejected(self):
        for times, counts in (([0, 2, 1, 3], [0, 1, 2, 0]), ([0, 3], [-1, 0])):
            with self.subTest(times=times, counts=counts), self.assertRaises(ValueError):
                aligned_pool([(np.array(times), np.array(counts))], np.array([0, 3]))


if __name__ == "__main__":
    unittest.main()
