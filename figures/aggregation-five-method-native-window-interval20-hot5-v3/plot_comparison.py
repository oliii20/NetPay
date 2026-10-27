#!/usr/bin/env python3
"""Rebuild the five-method comparison with the completed interval-20 v3 baselines."""

import csv
import json
import math
import statistics
from datetime import datetime
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np
import yaml


ROOT = Path(__file__).resolve().parents[2]
OUT = Path(__file__).resolve().parent
NEW_BASE = ROOT / "exp/aggregation-aligned-baselines-interval20-hot5-v3"
OLD_BASE = ROOT / "exp/aggregation-aligned-baselines-v1"
HOT = ROOT / "exp/aggregation-hot-destination-scale-v1"
BATCH = ROOT / "exp/aggregation-scale-beacon1000-repeat3"
SHARDS = (4, 8, 16, 24)
METHODS = ("Monoxide", "BrokerChain", "CLPA+Relay", "Batch-only", "Proposed")
STYLES = {
    "Monoxide": dict(color="#0072B2", marker="o", linestyle="--", linewidth=1.8),
    "BrokerChain": dict(color="#E69F00", marker="s", linestyle="--", linewidth=1.8),
    "CLPA+Relay": dict(color="#CC79A7", marker="^", linestyle="--", linewidth=1.8),
    "Batch-only": dict(color="#009E73", marker="D", linestyle="-.", linewidth=1.9),
    "Proposed": dict(color="#D55E00", marker="*", linestyle="-", linewidth=2.5),
}


def read_csv(path):
    with path.open(newline="") as handle:
        return list(csv.DictReader(handle))


def write_csv(path, rows, fields=None):
    if not rows:
        raise ValueError(f"refusing to write empty CSV: {path}")
    with path.open("w", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=fields or list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)


