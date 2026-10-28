import unittest

import gen_fig_newcode_scaling as figures


class StagePartitionTest(unittest.TestCase):
    def test_routes_share_end_to_end_accounting(self):
        second = 1_000_000_000
        for source, beacon, end in ((2, 2, 5), (2, 4, 5), (5, 5, 5)):
            with self.subTest(source=source, beacon=beacon):
                stages = figures.partition(
                    second, source * second, beacon * second, end * second
                )
                self.assertEqual(sum(stages), end - 1)
                self.assertTrue(all(value >= 0 for value in stages))

    def test_rejects_invalid_lifecycle_boundary(self):
        with self.assertRaises(ValueError):
            figures.partition(10, 20, 15, 30)


if __name__ == "__main__":
    unittest.main()
