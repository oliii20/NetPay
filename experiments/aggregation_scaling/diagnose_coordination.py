#!/usr/bin/env python3
"""Read per-batch timing; distinguish CTX-weighted costs from total dispatch work."""
import argparse
import csv
from pathlib import Path


def summarize(path):
    with path.open(newline='') as source:
        rows = list(csv.DictReader(source))
    if not rows or 'BuildTimeNs' not in rows[0]:
        raise ValueError(f'{path}: new timing columns missing; rerun using the updated binary')
    count = sum(int(r['ReceiptCount']) for r in rows)
    timing_count = sum(int(r['ReceiptTimingCount']) for r in rows)
    result = dict(batches=len(rows), ctx_count=count, receipt_timing_coverage=timing_count/count)
    for column in ('SourceToHandlerMeanNs', 'HandlerToBuildMeanNs', 'BuildTimeNs', 'LocalValidationTimeNs', 'BeaconConsensusLatencyNs', 'FrozenQueueWaitNs'):
        weight = 'ReceiptTimingCount' if column in ('SourceToHandlerMeanNs', 'HandlerToBuildMeanNs') else 'ReceiptCount'
        denominator = timing_count if weight == 'ReceiptTimingCount' else count
        result[column.removesuffix('Ns') + 'WeightedSeconds'] = sum(int(r[column])*int(r[weight]) for r in rows)/denominator/1e9 if denominator else ''
    for column in ('SupervisorNotifyTimeNs', 'HeaderBroadcastTimeNs', 'PackageDispatchTimeNs', 'CommitDeliveryTimeNs'):
        result[column.removesuffix('Ns') + 'TotalSeconds'] = sum(int(r[column]) for r in rows)/1e9
    result['max_batch_proposal_MB'] = max(int(r['BatchProposalBytes']) for r in rows)/1e6
    result['max_frozen_queue_including_current'] = max(int(r['FrozenWindowQueueLength']) for r in rows)
    return result


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--input', type=Path, required=True)
    args = parser.parse_args()
    results = []
    for path in sorted(args.input.glob('shards-*/repeat-*/*/results/aggregation_batch_metrics.csv')):
        row = dict(run=str(path.parent.parent.relative_to(args.input)), **summarize(path))
        results.append(row)
        print(f"{row['run']}: ingress={row['SourceToHandlerMeanWeightedSeconds']:.3f}s, handler→build={row['HandlerToBuildMeanWeightedSeconds']:.3f}s, build={row['BuildTimeWeightedSeconds']:.3f}s, local validation={row['LocalValidationTimeWeightedSeconds']:.3f}s, consensus={row['BeaconConsensusLatencyWeightedSeconds']:.3f}s; total header dispatch={row['HeaderBroadcastTimeTotalSeconds']:.3f}s")
    if not results:
        raise ValueError('no batch metrics found')
    with (args.input/'coordination_diagnostics.csv').open('w', newline='') as target:
        writer = csv.DictWriter(target, fieldnames=list(results[0]))
        writer.writeheader()
        writer.writerows(results)
