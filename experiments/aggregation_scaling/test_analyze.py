import csv
import tempfile
import unittest
from pathlib import Path
import analyze


def write(path, rows):
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open('w', newline='') as f:
        w = csv.DictWriter(f, fieldnames=list(rows[0]))
        w.writeheader()
        w.writerows(rows)


class ScalingTest(unittest.TestCase):
    def test_stage_partition(self):
        with tempfile.TemporaryDirectory() as tmp:
            run = Path(tmp)
            write(run/'results/relay_stats_detail_tx_info.csv', [{'Is cross-shard tx or not': 'false'}])
            write(run/'results/aggregation_execution_metrics.csv', [dict(Phase='source_debit', PaymentID='p', CommittedAtUnixNano=2_000_000_000)])
            write(run/'results/aggregation_batch_metrics.csv', [dict(BatchID='b', BeaconFinalizedAtUnixNano=4_000_000_000)])
            write(run/'results/aggregation_tx_lifecycle.csv', [dict(PaymentID='p', BatchID='b', CreatedAtUnixNano=1_000_000_000, FinalCommittedAtUnixNano=7_000_000_000)])
            result = analyze.stages(run, 'full_aggregation')
            self.assertEqual(result, dict(source_ms=1000, coordination_ms=2000, destination_ms=3000))
            write(run/'results/aggregation_batch_metrics.csv', [dict(BatchID='b', BeaconFinalizedAtUnixNano=8_000_000_000)])
            with self.assertRaises(ValueError):
                analyze.stages(run, 'full_aggregation')

    def test_asynchronous_pool_alignment(self):
        with tempfile.TemporaryDirectory() as tmp:
            run = Path(tmp)
            for shard, stamps, counts in [(0, ['00', '01', '02'], [2, 5, 0]), (1, ['00', '01.5', '02.5'], [3, 7, 0])]:
                write(run/f'block_record/shard={shard}_node=0/txpool_record.csv', [dict(Timestamp=f'1970-01-01T00:00:{s}Z', TxCount=c) for s,c in zip(stamps, counts)])
            rows = analyze.pools(run, 2, 0, 2_000_000_000)
            self.assertEqual([r['total_entries'] for r in rows], [5, 8, 7])
            with self.assertRaises(FileNotFoundError):
                analyze.pools(run, 3, 0, 2_000_000_000)


if __name__ == '__main__':
    unittest.main()
