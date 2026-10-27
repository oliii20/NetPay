#!/usr/bin/env python3
import statistics
from plot_common import args, read, save, plt, METHODS, LABELS, methods_for, method_style


def main():
    options = args()
    rows = read(options.input, 'stage_latency.csv')
    shards = sorted({int(r['shards']) for r in rows})
    fig, ax = plt.subplots(figsize=(10, 4.5))
    stages = ('source_ms', 'coordination_ms', 'destination_ms')
    labels = ('Source queue + confirmation', 'Coordination wait + confirmation', 'Destination transfer + queue + confirmation')
    colors = ('#65758B', '#E7AA53', '#459D90')
    methods = methods_for(rows)
    width = .8 / len(methods)
    for i, method in enumerate(methods):
        bottoms = [0.]*len(shards)
        for stage, label, color in zip(stages, labels, colors):
            means = [statistics.fmean(float(r[stage])/1000 for r in rows if int(r['shards']) == shard and r['method'] == method) for shard in shards]
            ax.bar([j+(i-(len(methods)-1)/2)*width for j in range(len(shards))], means, width*.95, bottom=bottoms, color=color, label=label if i == 0 else None)
            bottoms = [a+b for a,b in zip(bottoms, means)]
    ax.set_xticks([j+(i-(len(methods)-1)/2)*width for j in range(len(shards)) for i in range(len(methods))], [f'{s}\n{l}' for s in shards for l in [method_style(m)[0] for m in methods]], fontsize=9)
    ax.set_xlabel('Number of shards / method')
    ax.set_ylabel('Mean CTX latency (s)')
    ax.legend(frameon=False, fontsize=9, loc='upper left', bbox_to_anchor=(0, 1.22))
    ax.grid(axis='y', alpha=.2)
    ax.set_axisbelow(True)
    save(fig, options, 'stage_latency')


if __name__ == '__main__':
    main()
