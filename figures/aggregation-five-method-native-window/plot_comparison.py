#!/usr/bin/env python3
"""Native epoch TPS with logical-payment extension for aggregation. Epoch values are rounded in source CSVs."""
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
BASELINES = ROOT / 'exp/aggregation-aligned-baselines-v1/baseline_runs.csv'
HOT = ROOT / 'exp/aggregation-hot-destination-scale-v1'
BATCH = ROOT / 'exp/aggregation-scale-beacon1000-repeat3/scaling_runs.csv'
SHARDS = (4, 8, 16, 24)
STYLES = [('Monoxide', '#65758B'), ('BrokerChain', '#E7AA53'),
          ('CLPA+Relay', '#8872AE'), ('Batch-only', '#4C78A8'), ('Proposed', '#459D90')]

def original_rows():
    with BATCH.open(newline='') as f:
        batches = {(int(r['shards']), r['repeat'], r['method']): r for r in csv.DictReader(f)}
    with BASELINES.open(newline='') as f:
        baselines = {(int(r['shards']), r['method']): r for r in csv.DictReader(f)}
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
        for method, label in [('static_broker','BrokerChain'), ('clpa_relay','CLPA+Relay')]:
            r = baselines[(s, method)]
            if int(r['completed_txs']) != 300000 or int(r['tx_speed']) != s*600:
                raise ValueError(f'incomplete or mismatched baseline: {s}/{method}')
            rows.append(dict(shards=s, method=label, tps=float(r['logical_tps']),
                latency_s=float(r['latency_mean_ms'])/1000, completed=300000,
                source=str(BASELINES.relative_to(ROOT)), run_method=method,
                injection_speed=s*600, workload='original filtered dataset', repeat='01', timestamp_precision='ms/ns'))
    return rows

def input_rows():
    rows = original_rows()
    epoch_rows = []
    for r in rows:
        n = r['shards']; method = r['run_method']
        if method in ('static_broker', 'clpa_relay'):
            run = ROOT / f'exp/aggregation-aligned-baselines-v1/shards-{n}' / method
        elif method == 'batch_only':
            run = ROOT / f'exp/aggregation-scale-beacon1000-repeat3/shards-{n}/repeat-01/batch_only'
        else:
            run = ROOT / f'exp/aggregation-hot-destination-scale-v1/shards{n}-speed{n*600}/shards-{n}/repeat-01' / method
        brief = run / 'results' / ('broker_stats_brief_info.csv' if method == 'static_broker' else 'relay_stats_brief_info.csv')
        with brief.open(newline='') as f: epochs = list(csv.DictReader(f))
        is_aggregation = method in ('batch_only', 'full_aggregation', 'hot_destination_50')
        if is_aggregation and len(epochs) != 1:
            raise ValueError('aggregation payment allocation across multiple epochs is unavailable')
        extended = []
        for e in epochs:
            raw_tps = float(e['Avg. TPS of this epoch (txs per second)'])
            raw_count = float(e['Total tx # in this epoch'])
            count = r['completed'] if is_aggregation else raw_count
            tps = raw_tps * count / raw_count if is_aggregation else raw_tps
            latency_column = next(k for k in e if k.startswith('Avg. TCL of this epoch ('))
            # Current Go code formats float64(time.Duration) without conversion,
            # including Broker's incorrectly labelled '(second)' column.
            native_latency = float(e[latency_column]) / 1e9
            epoch_rows.append(dict(shards=n,method=r['method'],epoch=e['EpochID'],
                native_count=raw_count,extended_count=count,native_tps=raw_tps,
                extended_tps=tps,native_latency_s=native_latency,source=str(brief.relative_to(ROOT))))
            extended.append(tps)
        r['completion_tps'] = r['tps']
        r['tps'] = statistics.fmean(extended)
        r['native_raw_tps_epoch_mean'] = statistics.fmean(float(e['Avg. TPS of this epoch (txs per second)']) for e in epochs)
        r['tps_definition'] = 'arithmetic mean of native epoch TPS; logical-count extension for aggregation'
        r['latency_definition'] = 'original transaction creation to final commit, all original transactions'
    with (OUT/'native_epoch_data.csv').open('w',newline='') as f:
        w=csv.DictWriter(f,fieldnames=list(epoch_rows[0]));w.writeheader();w.writerows(epoch_rows)
    return rows

