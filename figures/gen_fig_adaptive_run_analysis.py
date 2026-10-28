#!/usr/bin/env python3
"""Inspect a completed full/adaptive pair and export its observed timeline."""
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
from analyze import area, comparable_config, routing_trace


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("suite", type=Path)
    a = p.parse_args()
    root = a.suite.resolve()
    out = root / "analysis"
    out.mkdir(exist_ok=True)
    summaries, queues, manifests, policies = {}, {}, {}, []
    for mode in ("full", "adaptive"):
        case = root / f"shards-24/repeat-01/{mode}"
        s = json.loads((case / "summary.json").read_text())
        m = json.loads((case / "manifest.json").read_text())
        if sha256_file(case / "config.yaml") != m["config_sha256"]:
            raise ValueError("saved config changed")
        batches = list(read_rows(case / "results/aggregation_batch_metrics.csv"))
        if any(int(b["ExperimentServiceTimeNs"]) for b in batches):
            raise ValueError("injected Beacon service cost present")
        summaries[mode], manifests[mode] = s, m
        queues[mode] = [
            dict(
                t=(int(r["ObservedAtUnixNano"]) - s["start_ns"]) / 1e9,
                wait=int(r["FrozenQueueWaitNs"]) / 1e9,
                queued=int(r["QueuedOriginals"]),
                destinations=int(r["AggregationDestinations"]),
            )
            for r in read_rows(case / "results/aggregation_queue_timeseries.csv")
        ]
        if mode == "adaptive":
            previous = 24
            for r in read_rows(case / "results/aggregation_routing_policy.csv"):
                count = len(r["DestinationShards"].split(";"))
                policies.append(
                    dict(
                        version=int(r["Version"]),
                        time_s=(int(r["IssuedAtUnixNano"]) - s["start_ns"]) / 1e9,
                        before=previous,
                        after=count,
                        reason=r["Reason"],
                        wait_s=int(r["FrozenQueueWaitNs"]) / 1e9,
                    )
                )
                previous = count
    if any(
        manifests["full"][k] != manifests["adaptive"][k]
        for k in ("dataset_sha256", "binary_sha256")
    ):
        raise ValueError("different dataset or binaries")
    if comparable_config(root / "shards-24/repeat-01/full") != comparable_config(
        root / "shards-24/repeat-01/adaptive"
    ):
        raise ValueError(
            "different configs beyond minimum aggregation destination percentage"
        )
    f, s = summaries["full"], summaries["adaptive"]
    performance = []
    for key, unit, scale in (
        ("logical_tps", "tx/s", 1),
        ("latency_mean_ms", "s", 1000),
        ("latency_p95_ms", "s", 1000),
        ("latency_p99_ms", "s", 1000),
        ("duration_s", "s", 1),
        ("service_queue_wait_mean_ms", "s", 1000),
        ("service_queue_wait_max_ms", "s", 1000),
        ("receiver_updates", "updates", 1),
        ("destination_entries", "entries", 1),
    ):
        performance.append(
            dict(
                metric=key,
                unit=unit,
                full=f[key] / scale,
                adaptive=s[key] / scale,
                change_percent=100 * (s[key] / f[key] - 1),
            )
        )
    horizon = min(f["injection_span_s"], s["injection_span_s"])
    peaks = []
    for mode, q in queues.items():
        peak_wait = max(q, key=lambda r: r["wait"])
        peak_work = max(q, key=lambda r: r["queued"])
        peaks.append(
            dict(
                mode=mode,
                wait_peak_s=peak_wait["wait"],
                wait_peak_at_s=peak_wait["t"],
                queued_peak=peak_work["queued"],
                queued_peak_at_s=peak_work["t"],
                injection_horizon_s=horizon,
                wait_area_s2=area(q, horizon),
            )
        )
    write_csv(out / "performance_comparison.csv", performance)
    write_csv(out / "policy_timeline.csv", policies)
    write_csv(out / "queue_comparison.csv", peaks)
    trace = routing_trace(root / "shards-24/repeat-01/adaptive", s["start_ns"])
    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "DejaVu Serif"],
            "font.size": 10,
            "axes.spines.top": False,
            "axes.spines.right": False,
            "pdf.fonttype": 42,
            "svg.fonttype": "none",
            "savefig.dpi": 300,
            "legend.frameon": False,
        }
    )
    fig, axes = plt.subplots(2, 2, figsize=(11, 7.4))
    colors = {"full": "#0072B2", "adaptive": "#D55E00"}
    for mode, q in queues.items():
        t = [r["t"] for r in q]
        axes[0, 0].step(
            t,
            [r["wait"] for r in q],
            where="post",
            label=mode.capitalize(),
            color=colors[mode],
        )
        axes[0, 1].step(
            t,
            [r["queued"] / 1000 for r in q],
            where="post",
            label=mode.capitalize(),
            color=colors[mode],
        )
        for ax in axes.flat:
            ax.axvline(
                summaries[mode]["injection_span_s"],
                color=colors[mode],
                ls=":",
                lw=1,
                alpha=0.7,
            )
    axes[0, 0].axhline(0.5, color="#999999", ls="--", lw=0.8)
    axes[0, 0].set_ylabel("Oldest frozen-queue wait (s)")
    axes[0, 1].set_ylabel("Queued original transactions (thousands)")
    for i in (0, 1):
        axes[0, i].legend(loc="upper left")
    q = queues["adaptive"]
    axes[1, 0].step(
        [r["t"] for r in q],
        [r["destinations"] for r in q],
        where="post",
        color=colors["adaptive"],
    )
    axes[1, 0].set_yticks([6, 12, 18, 24])
    axes[1, 0].set_ylim(0, 28)
    axes[1, 0].set_ylabel("Aggregation destination shards")
    for pol in policies:
        axes[1, 0].annotate(
            f"v{pol['version']}",
            (pol["time_s"], pol["after"]),
            xytext=(3, 7),
            textcoords="offset points",
            fontsize=8,
        )
        for ax in axes[0, :]:
            ax.axvline(pol["time_s"], color=colors["adaptive"], alpha=0.16, lw=0.8)
    times = np.array([r["t"] for r in trace])
    counts = np.array([r["eligible_relay2_sent"] for r in trace])
    order = np.argsort(times)
    axes[1, 1].step(
        np.r_[0, times[order]],
        np.r_[0, np.cumsum(counts[order])] / 1000,
        where="post",
        color=colors["adaptive"],
        label="Adaptive",
    )
    axes[1, 1].axhline(0, color=colors["full"], label="Full")
    axes[1, 1].set_ylabel("Cumulative ordinary Relay2 sent (thousands)")
    axes[1, 1].legend()
    for ax in axes.flat:
        ax.grid(alpha=0.17)
        ax.set_xlim(0, max(f["duration_s"], s["duration_s"]) * 1.06)
        ax.set_xlabel("Time since first transaction creation (s)")
    fig.text(
        0.5,
        0.012,
        "1,000,000 positive-value transactions, 24 shards; one paired run. Dotted lines: each run's last input creation.\nQueue excludes the active service/consensus window; traffic observations are not repeat-run confidence intervals.",
        ha="center",
        fontsize=8,
    )
    fig.tight_layout(rect=(0, 0.08, 1, 1))
    for suffix in ("png", "pdf", "svg"):
        fig.savefig(out / f"adaptive_timeline.{suffix}", bbox_inches="tight")
    plt.close(fig)
    write_json(
        out / "verified_observations.json",
        dict(
            no_artificial_service_cost=True,
            identical_code_and_dataset=True,
            performance=performance,
            policy_timeline=policies,
            queue_peaks=peaks,
            relay2_sent_after_policy=sum(
                r["eligible_relay2_sent"] for r in trace if r["version"] > 0
            ),
        ),
    )
    last_policy = policies[-1] if policies else None
    last_relay = sum(
        r["eligible_relay2_sent"]
        for r in trace
        if last_policy and r["version"] == last_policy["version"]
    )
    recoveries = [r for r in policies if r["reason"] == "frozen_queue_recovered"]
    recovery_note = (
        f"本次在 {recoveries[0]['time_s']:.2f}s 恢复全聚合后又发生降载，不能称为持续稳定消除拥堵。"
        if recoveries
        else "未观察到恢复策略，需结合队列曲线判断持续性。"
    )
    lines = [
        "# 24 分片百万笔：全聚合与自适应对比",
        "",
        f"数据目录：{root}",
        "",
        "两组均完成 1000000 笔正金额非合约原交易，无重复，无 WARN/ERROR。数据与二进制哈希一致，模拟服务成本为零，配置仅最低聚合目的分片比例不同。",
        "",
        "| 指标 | 全聚合 | 自适应 | 变化 |",
        "|---|---:|---:|---:|",
    ]
    lines.extend(
        f"| {r['metric']} ({r['unit']}) | {r['full']:,.3f} | {r['adaptive']:,.3f} | {r['change_percent']:+.2f}% |"
        for r in performance
    )
    lines.extend(
        [
            "",
            "## 策略时序",
            "",
            "| 相对时间/s | 版本 | 聚合目标分片 | 触发时等待/s | 原因 |",
            "|---:|---:|---|---:|---|",
        ]
    )
    lines.extend(
        f"| {r['time_s']:.3f} | {r['version']} | {r['before']} → {r['after']} | {r['wait_s']:.3f} | {r['reason']} |"
        for r in policies
    )
    lines.extend(
        [
            "",
            "## 结论与限制",
            "",
            f"策略变化 {len(policies)} 次。普通 Relay2 提交数为 {s['Relay2TxCount']}，零金额 Relay2 为 {s.get('ZeroValueRelay2TxCount',0)}。源端守恒：{s['Relay1TxCount']} = {s['aggregation_originals']} + {s['Relay2TxCount']}。"
            + recovery_note,
            "",
            f"共同注入时间范围为 {horizon:.3f}s，冻结队列最老等待曲线面积 full={peaks[0]['wait_area_s2']:.3f} s²，adaptive={peaks[1]['wait_area_s2']:.3f} s²。自适应的峰值等待与排队原交易数也更低。运行结束后的最终排空不能单独作为机制收益证据。",
            "",
            (
                f"最后一次 {last_policy['before']}→{last_policy['after']} 在 {last_policy['time_s']:.2f}s；自适应最后输入创建时刻为 {s['injection_span_s']:.2f}s。v{last_policy['version']} 源块发送 {last_relay} 笔正金额普通 Relay2。需要区分输入期间与输入结束后的变化。"
                if last_policy
                else "未发生策略变化。"
            ),
            "",
            f"每组仅一次运行；实际输入创建跨度 full={f['injection_span_s']:.3f}s、adaptive={s['injection_span_s']:.3f}s，配置相同并不表示实际到达节奏完全一致。TPS 使用最早原交易创建到最晚最终确认的跨度计算，并非配置注入速率。结果支持本次负载下的功能生效与性能改善，但不支持统计显著性或稳定性保证，建议交替顺序重复 3–5 次。",
            "",
            f"目标端成本：目标交易条目 {f['destination_entries']} → {s['destination_entries']}；接收方更新 {f['receiver_updates']} → {s['receiver_updates']}（{100*(s['receiver_updates']/f['receiver_updates']-1):+.2f}%）。应在吞吐/时延收益之外同时报告聚合压缩收益的变化。",
            "",
            "![队列与策略时间线](adaptive_timeline.png)",
        ]
    )
    (out / "report.md").write_text("\n".join(lines) + "\n")
    print(out)


if __name__ == "__main__":
    main()
