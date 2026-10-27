#!/usr/bin/env python3
"""Join completion throughput, route coverage and coordination diagnostics."""
import argparse
import csv
import json
from pathlib import Path


def read(path):
    with path.open(newline='') as source:
        return list(csv.DictReader(source))


def collect(root):
    diagnostics = {r['run']: r for r in read(root / 'coordination_diagnostics.csv')}
    stages = {(r['shards'], r['repeat'], r['method']): r
              for r in read(root / 'stage_latency.csv')}
    rows = []
    for run in read(root / 'scaling_runs.csv'):
        method = run['method']
        percent = 100 if method == 'full_aggregation' else int(method.removeprefix('hot_destination_'))
        relative = f"shards-{run['shards']}/{run['repeat']}/{method}"
        if percent == 100:
            selected = int(run['shards'])
            coverage = 1.0
        else:
            selection = json.loads((root / relative / 'selection.json').read_text())
            selected, coverage = selection['k'], selection['coverage']
        row = dict(requested_percent=percent, selected_shards=selected,
                   actual_destination_percent=100 * selected / int(run['shards']),
                   selected_ctx_fraction=coverage, **run)
        stage = stages[(run['shards'], run['repeat'], method)]
        row.update({key: stage[key] for key in ('source_ms', 'coordination_ms', 'destination_ms')})
        row.update({key: value for key, value in diagnostics[relative].items() if key != 'run'})
        rows.append(row)
    return sorted(rows, key=lambda r: (r['repeat'], r['requested_percent']))


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--input', type=Path, required=True)
    args = parser.parse_args()
    rows = collect(args.input)
    output = args.input / 'fine_sweep_summary.csv'
    with output.open('w', newline='') as target:
        writer = csv.DictWriter(target, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)
    for row in rows:
        print(f"{row['requested_percent']}% ({row['selected_shards']} shards): "
              f"TPS={float(row['logical_tps']):.2f}, "
              f"mean={float(row['latency_mean_ms']) / 1000:.3f}s, "
              f"p95={float(row['latency_p95_ms']) / 1000:.3f}s, "
              f"CTX coverage={row['selected_ctx_fraction']:.2%}")
    print(f'Saved: {output}')
