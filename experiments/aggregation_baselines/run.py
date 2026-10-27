#!/usr/bin/env python3
"""Run aligned static BrokerChain and CLPA+Relay baselines once per shard count."""
import argparse
import csv
import json
import os
import re
from pathlib import Path
import statistics
import subprocess
import time
from datetime import datetime

ROOT = Path(__file__).resolve().parents[2]

def stamp(s):
    match = re.fullmatch(r"(.{19})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})", s)
    if not match: raise ValueError(f'invalid timestamp: {s}')
    base, fraction, offset = match.groups()
    seconds = datetime.fromisoformat(base + ('+00:00' if offset == 'Z' else offset)).timestamp()
    return seconds + int((fraction or '0').ljust(9, '0')) / 1e9

def summarize(run, method, expected):
    name = 'broker_stats_detail_tx_info.csv' if method == 'static_broker' else 'relay_stats_detail_tx_info.csv'
    path = run / 'results' / name
    start, end, latencies, seen = float('inf'), -float('inf'), [], set()
    with path.open(newline='') as f:
        for r in csv.DictReader(f):
            key = r['OriginalHash']
            if key in seen: raise ValueError(f'duplicate original transaction: {path}')
            seen.add(key)
            a,b = stamp(r['Tx create time']), stamp(r['Tx finally commit time'])
            if b < a: raise ValueError(f'invalid lifecycle: {path}')
            start,end = min(start,a),max(end,b)
            latencies.append(b-a)
    if len(seen) != expected: raise ValueError(f'{run}: completed {len(seen)}, expected {expected}')
    latencies.sort()
    return dict(completed_txs=len(seen), logical_tps=len(seen)/(end-start),
                latency_mean_ms=statistics.fmean(latencies)*1000,
                latency_p95_ms=latencies[max(0, __import__('math').ceil(.95*len(seen))-1)]*1000,
                duration_s=end-start)

def run_case(args, shards, method, bins):
    run = args.output / f'shards-{shards}' / method
    if run.exists(): raise ValueError(f'output exists: {run}; choose another --output')
    run.mkdir(parents=True)
    text = (ROOT/'experiments/aggregation_ablation/relay.yaml.tmpl').read_text()
    for token,value in {'__RUN_DIR__':str(run),'__BLOCK_LIMIT__':'2000','__TX_NUMBER__':str(args.tx_number),
                        '__TX_SPEED__':str(shards*args.per_shard_rate),'__TX_SOURCE__':'csv_source','__BEACON_INTERVAL_MS__':'1000'}.items():
        text = text.replace(token,value)
    text = text.replace('  shard_num: 4\n',f'  shard_num: {shards}\n').replace('consensus_type: "static_relay"',f'consensus_type: "{method}"')
    text = text.replace('  epoch_duration: 50\n',f'  epoch_duration: {args.clpa_interval}\n')
    if method == 'static_broker' and args.broker_selection == 'hot-random':
        text = text.replace('broker_file_path: "./pkg/broker/broker"', f'broker_file_path: "{args.broker_file}"')
        text = text.replace('broker_num: 50', f'broker_num: {args.broker_count}')
    # The template prefixes paths with ./; normalize absolute output paths.
    text = text.replace('./'+str(run),str(run))
    config = run/'config.yaml';config.write_text(text)
    table={};port=32000
    for shard in list(range(shards))+[2147483647]:
        table[str(shard)]={}
        for node in range(1 if shard==2147483647 else 4):
            table[str(shard)][str(node)]=f'127.0.0.1:{port}';port+=1
    ip=run/'ip_table.json';ip.write_text(json.dumps(table,indent=2))
    logs=run/'process_logs';logs.mkdir()
    processes=[];handles=[]
    def launch(binary,shard,node):
        h=(logs/f'{binary}-{shard}-{node}.log').open('w');handles.append(h)
        p=subprocess.Popen([str(bins/binary),f'-config={config}',f'-ip_table={ip}',f'-shard_id={shard}',f'-node_id={node}'],stdout=h,stderr=subprocess.STDOUT,cwd=ROOT)
        processes.append(p);return p
    print(f'Running {shards} shards / {method}, injection {shards*args.per_shard_rate} TPS',flush=True)
    try:
        for shard in range(shards):
            for node in range(4): launch('consensusnode',shard,node)
        supervisor=launch('supervisor',2147483647,0)
        deadline=time.monotonic()+args.timeout
        while supervisor.poll() is None:
            if time.monotonic()>deadline: raise TimeoutError(f'{run}: timeout; partial results are invalid')
            for p in processes[:-1]:
                if p.poll() is not None: raise RuntimeError(f'node exited; inspect {logs}')
            time.sleep(1)
        if supervisor.returncode: raise RuntimeError(f'supervisor failed; inspect {logs}')
        time.sleep(2)
    finally:
        for p in processes:
            if p.poll() is None:p.terminate()
        for p in processes:
            try:p.wait(timeout=5)
            except subprocess.TimeoutExpired:p.kill();p.wait()
        for h in handles:h.close()
    errors=[]
    for path in logs.glob('*.log'):
        with path.open(errors='replace') as f:
            for line in f:
                if 'level=ERROR' in line or 'level=WARN' in line:
                    errors.append(f'{path}: {line.strip()}')
                    if len(errors)>=5:break
    if errors:raise RuntimeError('\n'.join(errors[:5]))
    return dict(shards=shards,method=method,tx_speed=shards*args.per_shard_rate,**summarize(run,method,args.tx_number))

