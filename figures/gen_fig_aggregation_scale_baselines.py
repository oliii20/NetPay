#!/usr/bin/env python3
"""Reproduce the four-method scaling comparison from existing completed runs."""

import csv
import json
import statistics
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np
import yaml


ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "figures/aggregation-scale-baselines-full-plus-24-hot75"
SHARDS = (4, 8, 16, 24)
METHODS = ("Monoxide", "BrokerChain", "CLPA+Relay", "Proposed")
STYLE = {
    "Monoxide": ("#0072B2", ""),
    "BrokerChain": ("#E69F00", "//"),
    "CLPA+Relay": ("#CC79A7", ".."),
    "Proposed": ("#D55E00", ""),
}


def read_csv(path):
    with path.open(newline="", encoding="utf-8") as handle:
        return list(csv.DictReader(handle))


def write_csv(name, rows):
    with (OUT / name).open("w", newline="", encoding="utf-8") as handle:
        writer = csv.DictWriter(handle, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)


def collect():
    sources = [
        ("aggregation-scale-beacon1000-repeat3", "scaling_runs.csv", {"relay": "Monoxide", "full_aggregation": "Proposed"}, {4, 8, 16}),
        ("aggregation-24shard-recheck-v1", "scaling_runs.csv", {"relay": "Monoxide", "hot_destination_75": "Proposed"}, {24}),
        ("aggregation-aligned-baselines-interval20-hot5-v3", "baseline_runs.csv", {"static_broker": "BrokerChain", "clpa_relay": "CLPA+Relay"}, {4, 8, 16}),
        ("aggregation-24shard-baselines-recheck-v1", "baseline_runs.csv", {"static_broker": "BrokerChain", "clpa_relay": "CLPA+Relay"}, {24}),
    ]
    records, audit = [], []
    for family, filename, mapping, selected in sources:
        source = ROOT / "exp" / family / filename
        for row in read_csv(source):
            shards, method = int(row["shards"]), row["method"]
            if shards not in selected or method not in mapping:
                continue
            repeat = row.get("repeat", "single")
            run = source.parent / f"shards-{shards}"
            if repeat != "single":
                run = run / repeat
            config_path = run / method / "config.yaml"
            config = yaml.safe_load(config_path.read_text())
            system, node, supervisor = (config[key] for key in ("system", "consensus_node", "supervisor"))
            assert int(row["completed_txs"]) == 300000
            assert int(system["shard_num"]) == shards
            assert system["node_num"] == 4 and system["limit"] == 2000
            assert node["block_interval"] == 2000
            assert supervisor["tx_number"] == 300000
            assert supervisor["tx_injection_speed"] == shards * 600
            assert supervisor["tx_source"]["exclude_contract_txs"] is True
            assert supervisor["tx_source"]["tx_source_file"] == "./data/25250000to25499999_BlockTransaction.csv"
            assert np.isclose(float(row["logical_tps"]), 300000 / float(row["duration_s"]))
            if method == "clpa_relay":
                assert supervisor["epoch_duration"] == 20
            records.append(dict(shards=shards, method=mapping[method], repeat=repeat,
                                logical_tps=float(row["logical_tps"]),
                                latency_mean_s=float(row["latency_mean_ms"]) / 1000,
                                completed_txs=int(row["completed_txs"]),
                                source=str(source.relative_to(ROOT)), run_method=method))
            audit.append(dict(shards=shards, method=mapping[method], repeat=repeat,
                              config=str(config_path.relative_to(ROOT)),
                              consensus_type=system["consensus_type"],
                              nodes_per_shard=system["node_num"], block_capacity=system["limit"],
                              block_interval_ms=node["block_interval"],
                              injection_speed=supervisor["tx_injection_speed"],
                              clpa_interval_s=supervisor["epoch_duration"] if method == "clpa_relay" else "N/A"))
    selection = json.loads((ROOT / "exp/aggregation-24shard-recheck-v1/shards-24/repeat-01/hot_destination_75/selection.json").read_text())
    assert selection["k"] == 18 and selection["percent"] == 75
    return records, audit, selection


def summarize(records):
    rows = []
    for shards in SHARDS:
        for method in METHODS:
            group = [row for row in records if row["shards"] == shards and row["method"] == method]
            expected = 3 if shards < 24 and method in ("Monoxide", "Proposed") else 1
            assert len(group) == expected, (shards, method, len(group))
            result = dict(shards=shards, method=method, n=len(group))
            for metric in ("logical_tps", "latency_mean_s"):
                values = [row[metric] for row in group]
                result[metric] = statistics.mean(values)
                result[metric + "_std"] = statistics.stdev(values) if len(values) > 1 else ""
            rows.append(result)
    return rows


