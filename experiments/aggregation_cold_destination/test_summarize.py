import csv
import json
import tempfile
import unittest
from pathlib import Path

import summarize


class SummarizeTest(unittest.TestCase):
    def test_joins_selection_and_orders_methods(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            rows = [
                {'shards': 4, 'repeat': 'repeat-01', 'method': 'full_aggregation'},
                {'shards': 4, 'repeat': 'repeat-01', 'method': 'cold_exclusion_20'},
                {'shards': 4, 'repeat': 'repeat-01', 'method': 'relay'},
            ]
            with (root / 'scaling_runs.csv').open('w', newline='') as target:
                writer = csv.DictWriter(target, fieldnames=list(rows[0]))
                writer.writeheader()
                writer.writerows(rows)
            run = root / 'shards-4/repeat-01/cold_exclusion_20'
            run.mkdir(parents=True)
            (run / 'selection.json').write_text(json.dumps({
                'selected_shard_count': 2, 'cold_shards': [1, 3], 'coverage': .8,
            }))

            result = summarize.collect(root)
            self.assertEqual([row['method'] for row in result], [
                'relay', 'cold_exclusion_20', 'full_aggregation',
            ])
            self.assertEqual(result[1]['selected_shards'], 2)
            self.assertEqual(result[1]['cold_shards'], 2)
            self.assertEqual(result[1]['selected_ctx_fraction'], .8)


if __name__ == '__main__':
    unittest.main()
