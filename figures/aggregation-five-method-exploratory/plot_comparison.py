#!/usr/bin/env python3
"""Exploratory five-way comparison; historic baselines have different workloads.

Metrics use original completed transactions: count/(last commit-first creation)
and mean final-commit minus creation. Old baseline timestamps have second precision.
"""
import csv
import math
import statistics
from datetime import datetime
from pathlib import Path
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
import numpy as np

ROOT = Path(__file__).resolve().parents[2]
OUT = Path(__file__).resolve().parent
HIST = ROOT / 'exp/netting-paper/repeated-exp1-scale-4-8-16-24/sessions'
HOT = ROOT / 'exp/aggregation-hot-destination-scale-v1'
BATCH = ROOT / 'exp/aggregation-scale-beacon1000-repeat3/scaling_runs.csv'
SHARDS = (4, 8, 16, 24)
STYLES = [('Monoxide', '#65758B'), ('BrokerChain*', '#E7AA53'),
          ('CLPA+Relay*', '#8872AE'), ('Batch-only', '#4C78A8'), ('Proposed', '#459D90')]

def timestamp(v):
    return datetime.fromisoformat(v.replace('Z', '+00:00')).timestamp()

def historic(shards, method):
    pattern = f'*/runs/repeat_01/exp1_scale_repeated_{method}_shard_num_{shards}_seed1/results/'
    dirs = sorted(HIST.glob(pattern))
    if len(dirs) != 1:
        raise ValueError(f'expected one historic run for {shards}/{method}, found {len(dirs)}')
    name = 'broker_stats_detail_tx_info.csv' if method == 'static_broker' else 'relay_stats_detail_tx_info.csv'
    p = dirs[0] / name
    n = 0; start = math.inf; end = -math.inf; latency_sum = 0.; ids = set()
    with p.open(newline='') as f:
        for r in csv.DictReader(f):
            if not r['Tx create time'] or not r['Tx finally commit time']:
                raise ValueError(f'incomplete lifecycle in {p}')
            txid = r['OriginalHash']
            if txid in ids: raise ValueError(f'duplicate transaction {txid} in {p}')
            ids.add(txid)
            a, b = timestamp(r['Tx create time']), timestamp(r['Tx finally commit time'])
            if b < a: raise ValueError(f'negative latency in {p}')
            n += 1; start = min(start, a); end = max(end, b); latency_sum += b-a
    if n != 300000 or end <= start: raise ValueError(f'{p}: completed {n}, expected 300000')
    return n/(end-start), latency_sum/n, str(p.relative_to(ROOT)), n

def input_rows():
    with BATCH.open(newline='') as f:
        batches = {(int(r['shards']), r['repeat'], r['method']): r for r in csv.DictReader(f)}
    rows = []
    for s in SHARDS:
        p = HOT / f'shards{s}-speed{s*600}' / 'scaling_runs.csv'
        with p.open(newline='') as f: hot = {r['method']: r for r in csv.DictReader(f)}
        selected = [('Monoxide', hot['relay'], str(p.relative_to(ROOT))),
                    ('Batch-only', batches[(s, 'repeat-01', 'batch_only')], str(BATCH.relative_to(ROOT))),
                    ('Proposed', hot['hot_destination_50' if s == 24 else 'full_aggregation'], str(p.relative_to(ROOT)))]
        for name, row, source in selected:
            if int(row['completed_txs']) != 300000: raise ValueError(f'incomplete {source}: {name}, {s}')
            rows.append(dict(shards=s, method=name, tps=float(row['logical_tps']),
                latency_s=float(row['latency_mean_ms'])/1000, completed=300000,
                source=source, run_method=row['method'], injection_speed=s*600,
                workload='original filtered dataset', repeat='01', timestamp_precision='ms/ns'))
        for method, label in [('static_broker','BrokerChain*'), ('clpa_relay','CLPA+Relay*')]:
            tps, latency, source, count = historic(s, method)
            rows.append(dict(shards=s, method=label, tps=tps, latency_s=latency,
                completed=count, source=source, run_method=method,
                injection_speed={4:3000,8:6000,16:12000,24:24000}[s],
                workload='historic workload.csv; exclude_contract_txs=false', repeat='01/seed1', timestamp_precision='s'))
    return rows

def main():
    rows=input_rows()
    with (OUT/'comparison_data.csv').open('w',newline='') as f:
        w=csv.DictWriter(f,fieldnames=list(rows[0]));w.writeheader();w.writerows(rows)
    plt.rcParams.update({'font.family':'serif','font.size':9,'pdf.fonttype':42,'axes.spines.top':False,'axes.spines.right':False})
    fig,axes=plt.subplots(1,2,figsize=(9.2,4.1))
    x=np.arange(len(SHARDS));width=.16
    for ax, metric, label in [(axes[0],'tps','Completed TPS (tx/s)'),(axes[1],'latency_s','Mean transaction latency (s)')]:
        for i,(method,color) in enumerate(STYLES):
            values=[next(r[metric] for r in rows if r['shards']==s and r['method']==method) for s in SHARDS]
            bars=ax.bar(x+(i-2)*width,values,width*.94,color=color,label=method,edgecolor='white',linewidth=.3)
            if '*' in method:
                for bar in bars: bar.set_hatch('//');bar.set_edgecolor('#303030');bar.set_linewidth(.35)
        ax.set_xticks(x,SHARDS);ax.set_xlabel('Number of shards');ax.set_ylabel(label)
        ax.grid(axis='y',alpha=.2);ax.set_axisbelow(True)
    handles,labels=axes[0].get_legend_handles_labels()
    fig.legend(handles,labels,loc='upper center',ncol=5,frameon=False,bbox_to_anchor=(.5,.98),fontsize=9)
    fig.text(.5,.025,'Exploratory only: * historical baselines use different injection rates and workloads.\nProposed: Full at 4/8/16; hot-destination 50% at 24. One run per point.',ha='center',fontsize=8,color='#9b2929')
    fig.subplots_adjust(top=.79,bottom=.24,wspace=.27)
    for suffix in ('png','pdf','svg'):fig.savefig(OUT/f'five_method_exploratory.{suffix}',dpi=300,bbox_inches='tight')
    plt.close(fig)
    print('Saved:', OUT/'five_method_exploratory.png')
    for r in rows: print(r['shards'],r['method'],round(r['tps'],2),round(r['latency_s'],2))
if __name__=='__main__':main()
