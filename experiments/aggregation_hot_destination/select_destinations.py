#!/usr/bin/env python3
"""Render a fixed allowlist without changing the historical dataset or Full settings."""
import argparse
import json
import math
from pathlib import Path

def select(report, percent):
    if not 0 < percent <= 100:
        raise ValueError('percent must be in (0, 100]')
    k = math.ceil(report['shards'] * percent / 100)
    selected = report['ranking'][:k]
    covered = sum(r['ctx'] for r in selected)
    return dict(percent=percent, k=k, destination_shards=[r['shard'] for r in selected],
                covered_ctx=covered, total_ctx=report['cross_shard'],
                coverage=covered / report['cross_shard'] if report['cross_shard'] else 0,
                dataset_sha256=report['sha256'], transactions=report['transactions'])

if __name__ == '__main__':
    p = argparse.ArgumentParser()
    p.add_argument('--ranking', type=Path, required=True)
    p.add_argument('--percent', type=int, required=True)
    p.add_argument('--config', type=Path, required=True)
    p.add_argument('--output', type=Path, required=True)
    a = p.parse_args()
    result = select(json.loads(a.ranking.read_text()), a.percent)
    text = a.config.read_text()
    if text.count('\naggregation:\n') != 1:
        raise ValueError('expected one aggregation config section')
    a.config.write_text(text.replace('\naggregation:\n', '\naggregation:\n  destination_shards: ' + json.dumps(result['destination_shards']) + '\n'))
    a.output.write_text(json.dumps(result, indent=2) + '\n')
    print(f"Hot destinations: {result['destination_shards']}, CTX coverage {result['coverage']:.2%}")
