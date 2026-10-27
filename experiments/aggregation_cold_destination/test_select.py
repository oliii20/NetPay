import unittest

from select_destinations import select


class SelectionTest(unittest.TestCase):
    def setUp(self):
        self.report = {
            'shards': 4, 'cross_shard': 500, 'sha256': 'abc', 'transactions': 1000,
            'ranking': [
                {'shard': 2, 'ctx': 250}, {'shard': 0, 'ctx': 125},
                {'shard': 1, 'ctx': 100}, {'shard': 3, 'ctx': 25},
            ],
        }

    def test_selects_by_capacity_utilization(self):
        # 1,000 tx/s injection and 1,000 tx/s shard capacity make the
        # per-shard dataset fraction equal to capacity utilization.
        result = select(self.report, 10, 1000, 2000, 2000)
        self.assertEqual(result['destination_shards'], [0, 1, 2])
        self.assertEqual(result['cold_shards'], [3])
        self.assertEqual(result['covered_ctx'], 475)
        self.assertEqual(result['coverage'], .95)
        self.assertEqual(result['selection_class'], 'partial_aggregation')

    def test_boundary_is_aggregated(self):
        result = select(self.report, 10, 1000, 2000, 2000)
        shard = next(row for row in result['shards'] if row['shard'] == 1)
        self.assertEqual(shard['capacity_utilization'], .1)
        self.assertTrue(shard['aggregate'])

    def test_injection_speed_changes_selection(self):
        slow = select(self.report, 20, 500, 2000, 2000)
        fast = select(self.report, 20, 2000, 2000, 2000)
        self.assertEqual(slow['destination_shards'], [])
        self.assertEqual(slow['selection_class'], 'relay_equivalent')
        self.assertEqual(fast['destination_shards'], [0, 1, 2])

    def test_rejects_invalid_parameters(self):
        for threshold in (0, -1, 101):
            with self.assertRaises(ValueError):
                select(self.report, threshold, 1000, 2000, 2000)
        for values in ((0, 2000, 2000), (1000, 0, 2000), (1000, 2000, 0)):
            with self.assertRaises(ValueError):
                select(self.report, 10, *values)

    def test_rejects_inconsistent_ranking(self):
        invalid = dict(self.report, cross_shard=501)
        with self.assertRaisesRegex(ValueError, 'cross-shard total'):
            select(invalid, 10, 1000, 2000, 2000)


if __name__ == '__main__':
    unittest.main()
