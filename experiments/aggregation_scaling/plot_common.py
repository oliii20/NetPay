"""Shared style and grouped bars; each figure has a separate entry point."""
import argparse
import csv
import statistics
from pathlib import Path
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt

METHODS = ('relay', 'batch_only', 'aggregation_only', 'full_aggregation', 'hot_destination_25', 'hot_destination_50', 'hot_destination_75', 'hot_destination_100')
LABELS = ('Relay', 'Batch-only', 'Aggregation-only', 'Full aggregation', 'Hot destinations 25%', 'Hot destinations 50%', 'Hot destinations 75%', 'Hot destinations 100%')
COLORS = ('#65758B', '#E7AA53', '#8872AE', '#459D90', '#4C78A8', '#E45756', '#B279A2', '#72B7B2')
plt.rcParams.update({'font.size': 11, 'pdf.fonttype': 42, 'axes.spines.top': False, 'axes.spines.right': False})


def methods_for(rows):
    global METHODS, LABELS, COLORS
    present = {r['method'] for r in rows}
    extra = sorted(present - set(METHODS))
    for method in extra:
        if not method.startswith('hot_destination_'):
            raise ValueError(f'unknown method: {method}')
        METHODS += (method,)
        LABELS += (f'Hot destinations {method.rsplit("_", 1)[1]}%',)
        COLORS += (plt.get_cmap('tab10')(len(METHODS) % 10),)
    return [m for m in METHODS if m in present]


def args():
    p = argparse.ArgumentParser()
    p.add_argument('--input', type=Path, default=Path('exp/aggregation-scaling'))
    p.add_argument('--output', type=Path)
    return p.parse_args()


def read(root, name):
    with (root / name).open(newline='') as f:
        return list(csv.DictReader(f))


def save(fig, options, name):
    out = options.output or options.input / 'figures'
    out.mkdir(parents=True, exist_ok=True)
    fig.tight_layout()
    for ext in ('png', 'pdf'):
        fig.savefig(out / f'{name}.{ext}', dpi=300, bbox_inches='tight')
    plt.close(fig)
    print(out / f'{name}.png')


def bar(metric, ylabel, name, scale=1):
    options = args()
    rows = read(options.input, 'scaling_runs.csv')
    shards = sorted({int(r['shards']) for r in rows})
    fig, ax = plt.subplots(figsize=(7.2, 4))
    methods = methods_for(rows)
    width = .8 / len(methods)
    for i, method in enumerate(methods):
        samples = [[float(r[metric])*scale for r in rows if int(r['shards']) == s and r['method'] == method] for s in shards]
        means = [statistics.fmean(v) for v in samples]
        sd = [statistics.stdev(v) if len(v)>1 else 0 for v in samples]
        ax.bar([j+(i-(len(methods)-1)/2)*width for j in range(len(shards))], means, width*.95, yerr=sd if any(len(v)>1 for v in samples) else None, capsize=3, label=LABELS[METHODS.index(method)], color=COLORS[METHODS.index(method)])
    ax.set_xticks(range(len(shards)), shards)
    ax.set_xlabel('Number of shards')
    ax.set_ylabel(ylabel)
    ax.grid(axis='y', alpha=.2)
    ax.set_axisbelow(True)
    ax.legend(frameon=False)
    save(fig, options, name)


def method_style(method):
    methods_for([{'method': method}])
    i = METHODS.index(method)
    return LABELS[i], COLORS[i]
