import unittest

import numpy as np

from gen_fig_repeated_scaling import MODES, SHARDS, mean_sd, pool_average, validate_grid


class RepeatedStatisticsTest(unittest.TestCase):
    def test_four_method_suite_requires_exactly_80_runs(self):
        modes = tuple(m for m in MODES if m != "clpa")
        cases = [
            dict(shards=s, mode=m, repeat=r)
            for s in SHARDS
            for m in modes
            for r in range(1, 6)
        ]
        self.assertEqual(len(cases), 80)
        validate_grid(cases, 5, modes)
        with self.assertRaises(ValueError):
            validate_grid(cases + [dict(shards=4, mode="clpa", repeat=1)], 5, modes)

    def test_each_configuration_requires_all_five_distinct_repeats(self):
        cases = [
            dict(shards=s, mode=m, repeat=r)
            for s in SHARDS
            for m in MODES
            for r in range(1, 6)
        ]
        validate_grid(cases, 5)
        with self.assertRaises(ValueError):
            validate_grid(cases[:-1], 5)
        with self.assertRaises(ValueError):
            validate_grid(cases[:-1] + [cases[0]], 5)

    def test_standard_deviation_is_between_runs_sample_sd(self):
        mean, sd = mean_sd([1, 2, 3, 4, 5])
        self.assertEqual(mean, 3)
        self.assertAlmostEqual(sd, np.sqrt(2.5))

    def test_pool_alignment_uses_all_runs_without_zero_padding(self):
        runs = [
            dict(time=np.array([0.0, 0.5, 1.0]), pool=np.array([2, 4, 6])),
            dict(time=np.array([0.0, 0.6, 1.2, 1.5]), pool=np.array([4, 8, 12, 14])),
        ]
        times, mean, sd = pool_average(runs)
        np.testing.assert_array_equal(times, [0.0, 0.5, 1.0])
        np.testing.assert_array_equal(mean, [3.0, 4.0, 7.0])
        np.testing.assert_allclose(sd, [np.sqrt(2), 0.0, np.sqrt(2)])


if __name__ == "__main__":
    unittest.main()
