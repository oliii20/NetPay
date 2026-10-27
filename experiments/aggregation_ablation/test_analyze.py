import csv
import tempfile
import unittest
from pathlib import Path
import analyze


def write(path, headers, rows):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open('w', newline='') as f:
        writer = csv.writer(f)
        writer.writerow(headers)
        writer.writerows(rows)


class AnalyzeTest(unittest.TestCase):
    def test_counts_and_latency(self):
        for method in analyze.METHODS:
            with self.subTest(method=method), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                relay_rows = [['1970-01-01T00:00:01Z', '1970-01-01T00:00:03Z', 'true']] if method == 'relay' else []
                write(root/'results/relay_stats_detail_tx_info.csv',
                      ['Tx create time','Tx finally commit time','Is cross-shard tx or not'], relay_rows)
                write(root/'block_record/shard=0_node=0/block_record.csv',
                      ['TxBodyLen','Relay1TxCount','Relay2TxCount','CrossShardPaymentTxCount','AggregationSettlementTxCount'],
                      [[2, 1, 1, 0, 0] if method == 'relay' else [2, 0, 0, 1, 1]])
                if method != 'relay':
                    write(root/'results/aggregation_tx_lifecycle.csv',
                          ['CreatedAtUnixNano','FinalCommittedAtUnixNano'], [[1000000000,3000000000]])
                    write(root/'results/aggregation_execution_metrics.csv',
                          ['Phase','ReceiverUpdateCount'], [['source_debit',0],['settlement',1]])
                row = analyze.summarize(root, method, 1)
                self.assertEqual(row['ctx_tps'], .5)
                self.assertEqual(row['ctx_latency_p95_ms'], 2000)
                self.assertEqual(row['destination_entries'], 1)
                self.assertEqual(row['receiver_updates'], 1)
                with self.assertRaises(ValueError):
                    analyze.summarize(root, method, 2)

class MethodCompatibilityTest(unittest.TestCase):
    def test_legacy_and_four_method_detection(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root/'repeat-01/relay').mkdir(parents=True)
            self.assertEqual(analyze.methods_for(root), analyze.LEGACY_METHODS)
            (root/'repeat-01/aggregation_only').mkdir()
            self.assertEqual(analyze.methods_for(root), analyze.METHODS)
            self.assertEqual(analyze.methods_for(root.parent / 'nonexistent-method-test'), analyze.LEGACY_METHODS)

    def test_selective_method_manifest(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root/'methods.txt').write_text(
                'relay\nfull_aggregation\nhot_destination_75\ncold_exclusion_20\n'
            )
            self.assertEqual(
                analyze.methods_for(root),
                ('relay', 'full_aggregation', 'hot_destination_75', 'cold_exclusion_20'),
            )
            (root/'methods.txt').write_text('relay\ncold_exclusion_invalid\n')
            with self.assertRaises(ValueError):
                analyze.methods_for(root)


if __name__ == '__main__':
    unittest.main()