def plot_single(rows, metric, ylabel, stem):
    plt.rcParams.update({'font.family':'serif','font.size':10,'pdf.fonttype':42,'axes.spines.top':False,'axes.spines.right':False})
    fig,ax=plt.subplots(figsize=(6.8,4.0))
    x=np.arange(len(SHARDS));width=.16
    for i,(method,color) in enumerate(STYLES):
        values=[next(r[metric] for r in rows if r['shards']==s and r['method']==method) for s in SHARDS]
        ax.bar(x+(i-2)*width,values,width*.94,color=color,label=method,edgecolor='white',linewidth=.3)
    ax.set_xticks(x,SHARDS);ax.set_xlabel('Number of shards');ax.set_ylabel(ylabel)
    ax.grid(axis='y',alpha=.2);ax.set_axisbelow(True)
    ax.legend(loc='lower center',bbox_to_anchor=(.5,1.01),ncol=3,frameon=False,fontsize=9)
    fig.tight_layout()
    for suffix in ('png','pdf','svg'):fig.savefig(OUT/f'{stem}.{suffix}',dpi=300,bbox_inches='tight')
    plt.close(fig)

def main():
    rows=input_rows()
    with (OUT/'comparison_data.csv').open('w',newline='') as f:
        w=csv.DictWriter(f,fieldnames=list(rows[0]));w.writeheader();w.writerows(rows)
    plt.rcParams.update({'font.family':'serif','font.size':9,'pdf.fonttype':42,'axes.spines.top':False,'axes.spines.right':False})
    fig,axes=plt.subplots(1,2,figsize=(9.2,4.05))
    x=np.arange(len(SHARDS));width=.16
    for ax, metric, label in [(axes[0],'tps','Mean epoch TPS (tx/s)'),(axes[1],'latency_s','Mean transaction latency (s)')]:
        for i,(method,color) in enumerate(STYLES):
            values=[next(r[metric] for r in rows if r['shards']==s and r['method']==method) for s in SHARDS]
            bars=ax.bar(x+(i-2)*width,values,width*.94,color=color,label=method,edgecolor='white',linewidth=.3)
            if '*' in method:
                for bar in bars: bar.set_hatch('//');bar.set_edgecolor('#303030');bar.set_linewidth(.35)
        ax.set_xticks(x,SHARDS);ax.set_xlabel('Number of shards');ax.set_ylabel(label)
        ax.grid(axis='y',alpha=.2);ax.set_axisbelow(True)
    handles,labels=axes[0].get_legend_handles_labels()
    fig.legend(handles,labels,loc='upper center',ncol=5,frameon=False,bbox_to_anchor=(.5,.98),fontsize=9)
    fig.text(.5,.025,'TPS: native epoch mean; logical-count extension for aggregation (approx. from rounded CSVs).\nLatency: original end-to-end definition. Proposed: 50% hot destinations at 24 shards.',ha='center',fontsize=8,color='#444444')
    fig.subplots_adjust(top=.82,bottom=.24,wspace=.27)
    for suffix in ('png','pdf','svg'):fig.savefig(OUT/f'five_method_native_window.{suffix}',dpi=300,bbox_inches='tight')
    plt.close(fig)
    for metric, ylabel, stem in [('tps','Mean epoch TPS (tx/s)','tps'),('latency_s','Mean transaction latency (s)','latency')]:
        plot_single(rows, metric, ylabel, stem)
    print('Saved:' , OUT/'five_method_native_window.png')
    for r in rows: print(r['shards'],r['method'],round(r['tps'],2),round(r['latency_s'],2))
if __name__=='__main__':main()