def draw(rows):
    plt.rcParams.update({"font.family": "serif", "font.serif": ["Times New Roman", "DejaVu Serif"],
                         "font.size": 10, "axes.spines.top": False, "axes.spines.right": False,
                         "axes.axisbelow": True, "pdf.fonttype": 42, "ps.fonttype": 42,
                         "savefig.dpi": 300})
    fig, axes = plt.subplots(1, 2, figsize=(9.2, 3.7))
    width, x = 0.19, np.arange(4)
    for i, method in enumerate(METHODS):
        selected = [row for row in rows if row["method"] == method]
        color, hatch = STYLE[method]
        for ax, metric in zip(axes, ("logical_tps", "latency_mean_s")):
            values = [row[metric] for row in selected]
            position = x + (i - 1.5) * width
            ax.bar(position, values, width * 0.95, label=method, color=color,
                   hatch=hatch, edgecolor="white", linewidth=0.6)
            indices = [j for j, row in enumerate(selected) if row["n"] > 1]
            if indices:
                ax.errorbar(position[indices], [values[j] for j in indices],
                            yerr=[selected[j][metric + "_std"] for j in indices],
                            fmt="none", ecolor="#333333", capsize=2, elinewidth=0.8)
            ax.set_xticks(x, ["4", "8", "16", "24"])
            ax.set_xlabel("Number of shards")
            ax.grid(axis="y", alpha=0.18)
    for ax, metric in zip(axes, ("logical_tps", "latency_mean_s")):
        upper = max(row[metric] + (row[metric + "_std"] or 0) for row in rows)
        ax.set_ylim(0, upper * 1.12)
    axes[0].set_ylabel("Logical TPS (tx/s)")
    axes[1].set_ylabel("Mean confirmation latency (s)")
    axes[0].set_title("(a) Throughput", fontweight="bold")
    axes[1].set_title("(b) Latency", fontweight="bold")
    fig.legend(*axes[0].get_legend_handles_labels(), loc="upper center", ncol=4,
               frameon=False, bbox_to_anchor=(0.5, 1.01))
    fig.text(0.5, 0.065, "Proposed: full aggregation at 4/8/16 shards; 18/24 destination shards (75%) at 24 shards.\n"
             "Mean ± SD for Monoxide/Proposed at 4/8/16 (n=3); all other points are single runs.",
             ha="center", va="center", fontsize=8, color="#444444")
    fig.tight_layout(rect=(0, 0.16, 1, 0.91), w_pad=2)
    for suffix in ("png", "pdf", "svg"):
        fig.savefig(OUT / f"fig_tps_latency_comparison.{suffix}", bbox_inches="tight")
    plt.close(fig)


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    records, audit, selection = collect()
    rows = summarize(records)
    write_csv("selected_runs.csv", records)
    write_csv("comparison_summary.csv", rows)
    write_csv("configuration_audit.csv", audit)
    draw(rows)
    note = """# Four-method scaling comparison

TPS is completed original transactions / (last final commit - first original creation),
using logical_tps from each source summary. Latency is mean original-transaction
creation-to-final-commit latency, converted from latency_mean_ms to seconds.
These are finite-workload completion measurements. No epoch-TPS values are mixed in.

All selected runs completed 300000 transactions. Configurations were checked for
the same dataset path, contract filtering, four nodes per shard, block capacity
2000, shard block interval 2000 ms, and injection rate 600 tx/s per shard.
CLPA denotes CLPA+Relay with a 20-second partition interval. BrokerChain uses
static_broker from the hot5-v3 baselines (4/8/16) and the latest recheck (24).
Monoxide uses the relay method with aggregation disabled.

Proposed preserves the previous plot's data: full aggregation for 4/8/16,
hot_destination_75 from the latest recheck for 24. The 75% scope selects 18/24
destination shards; actual cross-shard transaction coverage is COVERAGE.

Monoxide and Proposed each have three repeats at 4/8/16; error bars are sample SD.
Other points have one measurement; their SD fields are blank and no error bars
are drawn. Cross-batch selection and unequal repeat counts are retained explicitly
in selected_runs.csv and comparison_summary.csv. This config audit does not
establish identical executable revisions or runtime conditions across batches.

Reproduce with:
MPLCONFIGDIR=/tmp/codex-mpl-cache /opt/anaconda3/bin/python3 figures/gen_fig_aggregation_scale_baselines.py
"""
    (OUT / "README.md").write_text(note.replace("COVERAGE", f"{selection['coverage']:.4%}"))
    for row in rows:
        print(f"{row['shards']:2} {row['method']:12} n={row['n']} TPS={row['logical_tps']:.2f} latency={row['latency_mean_s']:.3f}s")


if __name__ == "__main__":
    main()
