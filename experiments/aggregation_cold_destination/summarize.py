#!/usr/bin/env python3
"""Join capacity selection metadata with the standard scaling results."""
import argparse
import csv
import json
from pathlib import Path


def read(path):
    with path.open(newline='') as source:
        return list(csv.DictReader(source))


def collect(root):
    rows = []
    for run in read(root / 'scaling_runs.csv'):
        method = run['method']
        shard_count = int(run['shards'])
        threshold = ''
        if method == 'relay':
            selected_count, coverage = 0, 0.0
            cold_count = shard_count
        elif method == 'full_aggregation':
            selected_count, coverage = shard_count, 1.0
            cold_count = 0
        elif method.startswith('cold_exclusion_'):
            threshold = int(method.removeprefix('cold_exclusion_'))
            relative = f"shards-{run['shards']}/{run['repeat']}/{method}"
            selection = json.loads((root / relative / 'selection.json').read_text())
            selected_count = selection['selected_shard_count']
            cold_count = len(selection['cold_shards'])
            coverage = selection['coverage']
        else:
            continue
        rows.append({
            'capacity_threshold_percent': threshold,
            'selected_shards': selected_count,
            'cold_shards': cold_count,
            'selected_ctx_fraction': coverage,
            **run,
        })
    def method_order(row):
        if row['method'] == 'relay':
            return -1
        if row['method'] == 'full_aggregation':
            return 101
        return int(row['capacity_threshold_percent'])

    return sorted(
        rows,
        key=lambda row: (row['repeat'], method_order(row)),
    )


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--input', type=Path, required=True)
    args = parser.parse_args()
    rows = collect(args.input)
    if not rows:
        raise ValueError('no cold-destination experiment rows found')
    output = args.input / 'cold_exclusion_summary.csv'
    with output.open('w', newline='') as target:
        writer = csv.DictWriter(target, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)
    for row in rows:
        threshold = row['capacity_threshold_percent'] or row['method']
        print(
            f"{threshold}: selected={row['selected_shards']}, "
            f"CTX2 coverage={float(row['selected_ctx_fraction']):.2%}, "
            f"TPS={float(row['logical_tps']):.2f}, "
            f"CTX mean/p95={float(row['ctx_latency_mean_ms']):.2f}/"
            f"{float(row['ctx_latency_p95_ms']):.2f} ms"
        )
    print(f'Saved: {output}')
