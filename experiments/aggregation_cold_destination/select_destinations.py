#!/usr/bin/env python3
"""Select destinations whose expected CTX2 rate reaches a capacity threshold."""
import argparse
import json
from pathlib import Path


def select(report, threshold_percent, injection_speed, block_limit, block_interval_ms):
    if not 0 < threshold_percent <= 100:
        raise ValueError('threshold percent must be in (0, 100]')
    if injection_speed <= 0 or block_limit <= 0 or block_interval_ms <= 0:
        raise ValueError('injection speed, block limit and block interval must be positive')
    if report['transactions'] <= 0 or report['shards'] <= 0:
        raise ValueError('ranking report must contain positive transactions and shards')
    if len(report['ranking']) != report['shards']:
        raise ValueError('ranking must contain exactly one row per shard')
    ids = [row['shard'] for row in report['ranking']]
    if set(ids) != set(range(report['shards'])):
        raise ValueError('ranking contains invalid or duplicate shard IDs')
    if any(row['ctx'] < 0 for row in report['ranking']):
        raise ValueError('CTX2 counts must be non-negative')
    if sum(row['ctx'] for row in report['ranking']) != report['cross_shard']:
        raise ValueError('CTX2 counts do not match the cross-shard total')

    capacity_tps = block_limit * 1000 / block_interval_ms
    shards = []
    selected = []
    covered = 0
    for row in sorted(report['ranking'], key=lambda item: item['shard']):
        # Compare integer products so a value exactly on the boundary is selected
        # without floating-point rounding changing the routing decision.
        aggregate = (
            injection_speed * row['ctx'] * block_interval_ms * 100
            >= threshold_percent * report['transactions'] * block_limit * 1000
        )
        ctx2_tps = injection_speed * row['ctx'] / report['transactions']
        utilization = ctx2_tps / capacity_tps
        shards.append({
            'shard': row['shard'], 'ctx2_count': row['ctx'],
            'ctx2_tps': ctx2_tps, 'capacity_tps': capacity_tps,
            'capacity_utilization': utilization, 'aggregate': aggregate,
        })
        if aggregate:
            selected.append(row['shard'])
            covered += row['ctx']

    total = report['cross_shard']
    if not selected:
        selection_class = 'relay_equivalent'
    elif len(selected) == report['shards']:
        selection_class = 'full_aggregation_equivalent'
    else:
        selection_class = 'partial_aggregation'
    return {
        'policy': 'ctx2_capacity_threshold',
        'selection_class': selection_class,
        'threshold_percent': threshold_percent,
        'injection_speed': injection_speed,
        'block_limit': block_limit,
        'block_interval_ms': block_interval_ms,
        'capacity_tps': capacity_tps,
        'destination_shards': selected,
        'selected_shard_count': len(selected),
        'cold_shards': [row['shard'] for row in shards if not row['aggregate']],
        'covered_ctx': covered,
        'excluded_ctx': total - covered,
        'total_ctx': total,
        'coverage': covered / total if total else 0,
        'dataset_sha256': report['sha256'],
        'transactions': report['transactions'],
        'shards': shards,
    }


def render_config(config, destination_shards):
    text = config.read_text()
    if text.count('\naggregation:\n') != 1:
        raise ValueError('expected one aggregation config section')
    return text.replace(
        '\naggregation:\n',
        '\naggregation:\n  destination_shards: ' + json.dumps(destination_shards) + '\n',
    )


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('--ranking', type=Path, required=True)
    parser.add_argument('--threshold-percent', type=int, required=True)
    parser.add_argument('--injection-speed', type=int, required=True)
    parser.add_argument('--block-limit', type=int, required=True)
    parser.add_argument('--block-interval-ms', type=int, required=True)
    parser.add_argument('--config', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    result = select(
        json.loads(args.ranking.read_text()), args.threshold_percent,
        args.injection_speed, args.block_limit, args.block_interval_ms,
    )
    args.config.write_text(render_config(args.config, result['destination_shards']))
    args.output.write_text(json.dumps(result, indent=2) + '\n')
    print(
        f"Capacity threshold {result['threshold_percent']}%: aggregate destinations "
        f"{result['destination_shards']}, CTX2 coverage {result['coverage']:.2%}, "
        f"class={result['selection_class']}"
    )
