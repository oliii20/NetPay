import csv
import tempfile
import unittest
from pathlib import Path
from diagnose_coordination import summarize


class DiagnosticsTest(unittest.TestCase):
    def test_weighted_latency_and_total_dispatch_are_distinct(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp)/'batch.csv'
            columns = ['ReceiptCount', 'ReceiptTimingCount', 'SourceToHandlerMeanNs', 'HandlerToBuildMeanNs', 'BuildTimeNs', 'LocalValidationTimeNs', 'BeaconConsensusLatencyNs', 'FrozenQueueWaitNs', 'SupervisorNotifyTimeNs', 'HeaderBroadcastTimeNs', 'PackageDispatchTimeNs', 'CommitDeliveryTimeNs', 'BatchProposalBytes', 'FrozenWindowQueueLength']
            with path.open('w', newline='') as target:
                writer = csv.DictWriter(target, fieldnames=columns)
                writer.writeheader()
                for count, seconds in [(1, 1), (3, 3)]:
                    row = dict.fromkeys(columns, seconds*1_000_000_000)
                    row.update(ReceiptCount=count, ReceiptTimingCount=count, FrozenWindowQueueLength=1)
                    writer.writerow(row)
            result = summarize(path)
            self.assertEqual(result['SourceToHandlerMeanWeightedSeconds'], 2.5)
            self.assertEqual(result['HeaderBroadcastTimeTotalSeconds'], 4.)
            self.assertEqual(result['receipt_timing_coverage'], 1.)


if __name__ == '__main__':
    unittest.main()
