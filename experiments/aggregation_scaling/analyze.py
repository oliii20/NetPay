#!/usr/bin/env python3
"""Collect complete runs, CTX stage means and time-aligned leader pool samples."""
import argparse
import csv
import importlib.util
import statistics
from bisect import bisect_right
from pathlib import Path

spec = importlib.util.spec_from_file_location('ablation', Path(__file__).resolve().parents[1] / 'aggregation_ablation/analyze.py')
ablation = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ablation)
parse_time = ablation.comparison.parse_time


def read(path):
    with path.open(newline='') as f:
        return list(csv.DictReader(f))


def write(path, rows):
    if not rows:
        raise ValueError(f'no data for {path}')
    with path.open('w', newline='') as f:
        writer = csv.DictWriter(f, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)


def stages(run, method):
    values = []
    for row in read(run / 'results/relay_stats_detail_tx_info.csv'):
        if row['Is cross-shard tx or not'].lower() != 'true':
            continue
        start = parse_time(row['Tx create time'])
        source = parse_time(row['Relay1 tx commit time'])
        end = parse_time(row['Tx finally commit time'])
        values.append(((source-start)/1e6, 0., (end-source)/1e6))
    if method != 'relay':
        sources = {r['PaymentID']: int(r['CommittedAtUnixNano']) for r in read(run / 'results/aggregation_execution_metrics.csv') if r['Phase'] == 'source_debit'}
        batches = {r['BatchID']: int(r['BeaconFinalizedAtUnixNano']) for r in read(run / 'results/aggregation_batch_metrics.csv')}
        for r in read(run / 'results/aggregation_tx_lifecycle.csv'):
            start, end = int(r['CreatedAtUnixNano']), int(r['FinalCommittedAtUnixNano'])
            source, beacon = sources[r['PaymentID']], batches[r['BatchID']]
            if not start <= source <= beacon <= end:
                raise ValueError(f'invalid stage timestamps: {run}, {r["PaymentID"]}')
            values.append(((source-start)/1e6, (beacon-source)/1e6, (end-beacon)/1e6))
    return dict(zip(('source_ms', 'coordination_ms', 'destination_ms'), map(statistics.fmean, zip(*values))))


def pools(run, shards, start, end):
    series = []
    for shard in range(shards):
        path = run / f'block_record/shard={shard}_node=0/txpool_record.csv'
        records = sorted((parse_time(r['Timestamp']), int(r['TxCount'])) for r in read(path))
        if not records or records[0][0] > start or records[-1][0] < end - 2_000_000_000:
            raise ValueError(f'missing pool coverage: {path}')
        series.append(([r[0] for r in records], [r[1] for r in records]))
    rows = []
    # Previous sample on a common wall-clock grid; never zip asynchronous samples.
    for t in range(start, end + 1, 1_000_000_000):
        counts = [counts[bisect_right(times, t)-1] for times, counts in series]
        rows.append(dict(time_s=(t-start)/1e9, total_entries=sum(counts), max_shard_entries=max(counts)))
    return rows


def collect(root, expected):
    runs, stage_rows, pool_rows = [], [], []
    for group in sorted(root.glob('shards-*'), key=lambda p: int(p.name.split('-')[1])):
        shards = int(group.name.split('-')[1])
        for repeat in sorted(group.glob('repeat-*')):
            for method in ablation.methods_for(group):
                run = repeat / method
                keys = dict(shards=shards, repeat=repeat.name, method=method)
                summary = ablation.summarize(run, method, expected)
                runs.append({**keys, **summary})
                stage_rows.append({**keys, **stages(run, method)})
                logical = ablation.comparison.relay_lifecycles(run / 'results/relay_stats_detail_tx_info.csv')
                if method != 'relay':
                    logical += ablation.comparison.aggregation_lifecycles(run / 'results/aggregation_tx_lifecycle.csv')
                start, end = min(r[0] for r in logical), max(r[1] for r in logical)
                pool_rows.extend({**keys, **r} for r in pools(run, shards, start, end))
    write(root / 'scaling_runs.csv', runs)
    write(root / 'stage_latency.csv', stage_rows)
    write(root / 'txpool_timeseries.csv', pool_rows)


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--input', type=Path, required=True)
    parser.add_argument('--expected-txs', type=int, default=300000)
    args = parser.parse_args()
    collect(args.input, args.expected_txs)
