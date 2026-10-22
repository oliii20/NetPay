#!/usr/bin/env python3
"""Summarize committed CTXs; requires complete runs, never extrapolates counters."""
import argparse
import csv
import importlib.util
import statistics
from pathlib import Path

spec = importlib.util.spec_from_file_location('comparison', Path(__file__).resolve().parents[1] / 'aggregation_4shard_300k/analyze.py')
comparison = importlib.util.module_from_spec(spec)
spec.loader.exec_module(comparison)
METHODS = ('relay', 'batch_only', 'full_aggregation')


def summarize(run, method, expected):
    relay = comparison.relay_lifecycles(run / 'results/relay_stats_detail_tx_info.csv')
    logical = list(relay)
    if method != 'relay':
        logical += comparison.aggregation_lifecycles(run / 'results/aggregation_tx_lifecycle.csv')
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
    return dict(method=method, completed_txs=len(logical), completed_ctx=len(cross), completion_rate=len(logical)/expected,
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
        for method in METHODS:
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
