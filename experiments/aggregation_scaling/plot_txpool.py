#!/usr/bin/env python3
from plot_common import args, read, save, plt, methods_for, method_style


def main():
    options = args()
    rows = read(options.input, 'txpool_timeseries.csv')
    shards = sorted({int(r['shards']) for r in rows})
    fig, axes = plt.subplots(len(shards), 1, figsize=(8, 2.7*len(shards)), squeeze=False)
    for ax, shard in zip(axes[:, 0], shards):
        # Each repeat is shown explicitly; do not average different drain durations.
        for method in methods_for(rows):
            label, color = method_style(method)
            selected = [r for r in rows if int(r['shards']) == shard and r['method'] == method]
            for i, repeat in enumerate(sorted({r['repeat'] for r in selected})):
                series = sorted((float(r['time_s']), float(r['total_entries'])) for r in selected if r['repeat'] == repeat)
                ax.plot(*zip(*series), color=color, alpha=.65, label=label if i == 0 else None)
        ax.set_title(f'{shard} shards')
        ax.set_ylabel('Total pool entries')
        ax.set_xlabel('Time since first transaction creation (s)')
        ax.grid(alpha=.2)
        ax.legend(frameon=False)
    save(fig, options, 'txpool')


if __name__ == '__main__':
    main()
