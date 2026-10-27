#!/usr/bin/env python3
"""Analyze capacity-threshold selections before launching a cluster."""
import argparse
import csv
import json
from pathlib import Path

from select_destinations import select


def analyze(report, thresholds, injection_speed, block_limit, block_interval_ms):
    selections = [
        select(report, threshold, injection_speed, block_limit, block_interval_ms)
        for threshold in thresholds
    ]
    rows = []
    for result in selections:
        for shard in result['shards']:
            rows.append({
                'threshold_percent': result['threshold_percent'],
                'shard': shard['shard'], 'ctx2_count': shard['ctx2_count'],
                'ctx2_tps': shard['ctx2_tps'], 'capacity_tps': shard['capacity_tps'],
                'capacity_utilization': shard['capacity_utilization'],
                'aggregate': shard['aggregate'],
                'selection_class': result['selection_class'],
                'selected_ctx_fraction': result['coverage'],
            })
    return selections, rows


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--ranking', type=Path, required=True)
    parser.add_argument('--thresholds', type=int, nargs='+', default=[10, 20, 30, 40])
    parser.add_argument('--injection-speed', type=int, required=True)
    parser.add_argument('--block-limit', type=int, default=2000)
    parser.add_argument('--block-interval-ms', type=int, default=2000)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    selections, rows = analyze(
        json.loads(args.ranking.read_text()), args.thresholds,
        args.injection_speed, args.block_limit, args.block_interval_ms,
    )
    args.output.mkdir(parents=True, exist_ok=True)
    (args.output / 'capacity_analysis.json').write_text(json.dumps(selections, indent=2) + '\n')
    with (args.output / 'capacity_analysis.csv').open('w', newline='') as target:
        writer = csv.DictWriter(target, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)
    for result in selections:
        print(
            f"{result['threshold_percent']}%: {result['selected_shard_count']} destinations, "
            f"CTX2 coverage {result['coverage']:.2%}, class={result['selection_class']}, "
            f"selected={result['destination_shards']}"
        )