def stamp(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00")).timestamp()


def run_path(shards, method, baseline_root=NEW_BASE):
    if method == "BrokerChain":
        return baseline_root / f"shards-{shards}/static_broker"
    if method == "CLPA+Relay":
        return baseline_root / f"shards-{shards}/clpa_relay"
    if method == "Batch-only":
        return BATCH / f"shards-{shards}/repeat-01/batch_only"
    family = HOT / f"shards{shards}-speed{shards * 600}" / f"shards-{shards}/repeat-01"
    if method == "Monoxide":
        return family / "relay"
    if method == "Proposed":
        return family / ("hot_destination_50" if shards == 24 else "full_aggregation")
    raise ValueError(f"unknown method: {method}")


def result_sources():
    batch_rows = {
        (int(row["shards"]), row["repeat"], row["method"]): row
        for row in read_csv(BATCH / "scaling_runs.csv")
    }
    sources = {}
    for shards in SHARDS:
        hot_path = HOT / f"shards{shards}-speed{shards * 600}/scaling_runs.csv"
        hot_rows = {row["method"]: row for row in read_csv(hot_path)}
        sources[(shards, "Monoxide")] = (
            hot_rows["relay"],
            hot_path,
            "relay",
        )
        sources[(shards, "Batch-only")] = (
            batch_rows[(shards, "repeat-01", "batch_only")],
            BATCH / "scaling_runs.csv",
            "batch_only",
        )
        proposed_method = "hot_destination_50" if shards == 24 else "full_aggregation"
        sources[(shards, "Proposed")] = (
            hot_rows[proposed_method],
            hot_path,
            proposed_method,
        )
    return sources


def build_comparison(baseline_root=NEW_BASE):
    baseline_rows = {
        (int(row["shards"]), row["method"]): row
        for row in read_csv(baseline_root / "baseline_runs.csv")
    }
    external = result_sources()
    comparison = []
    epoch_rows = []
    for shards in SHARDS:
        for method in METHODS:
            run = run_path(shards, method, baseline_root)
            is_aggregation = method in ("Batch-only", "Proposed")
            brief_name = (
                "broker_stats_brief_info.csv"
                if method == "BrokerChain"
                else "relay_stats_brief_info.csv"
            )
            brief = read_csv(run / "results" / brief_name)
            if is_aggregation and len(brief) != 1:
                raise ValueError(f"aggregation epoch allocation is ambiguous: {run}")

            extended_tps = []
            for epoch in brief:
                native_count = float(epoch["Total tx # in this epoch"])
                native_tps = float(epoch["Avg. TPS of this epoch (txs per second)"])
                extended_count = 300000.0 if is_aggregation else native_count
                logical_tps = (
                    native_tps * extended_count / native_count
                    if is_aggregation and native_count > 0
                    else native_tps
                )
                latency_col = next(key for key in epoch if key.startswith("Avg. TCL of this epoch ("))
                native_latency = float(epoch[latency_col]) / 1e9
                epoch_rows.append(
                    {
                        "shards": shards,
                        "method": method,
                        "epoch": int(epoch["EpochID"]),
                        "native_count": native_count,
                        "extended_logical_count": extended_count,
                        "native_tps": native_tps,
                        "logical_extended_tps": logical_tps,
                        "zero_transaction_epoch": native_count == 0,
                        "native_latency_s": native_latency,
                        "source": str((run / "results" / brief_name).relative_to(ROOT)),
                    }
                )
                extended_tps.append(logical_tps)

            if method in ("BrokerChain", "CLPA+Relay"):
                key = "static_broker" if method == "BrokerChain" else "clpa_relay"
                result = baseline_rows[(shards, key)]
                source = baseline_root / "baseline_runs.csv"
                run_method = key
            else:
                result, source, run_method = external[(shards, method)]

            completed = int(result["completed_txs"])
            speed = int(result.get("tx_speed", result.get("tx_injection_speed", shards * 600)))
            if completed != 300000 or speed != shards * 600:
                raise ValueError(f"incomplete or misaligned run: {shards}/{method}")
            comparison.append(
                {
                    "shards": shards,
                    "method": method,
                    "mean_epoch_tps": statistics.fmean(extended_tps),
                    "latency_mean_s": float(result["latency_mean_ms"]) / 1000,
                    "latency_p95_s": float(result["latency_p95_ms"]) / 1000,
                    "completion_tps": float(result["logical_tps"]),
                    "completed_txs": completed,
                    "epoch_count": len(brief),
                    "zero_transaction_epochs": sum(
                        float(epoch["Total tx # in this epoch"]) == 0 for epoch in brief
                    ),
                    "source": str(source.relative_to(ROOT)),
                    "run_method": run_method,
                    "injection_speed": speed,
                    "repeat": "01",
                    "tps_definition": (
                        "arithmetic mean of native epoch TPS; logical-count extension "
                        "for aggregation"
                    ),
                    "latency_definition": (
                        "mean original transaction creation-to-final-commit latency"
                    ),
                }
            )
    return comparison, epoch_rows


def audit_configuration():
    rows = []
    for shards in SHARDS:
        for method in METHODS:
            config_path = run_path(shards, method) / "config.yaml"
            with config_path.open() as handle:
                config = yaml.safe_load(handle)
            system = config["system"]
            node = config["consensus_node"]
            aggregation = config["aggregation"]
            supervisor = config["supervisor"]
            source = supervisor["tx_source"]
            beacon_interval = node["shard_block_intervals_ms"].get(2147483646)
            row = {
                "shards": shards,
                "method": method,
                "consensus_type": system["consensus_type"],
                "nodes_per_shard": system["node_num"],
                "block_capacity": system["limit"],
                "block_interval_ms": node["block_interval"],
                "beacon_interval_ms": beacon_interval,
                "tx_number": supervisor["tx_number"],
                "injection_speed": supervisor["tx_injection_speed"],
                "dataset": source["tx_source_file"],
                "exclude_contract_txs": source["exclude_contract_txs"],
                "aggregation_enabled": aggregation["enabled"],
                "batch_only": aggregation["batch_only"],
                "source_aggregate_abstracts": aggregation["source_aggregate_abstracts"],
                "credit_chunk_size": aggregation["credit_chunk_size"],
                "aggregation_window_ms": aggregation["max_window_duration_ms"],
                "clpa_interval_s": supervisor["epoch_duration"],
                "config": str(config_path.relative_to(ROOT)),
            }
            expected = {
                "nodes_per_shard": 4,
                "block_capacity": 2000,
                "block_interval_ms": 2000,
                "beacon_interval_ms": 1000,
                "tx_number": 300000,
                "injection_speed": shards * 600,
                "exclude_contract_txs": True,
            }
            for key, value in expected.items():
                if row[key] != value:
                    raise ValueError(f"configuration mismatch {config_path}: {key}={row[key]}")
            rows.append(row)

    broker_meta = json.loads((NEW_BASE / "selected_brokers.txt.json").read_text())
    brokers = broker_meta["brokers"]
    broker_lines = [line.strip() for line in (NEW_BASE / "selected_brokers.txt").read_text().splitlines()]
    if len(brokers) != 50 or len(set(broker_lines)) != 50:
        raise ValueError("expected 50 unique selected brokers")
    if sum(item["kind"] == "hot" for item in brokers) != 5:
        raise ValueError("expected five hot brokers")
    if sum(item["kind"] == "random" for item in brokers) != 45:
        raise ValueError("expected 45 random brokers")
    if broker_meta["seed"] != 1 or broker_meta["transactions"] != 300000:
        raise ValueError("broker selection metadata mismatch")
    return rows


def scan_logs():
    findings = []
    for path in NEW_BASE.glob("shards-*/**/process_logs/*.log"):
        with path.open(errors="replace") as handle:
            for line_number, line in enumerate(handle, 1):
                if "level=ERROR" in line or "level=WARN" in line:
                    findings.append((path, line_number, line.strip()))
    if findings:
        path, line_number, line = findings[0]
        raise ValueError(f"warning/error in {path}:{line_number}: {line}")


def audit_baseline_details():
    summaries = {
        (int(row["shards"]), row["method"]): row
        for row in read_csv(NEW_BASE / "baseline_runs.csv")
    }
    for shards in SHARDS:
        for run_method, detail_name in (
            ("static_broker", "broker_stats_detail_tx_info.csv"),
            ("clpa_relay", "relay_stats_detail_tx_info.csv"),
        ):
            path = NEW_BASE / f"shards-{shards}/{run_method}/results/{detail_name}"
            seen = set()
            latencies = []
            with path.open(newline="") as handle:
                for row in csv.DictReader(handle):
                    tx_hash = row["OriginalHash"]
                    if tx_hash in seen:
                        raise ValueError(f"duplicate original transaction in {path}: {tx_hash}")
                    seen.add(tx_hash)
                    created = stamp(row["Tx create time"])
                    committed = stamp(row["Tx finally commit time"])
                    if committed < created:
                        raise ValueError(f"negative transaction latency in {path}: {tx_hash}")
                    latencies.append(committed - created)
            if len(seen) != 300000:
                raise ValueError(f"expected 300000 unique transactions in {path}, got {len(seen)}")
            latencies.sort()
            mean_ms = statistics.fmean(latencies) * 1000
            p95_ms = latencies[math.ceil(0.95 * len(latencies)) - 1] * 1000
            expected = summaries[(shards, run_method)]
            if not math.isclose(mean_ms, float(expected["latency_mean_ms"]), abs_tol=1e-6):
                raise ValueError(f"mean latency does not match summary: {path}")
            if not math.isclose(p95_ms, float(expected["latency_p95_ms"]), abs_tol=1e-6):
                raise ValueError(f"p95 latency does not match summary: {path}")


def migration_summary(comparison, old_comparison, old_epochs):
    by_new = {(row["shards"], row["method"]): row for row in comparison}
    by_old = {(row["shards"], row["method"]): row for row in old_comparison}
    old_epoch_map = {}
    for row in old_epochs:
        if row["method"] == "CLPA+Relay":
            old_epoch_map.setdefault(row["shards"], []).append(row)

    rows = []
    for interval, root, result_map in ((50, OLD_BASE, by_old), (20, NEW_BASE, by_new)):
        for shards in SHARDS:
            migration_path = root / f"shards-{shards}/clpa_relay/results/clpa_repartition_metrics.csv"
            migrations = read_csv(migration_path)
            totals = [float(row["Total latency ns"]) / 1e9 for row in migrations]
            result = result_map[(shards, "CLPA+Relay")]
            rows.append(
                {
                    "clpa_interval_s": interval,
                    "shards": shards,
                    "migration_count": len(migrations),
                    "migrated_accounts_total": sum(int(row["Migrated account count"]) for row in migrations),
                    "migration_total_s": sum(totals),
                    "migration_mean_s": statistics.fmean(totals),
                    "migration_max_s": max(totals),
                    "partition_total_s": sum(float(row["Partition latency ns"]) for row in migrations) / 1e9,
                    "broadcast_total_s": sum(float(row["Broadcast latency ns"]) for row in migrations) / 1e9,
                    "migration_sync_total_s": sum(float(row["Migration sync latency ns"]) for row in migrations) / 1e9,
                    "mean_epoch_tps": result["mean_epoch_tps"],
                    "completion_tps": result["completion_tps"],
                    "latency_mean_s": result["latency_mean_s"],
                    "epoch_count": result["epoch_count"],
                    "zero_transaction_epochs": result["zero_transaction_epochs"],
                    "source": str(migration_path.relative_to(ROOT)),
                }
            )
    return rows


def completion_quantiles():
    rows = []
    for interval, root in ((50, OLD_BASE), (20, NEW_BASE)):
        for shards in SHARDS:
            run = root / f"shards-{shards}/clpa_relay"
            details = run / "results/relay_stats_detail_tx_info.csv"
            commits = []
            first_create = math.inf
            with details.open(newline="") as handle:
                for row in csv.DictReader(handle):
                    first_create = min(first_create, stamp(row["Tx create time"]))
                    commits.append(stamp(row["Tx finally commit time"]))
            if len(commits) != 300000:
                raise ValueError(f"incomplete CLPA detail file: {details}")
            commits.sort()
            migrations = read_csv(run / "results/clpa_repartition_metrics.csv")
            first_migration = stamp(migrations[0]["Start time"])

            def quantile(p):
                return commits[max(0, math.ceil(p * len(commits)) - 1)] - first_create

            rows.append(
                {
                    "clpa_interval_s": interval,
                    "shards": shards,
                    "completed_txs": len(commits),
                    "completion_p50_s": quantile(0.50),
                    "completion_p90_s": quantile(0.90),
                    "completion_p95_s": quantile(0.95),
                    "completion_p99_s": quantile(0.99),
                    "completion_p100_s": quantile(1.00),
                    "completed_before_first_migration": sum(t <= first_migration for t in commits),
                    "fraction_completed_before_first_migration": (
                        sum(t <= first_migration for t in commits) / len(commits)
                    ),
                    "source": str(details.relative_to(ROOT)),
                }
            )
    return rows


def configure_plotting(font_size=9.5):
    plt.rcParams.update(
        {
            "font.family": "serif",
            "font.serif": ["Times New Roman", "DejaVu Serif"],
            "font.size": font_size,
            "axes.labelsize": font_size + 0.5,
            "legend.fontsize": font_size - 0.5,
            "pdf.fonttype": 42,
            "ps.fonttype": 42,
            "figure.dpi": 300,
            "savefig.dpi": 300,
            "savefig.bbox": "tight",
            "axes.spines.top": False,
            "axes.spines.right": False,
            "axes.grid": True,
            "grid.alpha": 0.18,
            "grid.linestyle": "-",
        }
    )


def values_by_method(rows, metric, method):
    lookup = {(row["shards"], row["method"]): row for row in rows}
    return [lookup[(shards, method)][metric] for shards in SHARDS]


def draw_methods(ax, rows, metric):
    for method in METHODS:
        style = STYLES[method]
        ax.plot(
            SHARDS,
            values_by_method(rows, metric, method),
            label=method,
            markersize=8 if method == "Proposed" else 5.5,
            markeredgecolor="white",
            markeredgewidth=0.55,
            zorder=4 if method == "Proposed" else 3,
            **style,
        )
    ax.set_xticks(SHARDS)
    ax.set_xlabel("Number of shards")
    ax.set_axisbelow(True)


def save_figure(fig, stem):
    fig.savefig(OUT / f"{stem}.pdf")
    fig.savefig(OUT / f"{stem}.png", dpi=300)
    plt.close(fig)


def plot_five_methods(rows):
    configure_plotting()
    fig, axes = plt.subplots(1, 2, figsize=(8.8, 3.45))
    draw_methods(axes[0], rows, "mean_epoch_tps")
    draw_methods(axes[1], rows, "latency_mean_s")
    axes[0].set_ylabel("Mean epoch TPS (tx/s)")
    axes[1].set_ylabel("Mean end-to-end latency (s)")
    handles, labels = axes[0].get_legend_handles_labels()
    fig.legend(handles, labels, loc="upper center", ncol=5, frameon=False, bbox_to_anchor=(0.5, 1.02))
    fig.text(
        0.5,
        -0.01,
        "TPS uses the arithmetic mean of native epochs (zero-transaction epochs retained); "
        "aggregation TPS extends native windows by logical payment count. Single run per configuration.",
        ha="center",
        va="top",
        fontsize=7.6,
        color="#444444",
    )
    fig.subplots_adjust(top=0.82, bottom=0.20, wspace=0.28)
    save_figure(fig, "five_method_native_window_interval20_hot5_v3")

    for metric, ylabel, stem in (
        ("mean_epoch_tps", "Mean epoch TPS (tx/s)", "tps"),
        ("latency_mean_s", "Mean end-to-end latency (s)", "latency"),
    ):
        fig, ax = plt.subplots(figsize=(5.3, 3.5))
        draw_methods(ax, rows, metric)
        ax.set_ylabel(ylabel)
        ax.legend(loc="upper center", bbox_to_anchor=(0.5, 1.18), ncol=3, frameon=False)
        fig.subplots_adjust(top=0.78, bottom=0.16, left=0.14, right=0.98)
        save_figure(fig, stem)


def plot_clpa_interval(migrations):
    configure_plotting()
    lookup = {(row["clpa_interval_s"], row["shards"]): row for row in migrations}
    fig, axes = plt.subplots(1, 2, figsize=(8.0, 3.35))
    for interval, color, marker in ((50, "#7A7A7A", "o"), (20, "#CC79A7", "^")):
        label = f"CLPA interval = {interval} s"
        axes[0].plot(
            SHARDS,
            [lookup[(interval, s)]["mean_epoch_tps"] for s in SHARDS],
            color=color,
            marker=marker,
            linewidth=2,
            markersize=6,
            label=label,
        )
        axes[1].plot(
            SHARDS,
            [lookup[(interval, s)]["latency_mean_s"] for s in SHARDS],
            color=color,
            marker=marker,
            linewidth=2,
            markersize=6,
            label=label,
        )
    for ax in axes:
        ax.set_xticks(SHARDS)
        ax.set_xlabel("Number of shards")
        ax.set_axisbelow(True)
    axes[0].set_ylabel("Mean epoch TPS (tx/s)")
    axes[1].set_ylabel("Mean end-to-end latency (s)")
    handles, labels = axes[0].get_legend_handles_labels()
    fig.legend(handles, labels, loc="upper center", ncol=2, frameon=False, bbox_to_anchor=(0.5, 1.02))
    fig.subplots_adjust(top=0.80, bottom=0.17, wspace=0.30)
    save_figure(fig, "clpa_interval_comparison")


def write_analysis(comparison, migrations, quantiles):
    by_method = {(row["shards"], row["method"]): row for row in comparison}
    by_migration = {(row["clpa_interval_s"], row["shards"]): row for row in migrations}
    by_quantile = {(row["clpa_interval_s"], row["shards"]): row for row in quantiles}
    lines = [
        "# Five-method comparison with interval-20 CLPA and hot-random BrokerChain",
        "",
        "All 20 plotted points are single completed runs; no error bars are shown. Every selected run "
        "completed 300,000 original transactions.",
        "",
        "## Metric definitions",
        "",
        "- TPS is the arithmetic mean of per-epoch native TPS, retaining zero-transaction epochs. "
        "For Batch-only and Proposed, the native epoch window is extended from recorded relay entries "
        "to all 300,000 logical payments; native TPS values are rounded to two decimals in the source CSV, "
        "so those extended values are approximate.",
        "- Latency is the mean end-to-end time from original transaction creation to final commit for all "
        "300,000 original transactions.",
        "- Proposed uses full aggregation at 4/8/16 shards and 50% hot-destination aggregation at 24 shards.",
        "",
        "## Final values",
        "",
        "| Shards | Method | Mean epoch TPS | Mean latency (s) | Completion TPS |",
        "|---:|---|---:|---:|---:|",
    ]
    for shards in SHARDS:
        for method in METHODS:
            row = by_method[(shards, method)]
            lines.append(
                f"| {shards} | {method} | {row['mean_epoch_tps']:.2f} | "
                f"{row['latency_mean_s']:.3f} | {row['completion_tps']:.2f} |"
            )

    lines.extend(
        [
            "",
            "## Main comparison",
            "",
        ]
    )
    for shards in SHARDS:
        proposed = by_method[(shards, "Proposed")]
        baselines = [by_method[(shards, method)] for method in METHODS[:3]]
        best_tps = max(baselines, key=lambda row: row["mean_epoch_tps"])
        best_latency = min(baselines, key=lambda row: row["latency_mean_s"])
        tps_gain = (proposed["mean_epoch_tps"] / best_tps["mean_epoch_tps"] - 1) * 100
        latency_drop = (1 - proposed["latency_mean_s"] / best_latency["latency_mean_s"]) * 100
        lines.append(
            f"- At {shards} shards, Proposed delivers {tps_gain:.1f}% more mean epoch TPS than "
            f"the strongest non-aggregation baseline ({best_tps['method']}) and {latency_drop:.1f}% "
            f"lower mean latency than the lowest-latency non-aggregation baseline "
            f"({best_latency['method']})."
        )

    lines.extend(
        [
            "",
            "## CLPA interval: 50 s versus 20 s",
            "",
            "| Shards | TPS change | Latency change | Migrations (50→20 s) | "
            "Total migration time (50→20 s) | Zero epochs (50→20 s) |",
            "|---:|---:|---:|---:|---:|---:|",
        ]
    )
    for shards in SHARDS:
        old = by_migration[(50, shards)]
        new = by_migration[(20, shards)]
        tps_change = (new["mean_epoch_tps"] / old["mean_epoch_tps"] - 1) * 100
        latency_change = (new["latency_mean_s"] / old["latency_mean_s"] - 1) * 100
        lines.append(
            f"| {shards} | {tps_change:+.1f}% | {latency_change:+.1f}% | "
            f"{old['migration_count']}→{new['migration_count']} | "
            f"{old['migration_total_s']:.2f}→{new['migration_total_s']:.2f} s | "
            f"{old['zero_transaction_epochs']}→{new['zero_transaction_epochs']} |"
        )

    old24 = by_migration[(50, 24)]
    new24 = by_migration[(20, 24)]
    q_old24 = by_quantile[(50, 24)]
    q_new24 = by_quantile[(20, 24)]
    lines.extend(
        [
            "",
            "The 20-second interval raises CLPA mean epoch TPS at every shard count, but it does not "
            "uniformly reduce latency. At 4/8/16 shards, mean latency falls; at 24 shards it rises "
            f"from {old24['latency_mean_s']:.3f} s to {new24['latency_mean_s']:.3f} s. The 24-shard "
            f"run now performs {new24['migration_count']} migrations instead of {old24['migration_count']}; "
            f"the longest migration is {new24['migration_max_s']:.2f} s, and the second migration occurs "
            "while a substantial transaction tail remains. This shifts migration cost into the observed "
            "transaction lifecycle rather than leaving most of it after workload completion.",
            "",
            f"For 24 shards, 95% completion moves from {q_old24['completion_p95_s']:.2f} s to "
            f"{q_new24['completion_p95_s']:.2f} s and full completion from "
            f"{q_old24['completion_p100_s']:.2f} s to {q_new24['completion_p100_s']:.2f} s. "
            "Thus, the higher mean epoch TPS and worse end-to-end latency are not contradictory: epoch TPS "
            "measures throughput inside each epoch's active block window, while latency includes waiting "
            "across repartition pauses.",
            "",
            "The result supports treating the 20-second interval as a throughput/latency trade-off rather "
            "than a universally superior setting. A longer workload covering multiple steady-state "
            "migration cycles is still needed before claiming sustained-capacity superiority.",
            "",
            "## Audit notes",
            "",
            "- v3 contains eight complete baseline detail files with 300,000 unique original transactions each.",
            "- No WARN or ERROR records were found in the v3 process logs.",
            "- The BrokerChain list contains 50 unique brokers: five highest-activity accounts plus "
            "45 seeded random accounts (seed 1), shared across shard counts.",
            "- All selected runs use the same filtered 300,000-transaction dataset, 600 tx/s per shard, "
            "four nodes per shard, block capacity 2,000, and a 2,000 ms shard block interval. "
            "Aggregation runs use a 1,000 ms Beacon interval, a 2,000 ms aggregation window, and "
            "credit chunks of 512.",
        ]
    )
    (OUT / "analysis.md").write_text("\n".join(lines) + "\n")


def main():
    scan_logs()
    audit_baseline_details()
    audit_rows = audit_configuration()
    comparison, epochs = build_comparison(NEW_BASE)
    old_comparison, old_epochs = build_comparison(OLD_BASE)
    migrations = migration_summary(comparison, old_comparison, old_epochs)
    quantiles = completion_quantiles()

    write_csv(OUT / "comparison_data.csv", comparison)
    write_csv(OUT / "native_epoch_data.csv", epochs)
    write_csv(OUT / "configuration_audit.csv", audit_rows)
    write_csv(OUT / "clpa_migration_comparison.csv", migrations)
    write_csv(OUT / "clpa_completion_quantiles.csv", quantiles)
    plot_five_methods(comparison)
    plot_clpa_interval(migrations)
    write_analysis(comparison, migrations, quantiles)

    print(f"Validated and wrote outputs to {OUT}")
    for row in comparison:
        print(
            f"{row['shards']:>2} {row['method']:<12} "
            f"TPS={row['mean_epoch_tps']:8.2f} latency={row['latency_mean_s']:6.3f}s"
        )


if __name__ == "__main__":
    main()
