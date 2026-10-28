#!/usr/bin/env python3
"""Plot a same-build three-way suite and its Beacon routing timeline."""
import argparse
import csv
import json
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.lines import Line2D
import numpy as np


def rows(path):
    with path.open() as handle:
        return list(csv.DictReader(handle))


def export_csv(path, records):
    with path.open("w", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=list(records[0]))
        writer.writeheader()
        writer.writerows(records)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("suite", type=Path)
    args = parser.parse_args()
    root = args.suite.resolve()
    out = root / "figures"
    out.mkdir(exist_ok=True)
    modes = ["full", "adaptive", "monoxide"]
    labels = ["Full aggregation", "Adaptive", "Pure Relay"]
    colors = ["#0072B2", "#D55E00", "#009E73"]
    summaries, manifests, queues = {}, {}, {}
    for mode in modes:
        case = root / "shards-24/repeat-01" / mode
        summaries[mode] = json.loads((case / "summary.json").read_text())
        manifests[mode] = json.loads((case / "manifest.json").read_text())
        validation = json.loads((case / "validation.json").read_text())
        assert validation["completed_originals"] == validation["expected"] == 1000000
        assert (
            validation["duplicate_originals"] == validation["warning_error_count"] == 0
        )
        assert manifests[mode]["settings"]["block_limit"] == 2000
        if mode != "monoxide":
            start = summaries[mode]["start_ns"]
            queues[mode] = sorted(
                [
                    dict(
                        time_s=(int(r["ObservedAtUnixNano"]) - start) / 1e9,
                        queued_originals=int(r["QueuedOriginals"]),
                        frozen_wait_s=int(r["FrozenQueueWaitNs"]) / 1e9,
                    )
                    for r in rows(case / "results/aggregation_queue_timeseries.csv")
                ],
                key=lambda r: r["time_s"],
            )
    assert all(
        manifests[m]["binary_sha256"] == manifests["full"]["binary_sha256"]
        for m in modes
    )
    assert all(
        manifests[m]["dataset_sha256"] == manifests["full"]["dataset_sha256"]
        for m in modes
    )
    policies = []
    before = 24
    for r in rows(
        root / "shards-24/repeat-01/adaptive/results/aggregation_routing_policy.csv"
    ):
        after = len(r["DestinationShards"].split(";"))
        policies.append(
            dict(
                version=int(r["Version"]),
                time_s=(int(r["IssuedAtUnixNano"]) - summaries["adaptive"]["start_ns"])
                / 1e9,
                before=before,
                after=after,
                action="Load shedding" if after < before else "Recovery",
            )
        )
        before = after
    assert [(p["before"], p["after"]) for p in policies] == [
        (24, 18),
        (18, 12),
        (12, 6),
        (6, 12),
        (12, 18),
    ]
    export_csv(out / "switching_timeline.csv", policies)
    export_csv(
        out / "performance_data.csv",
        [
            dict(
                method=label,
                tps=summaries[m]["logical_tps"],
                mean_s=summaries[m]["latency_mean_ms"] / 1000,
                p95_s=summaries[m]["latency_p95_ms"] / 1000,
                p99_s=summaries[m]["latency_p99_ms"] / 1000,
            )
            for m, label in zip(modes, labels)
        ],
    )
    for m, q in queues.items():
        export_csv(out / f"{m}_queue_data.csv", q)
    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "DejaVu Serif"],
            "font.size": 11,
            "axes.spines.top": False,
            "axes.spines.right": False,
            "legend.frameon": False,
            "pdf.fonttype": 42,
            "savefig.dpi": 300,
        }
    )

    def save(fig, name):
        for ext in ("png", "pdf", "svg"):
            fig.savefig(out / f"{name}.{ext}", bbox_inches="tight")
        plt.close(fig)

    fig, axes = plt.subplots(1, 2, figsize=(12, 4.5))
    values = [summaries[m]["logical_tps"] for m in modes]
    bars = axes[0].bar(np.arange(3), values, color=colors, width=0.62)
    axes[0].bar_label(bars, labels=[f"{v:,.0f}" for v in values], padding=5)
    axes[0].set(
        xticks=np.arange(3),
        xticklabels=labels,
        ylabel="Throughput (original tx/s)",
        ylim=(0, max(values) * 1.18),
        title="(a) Throughput",
    )
    width = 0.23
    for i, (m, label, color) in enumerate(zip(modes, labels, colors)):
        vals = [
            summaries[m][k] / 1000
            for k in ("latency_mean_ms", "latency_p95_ms", "latency_p99_ms")
        ]
        b = axes[1].bar(
            np.arange(3) + (i - 1) * width, vals, width * 0.95, label=label, color=color
        )
        axes[1].bar_label(b, labels=[f"{v:.2f}" for v in vals], padding=3, fontsize=9)
    axes[1].set(
        xticks=np.arange(3),
        xticklabels=["Mean", "P95", "P99"],
        ylabel="Confirmation latency (s)",
        ylim=(0, 140),
        title="(b) End-to-end latency",
    )
    axes[1].legend(fontsize=9, loc="upper left")
    for ax in axes:
        ax.grid(axis="y", alpha=0.18)
        ax.set_axisbelow(True)
    fig.suptitle(
        "24 shards | 1,000,000 valid transactions | 700 tx/s/shard | block limit 2,000",
        fontsize=13,
    )
    fig.text(
        0.5,
        0.01,
        "One run per method; no error bars. TPS uses first creation to final confirmation.",
        ha="center",
        fontsize=9,
        color="#555555",
    )
    fig.tight_layout(rect=(0, 0.05, 1, 0.94))
    save(fig, "performance_comparison")

    end = max(q[-1]["time_s"] for q in queues.values())
    fig, axes = plt.subplots(
        3, 1, figsize=(12, 9), sharex=True, gridspec_kw={"height_ratios": [1, 1, 0.9]}
    )
    for m, label, color, style in zip(modes[:2], labels[:2], colors[:2], ["--", "-"]):
        q = queues[m]
        x = [r["time_s"] for r in q]
        axes[0].step(
            x,
            [r["queued_originals"] / 1000 for r in q],
            where="post",
            color=color,
            ls=style,
            label=label,
        )
        axes[1].step(
            x,
            [r["frozen_wait_s"] for r in q],
            where="post",
            color=color,
            ls=style,
            label=label,
        )
        for ax in axes:
            ax.axvline(
                summaries[m]["injection_span_s"], color=color, ls=":", lw=1.4, alpha=0.8
            )
    axes[0].set(ylabel="Queued originals (thousands)", title="(a) Frozen queue backlog")
    axes[1].set(ylabel="Oldest frozen wait (s)", title="(b) Frozen queue waiting time")
    axes[2].step(
        [0] + [p["time_s"] for p in policies] + [end],
        [24] + [p["after"] for p in policies] + [policies[-1]["after"]],
        where="post",
        color=colors[1],
        lw=2,
    )
    axes[2].plot([0, end], [24, 24], color=colors[0], ls="--", lw=1.5)
    for p in policies:
        color = "#CC79A7" if p["after"] < p["before"] else "#009E73"
        for ax in axes:
            ax.axvline(p["time_s"], color=color, ls="--", lw=1, alpha=0.65)
        axes[2].scatter(
            p["time_s"],
            p["after"],
            marker="v" if p["after"] < p["before"] else "^",
            color=color,
            zorder=5,
            s=50,
        )
        axes[2].annotate(
            f"{p['before']}→{p['after']}\n{p['time_s']:.2f} s",
            (p["time_s"], p["after"]),
            xytext=(5, 12),
            textcoords="offset points",
            fontsize=9,
            color=color,
        )
    axes[2].set(
        ylabel="Aggregation destinations",
        xlabel="Time since first transaction creation in each run (s)",
        yticks=[6, 12, 18, 24],
        ylim=(0, 31),
        title="(c) Adaptive routing policy",
    )
    axes[0].legend(loc="upper left", ncol=2)
    axes[1].legend(
        handles=[
            Line2D([0], [0], color="#CC79A7", ls="--", label="Load shedding"),
            Line2D([0], [0], color="#009E73", ls="--", label="Recovery"),
            Line2D(
                [0],
                [0],
                color=colors[0],
                ls=":",
                label=f"Full input ends ({summaries['full']['injection_span_s']:.2f} s)",
            ),
            Line2D(
                [0],
                [0],
                color=colors[1],
                ls=":",
                label=f"Adaptive input ends ({summaries['adaptive']['injection_span_s']:.2f} s)",
            ),
        ],
        loc="upper left",
        fontsize=9,
        ncol=2,
    )
    for ax in axes:
        ax.set_xlim(0, end)
        ax.grid(alpha=0.15)
    fig.suptitle(
        "Frozen backlog and adaptive switching | 24 shards, block limit 2,000",
        fontsize=14,
        y=0.99,
    )
    fig.text(
        0.5,
        0.012,
        "Independent runs aligned by first creation; sampled queues shown as steps, without smoothing.\n"
        "Pure Relay has no Beacon aggregation queue. Both recoveries occur after adaptive input ends.",
        ha="center",
        fontsize=9,
        color="#555555",
    )
    fig.tight_layout(rect=(0, 0.065, 1, 0.97), h_pad=1.2)
    save(fig, "queue_and_switching_timeline")
    (out / "README.md").write_text(
        "# 区块上限 2000：三方案性能与动态机制\n\n"
        "- performance_comparison：TPS 与平均/P95/P99 确认时延。\n"
        "- queue_and_switching_timeline：全聚合/自适应冻结积压、最老冻结等待及策略阶梯。\n"
        "- 每张图提供 PNG（300 dpi）、PDF、SVG；CSV 保存绘图数据和切换时间。\n\n"
        "各组按自己的首笔交易创建对齐；曲线为采样后值保持，无平滑。纯 Relay 不存在 Beacon 聚合队列。\n"
        "紫色虚线为降载，绿色虚线为恢复；蓝/橙色点线分别是全聚合/自适应注入结束。\n"
        "两次恢复均在注入结束后，不代表持续高负载下已验证恢复稳定性。每方案只有一次，无误差条。\n\n"
        f"复现：`/opt/anaconda3/bin/python3 figures/gen_fig_block2000_comparison.py '{root}'`\n"
    )
    print(out)


if __name__ == "__main__":
    main()
