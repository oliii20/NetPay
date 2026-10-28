#!/usr/bin/env python3
"""Compare historical full/v1 with the current v2/Relay suite transparently."""
import argparse
import json
from pathlib import Path
import sys

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "experiments/adaptive_aggregation"))
from common import read_rows, write_csv, write_json, sha256_file
from analyze import area, routing_trace


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--reference", type=Path, required=True)
    p.add_argument("--current", type=Path, required=True)
    a = p.parse_args()
    old, new = a.reference.resolve(), a.current.resolve()
    out = new / "fourway_comparison"
    out.mkdir(exist_ok=True)
    sources = [
        ("Full (previous)", old, "full"),
        ("Adaptive v1", old, "adaptive"),
        ("Adaptive v2", new, "adaptive"),
        ("Relay", new, "monoxide"),
    ]
    records, queues, policies, traces = [], {}, {}, {}
    digest = None
    signatures = {}
    conditions = None
    for label, root, mode in sources:
        case = root / f"shards-24/repeat-01/{mode}"
        m = json.loads((case / "manifest.json").read_text())
        s = json.loads((case / "summary.json").read_text())
        v = json.loads((case / "validation.json").read_text())
        if (
            v["completed_originals"] != v["expected"]
            or v["duplicate_originals"]
            or v["warning_error_count"]
            or sha256_file(case / "config.yaml") != m["config_sha256"]
        ):
            raise ValueError(f"run validation failed: {case}")
        if digest is None:
            digest = m["dataset_sha256"]
        if digest != m["dataset_sha256"]:
            raise ValueError("datasets differ")
        signature = {
            k: m["settings"][k]
            for k in (
                "shards",
                "tx_number",
                "per_shard_rate",
                "block_limit",
                "block_interval_ms",
                "beacon_interval_ms",
                "window_ms",
                "freeze_high_ms",
                "freeze_low_ms",
                "cooldown_ms",
                "beacon_work_us",
            )
        }
        if conditions is None:
            conditions = signature
        if conditions != signature:
            raise ValueError("nominal workload settings differ")
        signatures[label] = m["binary_sha256"]
        record = dict(label=label, source=str(case), **s)
        records.append(record)
        if mode == "monoxide":
            continue
        batches = list(read_rows(case / "results/aggregation_batch_metrics.csv"))
        if any(int(r["ExperimentServiceTimeNs"]) for r in batches):
            raise ValueError("artificial service cost found")
        q = [
            dict(
                t=(int(r["ObservedAtUnixNano"]) - s["start_ns"]) / 1e9,
                wait=int(r["FrozenQueueWaitNs"]) / 1e9,
                queued=int(r["QueuedOriginals"]),
                destinations=int(r["AggregationDestinations"]),
            )
            for r in read_rows(case / "results/aggregation_queue_timeseries.csv")
        ]
        queues[label] = q
        record["sampled_queue_wait_peak_s"] = max(r["wait"] for r in q)
        record["queued_original_peak"] = max(r["queued"] for r in q)
        pp = []
        previous = 24
        for row in read_rows(case / "results/aggregation_routing_policy.csv"):
            count = len(row["DestinationShards"].split(";"))
            pp.append(
                dict(
                    label=label,
                    version=int(row["Version"]),
                    time_s=(int(row["IssuedAtUnixNano"]) - s["start_ns"]) / 1e9,
                    before=previous,
                    after=count,
                    reason=row["Reason"],
                    wait_s=int(row["FrozenQueueWaitNs"]) / 1e9,
                    baseline=row.get("RecoveryBaselinePayments", ""),
                    observed=row.get("RecoveryObservedPayments", ""),
                )
            )
            previous = count
        policies[label] = pp
        record["overload_changes"] = sum(
            r["reason"] == "frozen_queue_overload" for r in pp
        )
        record["recovery_changes"] = sum(
            r["reason"] == "frozen_queue_recovered" for r in pp
        )
        traces[label] = routing_trace(case, s["start_ns"])
    if signatures["Adaptive v2"] != signatures["Relay"]:
        raise ValueError("new runs use different binaries")
    if signatures["Full (previous)"] != signatures["Adaptive v1"]:
        raise ValueError("historical pair uses different binaries")
    horizon = min(r["injection_span_s"] for r in records if r["label"] in queues)
    fields = list(dict.fromkeys(k for r in records for k in r))
    for r in records:
        if r["label"] in queues:
            r["wait_area_common_input_s2"] = area(queues[r["label"]], horizon)
    fields = list(dict.fromkeys(k for r in records for k in r))
    write_csv(
        out / "fourway_results.csv", [{k: r.get(k) for k in fields} for r in records]
    )
    write_csv(
        out / "switching_timeline.csv", [r for pp in policies.values() for r in pp]
    )
    write_json(
        out / "comparison_audit.json",
        dict(
            same_dataset=True,
            nominal_settings=conditions,
            same_binaries_within_new_pair=True,
            historical_binary_comparison=signatures["Adaptive v2"]
            == signatures["Adaptive v1"],
            common_input_horizon_s=horizon,
            repeats=1,
        ),
    )
    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "DejaVu Serif"],
            "font.size": 10,
            "axes.spines.top": False,
            "axes.spines.right": False,
            "legend.frameon": False,
            "pdf.fonttype": 42,
            "savefig.dpi": 300,
        }
    )
    labels = [r["label"] for r in records]
    colors = ["#0072B2", "#E69F00", "#D55E00", "#009E73"]
    fig, axes = plt.subplots(1, 3, figsize=(12, 4.1))
    for ax, key, title, scale in zip(
        axes,
        ("logical_tps", "latency_mean_ms", "latency_p95_ms"),
        ("Throughput (original tx/s)", "Mean latency (s)", "P95 latency (s)"),
        (1, 1000, 1000),
    ):
        values = [r[key] / scale for r in records]
        ax.bar(range(4), values, color=colors, width=0.65)
        for i, value in enumerate(values):
            ax.text(
                i, value + max(values) * 0.02, f"{value:,.2f}", ha="center", fontsize=9
            )
        ax.set_xticks(
            range(4), ["Full\n(previous)", "Adaptive\nv1", "Adaptive\nv2", "Relay"]
        )
        ax.set_ylabel(title)
        ax.set_ylim(0, max(values) * 1.17)
        ax.grid(axis="y", alpha=0.15)
        ax.set_axisbelow(True)
    fig.text(
        0.5,
        0.015,
        "24 shards, 1,000,000 positive-value originals; one run each. Full/v1 are historical binaries; v2/Relay share new binaries.",
        ha="center",
        fontsize=8,
    )
    fig.tight_layout(rect=(0, 0.07, 1, 1))
    for suffix in ("png", "pdf", "svg"):
        fig.savefig(out / f"fourway_performance.{suffix}", bbox_inches="tight")
    plt.close(fig)
    fig, axes = plt.subplots(2, 2, figsize=(11, 7))
    for i, (label, q) in enumerate(queues.items()):
        t = [r["t"] for r in q]
        axes[0, 0].step(
            t, [r["wait"] for r in q], where="post", label=label, color=colors[i]
        )
        axes[0, 1].step(
            t,
            [r["queued"] / 1000 for r in q],
            where="post",
            label=label,
            color=colors[i],
        )
        axes[1, 0].step(
            t,
            [r["destinations"] for r in q],
            where="post",
            label=label,
            color=colors[i],
        )
    for i, label in enumerate(("Adaptive v1", "Adaptive v2"), 1):
        trace = sorted(traces[label], key=lambda r: r["t"])
        axes[1, 1].step(
            [0] + [r["t"] for r in trace],
            np.r_[0, np.cumsum([r["eligible_relay2_sent"] for r in trace])] / 1000,
            where="post",
            label=label,
            color=colors[i],
        )
    for ax, ylabel in zip(
        axes.flat,
        (
            "Oldest frozen-queue wait (s)",
            "Queued originals (thousands)",
            "Aggregation destinations",
            "Cumulative Relay2 sent (thousands)",
        ),
    ):
        ax.set_xlabel("Time since first original creation (s)")
        ax.set_ylabel(ylabel)
        ax.grid(alpha=0.15)
        ax.set_xlim(left=0)
        ax.legend(fontsize=8)
    axes[1, 0].set_yticks([6, 12, 18, 24])
    axes[1, 0].set_ylim(0, 27)
    fig.text(
        0.5,
        0.01,
        "Queue excludes the active service/consensus window. Timelines aligned by first creation, not identical realized arrival schedules.",
        ha="center",
        fontsize=8,
    )
    fig.tight_layout(rect=(0, 0.07, 1, 1))
    for suffix in ("png", "pdf", "svg"):
        fig.savefig(out / f"queue_and_switching.{suffix}", bbox_inches="tight")
    plt.close(fig)
    lines = [
        "# 24 分片百万笔四组统计",
        "",
        "各组完成 100 万笔，审计无重复/告警。名义工作负载相同，数据 SHA256 一致；旧全聚合/旧自适应为历史二进制，新自适应/Relay 共享新二进制，不将四组称为同代码配对。",
        "",
        "| 方案 | TPS | 平均/s | P95/s | P99/s | 完成跨度/s | 实际注入跨度/s | Relay2 |",
        "|---|---:|---:|---:|---:|---:|---:|---:|",
    ]
    lines.extend(
        f"| {r['label']} | {r['logical_tps']:.2f} | {r['latency_mean_ms']/1000:.3f} | {r['latency_p95_ms']/1000:.3f} | {r['latency_p99_ms']/1000:.3f} | {r['duration_s']:.3f} | {r['injection_span_s']:.3f} | {r['Relay2TxCount']} |"
        for r in records
    )
    lines.extend(
        [
            "",
            "## 新版切换",
            "",
            "| 时间/s | 目标变化 | 排队等待/s | 基准交易量 | 比较交易量 |",
            "|---:|---|---:|---:|---:|",
        ]
    )
    lines.extend(
        f"| {r['time_s']:.3f} | {r['before']}→{r['after']} | {r['wait_s']:.3f} | {r['baseline']} | {r['observed']} |"
        for r in policies["Adaptive v2"]
    )
    lines.extend(
        [
            "",
            "## 解释",
            "",
            "新版只降载、不恢复，符合抑制旧版反复恢复的目标；但不等同于性能更好。新版与旧版 TPS 接近，平均与尾部时延更高，窗口排队也更严重。纯 Relay 在本次名义负载下有最高吞吐和最低时延，因此不能宣称聚合自适应在该配置下优于 Relay。",
            "",
            "新版仍减少目标端交易条目，但目标端压缩收益并不必然抵消 Beacon 的协调瓶颈。队列原交易数少并不代表等待短，因为批次大小、等待年龄和处理耗时不同。",
            "",
            "实际输入节奏有差异，各组仅一次；不能把差异直接归因于采样成本或恢复规则，应交替顺序重复实验。源端交易量统计通过源块回执在 Beacon 观察到，并非一个绝对相同的回放时间表。",
            "",
            "![性能比较](fourway_performance.png)",
            "",
            "![队列与切换](queue_and_switching.png)",
        ]
    )
    (out / "report.md").write_text("\n".join(lines) + "\n")
    print(out)


if __name__ == "__main__":
    main()
