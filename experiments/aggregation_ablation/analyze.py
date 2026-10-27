#!/usr/bin/env python3
"""Summarize committed CTXs; requires complete runs, never extrapolates counters."""
import argparse
import csv
import importlib.util
import json
import statistics
from pathlib import Path

spec = importlib.util.spec_from_file_location('comparison', Path(__file__).resolve().parents[1] / 'aggregation_4shard_300k/analyze.py')
comparison = importlib.util.module_from_spec(spec)
spec.loader.exec_module(comparison)
METHODS = ('relay', 'batch_only', 'aggregation_only', 'full_aggregation')
LEGACY_METHODS = ('relay', 'batch_only', 'full_aggregation')


def methods_for(root):
    manifest = root / 'methods.txt'
    if manifest.exists():
        methods = tuple(manifest.read_text().split())
        if not methods or len(set(methods)) != len(methods) or any(m not in METHODS and not (m.startswith('hot_destination_') and m.removeprefix('hot_destination_').isdigit()) for m in methods):
            raise ValueError(f'invalid method manifest: {manifest}')
        return methods
    # Old three-method results remain readable; a new group must be complete.
    present = any(root.glob('repeat-*/aggregation_only')) or any(root.glob('shards-*/repeat-*/aggregation_only'))
    return METHODS if present else LEGACY_METHODS



def summarize(run, method, expected):
    relay = comparison.relay_lifecycles(run / 'results/relay_stats_detail_tx_info.csv')
    logical = list(relay)
    aggregated = []
    if method != 'relay':
        aggregated = comparison.aggregation_lifecycles(run / 'results/aggregation_tx_lifecycle.csv')
        logical += aggregated
    if len(logical) != expected:
        raise ValueError(f'{run}: completed {len(logical)}, expected {expected}; incomplete run')
    cross = [(s, e) for s, e, ctx in logical if ctx]
    if not cross:
        raise ValueError(f'{run}: no completed CTXs')
    duration = (max(e for _, e, _ in logical) - min(s for s, _, _ in logical)) / 1e9
    latencies = [(e-s)/1e6 for s, e in cross]
    all_latencies = [(e-s)/1e6 for s, e, _ in logical]
    blocks = comparison.block_stats(run)
    entries = blocks.get('block_Relay2TxCount', 0) + blocks.get('block_AggregationSettlementTxCount', 0)
    # Relay2 performs one receiver balance addition per committed CTX.
    updates = blocks.get('block_Relay2TxCount', 0)
    if method != 'relay':
        with (run / 'results/aggregation_execution_metrics.csv').open(newline='') as source:
            updates += sum(int(row['ReceiverUpdateCount']) for row in csv.DictReader(source) if row['Phase'] == 'settlement')
    def path_stats(items):
        latency = [(e-s)/1e6 for s,e,ctx in items if ctx]
        return (len(latency), statistics.fmean(latency) if latency else 0., comparison.percentile(latency, .95) if latency else 0.)
    relay_count, relay_mean, relay_p95 = path_stats(relay)
    agg_count, agg_mean, agg_p95 = path_stats(aggregated)
    if method.startswith('hot_destination_'):
        selection = json.loads((run / 'selection.json').read_text())
        if agg_count != selection['covered_ctx'] or len(cross) != selection['total_ctx']:
            raise ValueError(f'{run}: completed route counts disagree with the selected CSV prefix')
    return dict(relay_completed_ctx=relay_count, relay_ctx_latency_mean_ms=relay_mean, relay_ctx_latency_p95_ms=relay_p95,
                aggregation_completed_ctx=agg_count, aggregation_ctx_latency_mean_ms=agg_mean, aggregation_ctx_latency_p95_ms=agg_p95,
                aggregation_ctx_fraction=agg_count/len(cross), method=method, completed_txs=len(logical), completed_ctx=len(cross), completion_rate=len(logical)/expected,
                duration_s=duration, logical_tps=len(logical)/duration, ctx_tps=len(cross)/duration,
                latency_mean_ms=statistics.fmean(all_latencies), latency_p95_ms=comparison.percentile(all_latencies, .95),
                ctx_latency_mean_ms=statistics.fmean(latencies), ctx_latency_p95_ms=comparison.percentile(latencies, .95),
                destination_entries=entries, receiver_updates=updates,
                destination_entries_per_ctx=entries/len(cross), receiver_updates_per_ctx=updates/len(cross))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--input', type=Path, required=True)
    parser.add_argument('--expected-txs', type=int, required=True)
    args = parser.parse_args()
    rows = []
    for repeat in sorted(args.input.glob('repeat-*')):
        for method in methods_for(args.input):
            row = summarize(repeat / method, method, args.expected_txs)
            row['repeat'] = repeat.name
            rows.append(row)
    if not rows:
        raise ValueError('no repeat directories found')
    with (args.input / 'ablation_runs.csv').open('w', newline='') as target:
        writer = csv.DictWriter(target, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)
    for row in rows:
        print(f"{row['repeat']} {row['method']}: TPS={row['logical_tps']:.2f}, CTX TPS={row['ctx_tps']:.2f}, "
              f"CTX latency mean/p95={row['ctx_latency_mean_ms']:.2f}/{row['ctx_latency_p95_ms']:.2f} ms, "
              f"destination entries={row['destination_entries']}, receiver updates={row['receiver_updates']}")


if __name__ == '__main__':
    main()
