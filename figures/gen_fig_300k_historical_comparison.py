#!/usr/bin/env python3
"""Compare current 300k adaptive run with explicitly labeled historical builds."""
import argparse
import csv
import json
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--current", required=True, type=Path)
    parser.add_argument("--reference", required=True, type=Path)
    args = parser.parse_args()
    current, reference = args.current.resolve(), args.reference.resolve()
    out = current / "figures"
    out.mkdir(exist_ok=True)
    cases = [
        reference / "shards-24/repeat-01/full",
        current,
        reference / "shards-24/repeat-01/monoxide",
    ]
    labels = [
        "Full aggregation\nHistorical / build A",
        "Adaptive\nCurrent / build B",
        "Pure Relay\nHistorical / build A",
    ]
    colors = ["#0072B2", "#D55E00", "#009E73"]
    manifests, summaries, records = [], [], []
    conditions = None
    for case, label in zip(cases, labels):
        manifest = json.loads((case / "manifest.json").read_text())
        summary = json.loads((case / "summary.json").read_text())
        validation = json.loads((case / "validation.json").read_text())
        assert validation["completed_originals"] == validation["expected"] == 300000
        assert (
            validation["duplicate_originals"] == validation["warning_error_count"] == 0
        )
        settings = manifest["settings"]
        signature = {
            k: settings[k]
            for k in [
                "shards",
                "tx_number",
                "per_shard_rate",
                "block_limit",
                "block_interval_ms",
                "beacon_interval_ms",
                "window_ms",
            ]
        }
        signature["dataset_sha256"] = manifest["dataset_sha256"]
        if conditions is None:
            conditions = signature
        assert conditions == signature
        manifests.append(manifest)
        summaries.append(summary)
        records.append(
            dict(
                method=label.replace("\n", " / "),
                source=str(case),
                tps=summary["logical_tps"],
                mean_s=summary["latency_mean_ms"] / 1000,
                p95_s=summary["latency_p95_ms"] / 1000,
                p99_s=summary["latency_p99_ms"] / 1000,
                beacon_sha256=manifest["binary_sha256"]["beaconnode"],
            )
        )
    assert manifests[0]["binary_sha256"] == manifests[2]["binary_sha256"]
    assert manifests[0]["binary_sha256"] != manifests[1]["binary_sha256"]
    assert summaries[1]["policy_versions"] == summaries[1]["Relay2TxCount"] == 0
    with (out / "comparison_data.csv").open("w", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=list(records[0]))
        writer.writeheader()
        writer.writerows(records)
    (out / "version_audit.json").write_text(
        json.dumps(
            dict(
                conditions=conditions,
                same_binaries=False,
                repeats=1,
                historical_build_A=manifests[0]["binary_sha256"],
                current_build_B=manifests[1]["binary_sha256"],
                current_adaptive_switched=False,
                sources=[str(c) for c in cases],
            ),
            indent=2,
        )
    )

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
    fig, axes = plt.subplots(1, 2, figsize=(12.8, 5.4))
    x = np.arange(3)
    bars = axes[0].bar(x, [r["tps"] for r in records], color=colors, width=0.62)
    # Hatch historical results so build differences are visible in grayscale.
    for i in [0, 2]:
        bars[i].set_hatch("//")
        bars[i].set_edgecolor("white")
    axes[0].bar_label(bars, labels=[f"{r['tps']:,.0f}" for r in records], padding=5)
    axes[0].set(
        xticks=x,
        xticklabels=labels,
        ylabel="Throughput (original tx/s)",
        ylim=(0, 8500),
        title="(a) Throughput",
    )
    for i, (r, label, color) in enumerate(zip(records, labels, colors)):
        values = [r[k] for k in ["mean_s", "p95_s", "p99_s"]]
        bars = axes[1].bar(
            x + (i - 1) * 0.24,
            values,
            0.23,
            color=color,
            label=label.replace("\n", " — "),
            edgecolor="white",
            hatch="//" if i != 1 else None,
        )
        axes[1].bar_label(
            bars, labels=[f"{v:.2f}" for v in values], fontsize=9, padding=3
        )
    axes[1].set(
        xticks=x,
        xticklabels=["Mean", "P95", "P99"],
        ylabel="Confirmation latency (s)",
        ylim=(0, 55),
        title="(b) End-to-end latency",
    )
    axes[1].legend(loc="upper left", fontsize=8.5)
    for ax in axes:
        ax.grid(axis="y", alpha=0.18)
        ax.set_axisbelow(True)
    fig.suptitle(
        "24 shards | 300,000 valid transactions | 600 tx/s/shard | block limit 2,000",
        fontsize=13,
    )
    old_hash = manifests[0]["binary_sha256"]["beaconnode"][:12]
    new_hash = manifests[1]["binary_sha256"]["beaconnode"][:12]
    fig.text(
        0.5,
        0.112,
        f"Different code builds: A = {old_hash}…; B = {new_hash}… (Beacon binary SHA-256).",
        ha="center",
        fontsize=10,
        weight="bold",
        color="#663B20",
    )
    fig.text(
        0.5,
        0.065,
        "Historical comparison, not a same-build ablation; one run per method, no error bars.",
        ha="center",
        fontsize=9,
        color="#555555",
    )
    fig.text(
        0.5,
        0.025,
        "Current adaptive run: no load shedding or recovery; all cross-shard transactions remained aggregated.",
        ha="center",
        fontsize=9,
        color="#555555",
    )
    fig.tight_layout(rect=(0, 0.17, 1, 0.93))
    for ext in ["png", "pdf", "svg"]:
        fig.savefig(out / f"performance_comparison_300k.{ext}", bbox_inches="tight")
    plt.close(fig)
    (out / "README.md").write_text(
        "# 30 万笔自适应与历史 600 档基线\n\n"
        "performance_comparison_300k 提供 PNG、PDF、SVG。comparison_data.csv 保存绘图数据。\n\n"
        "图中 A 为历史全聚合/纯 Relay，B 为本次自适应；二进制哈希不同，具体 SHA256 见 version_audit.json。\n"
        "各组数据文件、交易量、分片数、配置注入速率、区块上限与窗口/出块间隔一致，但不是同版本消融。\n"
        "本次没有降载或恢复；每组仅一次，不绘制误差条。不能将性能差值全部归因于自适应或代码优化。\n"
    )
    print(out)


if __name__ == "__main__":
    main()