def main():
    p=argparse.ArgumentParser(description=__doc__)
    p.add_argument('--shards',nargs='+',type=int,default=[4,8,16,24])
    p.add_argument('--methods',nargs='+',choices=['static_broker','clpa_relay'],default=['static_broker','clpa_relay'])
    p.add_argument('--tx-number',type=int,default=300000)
    p.add_argument('--per-shard-rate',type=int,default=600)
    p.add_argument('--clpa-interval',type=int,default=20,help='repartition interval in seconds')
    p.add_argument('--timeout',type=int,default=1800)
    p.add_argument('--output',type=Path,default=Path('exp/aggregation-aligned-baselines-v1'))
    p.add_argument('--broker-selection',choices=['original','hot-random'],default='original')
    p.add_argument('--broker-count',type=int,default=50)
    p.add_argument('--hot-brokers',type=int,default=5)
    p.add_argument('--broker-seed',type=int,default=1)
    args=p.parse_args();os.chdir(ROOT);args.output=args.output.resolve()
    if any(s<2 for s in args.shards) or min(args.tx_number,args.per_shard_rate,args.clpa_interval,args.timeout)<=0:p.error('positive parameters and at least 2 shards required')
    for s in args.shards:
        for m in args.methods:
            if (args.output/f'shards-{s}'/m).exists():raise ValueError('case already exists; use a fresh --output')
    bins=args.output/'bin';bins.mkdir(parents=True,exist_ok=True)
    env=dict(os.environ,GOCACHE=os.environ.get('GOCACHE','/tmp/block-emulator-x-comparison-cache'))
    for name in ('consensusnode','supervisor'):
        subprocess.run(['go','build','-o',str(bins/name),f'./cmd/{name}'],check=True,env=env)
    if args.broker_selection == 'hot-random' and 'static_broker' in args.methods:
        args.broker_file = args.output/'selected_brokers.txt'
        subprocess.run(['go','run','./cmd/selectbrokers',f'-tx-number={args.tx_number}',
            f'-count={args.broker_count}',f'-hot={args.hot_brokers}',f'-seed={args.broker_seed}',
            f'-output={args.broker_file}'],check=True,env=env)
    rows=[]
    for shards in args.shards:
        for method in args.methods:
            row=run_case(args,shards,method,bins);rows.append(row)
            with (args.output/'baseline_runs.csv').open('w',newline='') as f:
                w=csv.DictWriter(f,fieldnames=list(row));w.writeheader();w.writerows(rows)
            print(f"Completed: TPS={row['logical_tps']:.2f}, mean latency={row['latency_mean_ms']/1000:.3f}s",flush=True)
    print(args.output/'baseline_runs.csv')

if __name__=='__main__':main()
