#!/usr/bin/env python3
"""Generate six diagnostics from the same completed runs as the TPS comparison."""

from __future__ import annotations

import csv
import importlib.util
import json
import statistics
from pathlib import Path

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.backends.backend_pdf import PdfPages
from matplotlib.ticker import FuncFormatter
import numpy as np

import gen_fig_aggregation_scale_baselines as comparison


ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / "figures/aggregation-diagnostics-full-plus-24-hot75"
METHODS = comparison.METHODS
COLORS = {method: comparison.STYLE[method][0] for method in METHODS}
SHARDS = comparison.SHARDS
STAGES = ("source_s", "coordination_s", "destination_s")
STAGE_LABELS = ("Source confirmation", "Coordination", "Destination confirmation")
STAGE_COLORS = ("#7B8794", "#E69F00", "#009E73")
spec = importlib.util.spec_from_file_location(
    "aggregation_analysis", ROOT / "experiments/aggregation_4shard_300k/analyze.py"
)
original = importlib.util.module_from_spec(spec)
spec.loader.exec_module(original)
parse_time = original.parse_time


def iter_csv(path):
    with path.open(newline="", encoding="utf-8") as handle:
        yield from csv.DictReader(handle)


def write_csv(name, rows):
    rows = list(rows)
    if not rows:
        raise ValueError(f"no records for {name}")
    with (OUT / name).open("w", newline="", encoding="utf-8") as handle:
        writer = csv.DictWriter(handle, fieldnames=list(rows[0]))
        writer.writeheader()
        writer.writerows(rows)


def run_for(record):
    run = (ROOT / record["source"]).parent / f"shards-{record['shards']}"
    if record["repeat"] != "single":
        run /= record["repeat"]
    return run / record["run_method"]


def stage_partition(start, source, beacon, end):
    if not start <= source <= beacon <= end:
        raise ValueError(f"nonmonotonic lifecycle: {start}, {source}, {beacon}, {end}")
    return np.array((source - start, beacon - source, end - beacon), dtype=float) / 1e9


def aligned_pool(series, grid):
    """Previous observation per leader on common timestamps, never sample-index zip."""
    result = np.zeros(len(grid), dtype=np.int64)
    for times, counts in series:
        if len(times) == 0 or times[0] > grid[0] or times[-1] < grid[-1]:
            raise ValueError("incomplete pool coverage")
        if np.any(np.diff(times) < 0) or np.any(counts < 0):
            raise ValueError("invalid pool samples")
        indices = np.searchsorted(times, grid, side="right") - 1
        result += counts[indices]
    return result


def collect_savings(records):
    runs, batches_by_run = [], {}
    for record in records:
        if record["method"] != "Proposed":
            continue
        run = run_for(record)
        summary = next(
            row for row in iter_csv(ROOT / record["source"])
            if int(row["shards"]) == record["shards"]
            and row["repeat"] == record["repeat"]
            and row["method"] == record["run_method"]
        )
        batches = list(iter_csv(run / "results/aggregation_batch_metrics.csv"))
        if len({row["BatchID"] for row in batches}) != len(batches):
            raise ValueError(f"duplicate batch: {run}")
        batches_by_run[str(run)] = batches
        blocks = original.block_stats(run)
        receiver_updates = sum(
            int(row["ReceiverUpdateCount"])
            for row in iter_csv(run / "results/aggregation_execution_metrics.csv")
            if row["Phase"] == "settlement"
        )
        baseline = int(summary["completed_ctx"])
        settlement_entries = blocks["block_AggregationSettlementTxCount"]
        remaining_relay = blocks["block_Relay2TxCount"]
        destination_entries = remaining_relay + settlement_entries
        remaining_updates = remaining_relay + receiver_updates
        aggregated = sum(int(row["ReceiptCount"]) for row in batches)
        assert baseline == remaining_relay + aggregated
        assert destination_entries == int(summary["destination_entries"])
        assert remaining_updates == int(summary["receiver_updates"])
        assert receiver_updates == sum(int(row["CreditCount"]) for row in batches)
        assert settlement_entries == sum(int(row["SettlementTxCount"]) for row in batches)
        assert 0 <= destination_entries <= baseline and 0 <= remaining_updates <= baseline
        runs.append(dict(
            shards=record["shards"], repeat=record["repeat"],
            baseline_destination_entries=baseline, aggregated_original_ctx=aggregated,
            remaining_relay_entries=remaining_relay, settlement_entries=settlement_entries,
            proposed_destination_entries=destination_entries,
            destination_entries_saved=baseline - destination_entries,
            destination_saved_percent=100 * (baseline - destination_entries) / baseline,
            baseline_receiver_updates=baseline, proposed_receiver_updates=remaining_updates,
            receiver_updates_saved=baseline - remaining_updates,
            receiver_updates_saved_percent=100 * (baseline - remaining_updates) / baseline,
            source=str(run.relative_to(ROOT)),
        ))
    summary = []
    for shards in SHARDS:
        group = [row for row in runs if row["shards"] == shards]
        row = dict(shards=shards, n=len(group))
        for field in list(group[0])[2:-1]:
            values = [item[field] for item in group]
            row[field] = statistics.mean(values)
            row[field + "_std"] = statistics.stdev(values) if len(values) > 1 else ""
        summary.append(row)
    write_csv("savings_runs.csv", runs)
    write_csv("savings_summary.csv", summary)
    return summary, batches_by_run


def collect_lifecycles(record):
    run, method = run_for(record), record["method"]
    results = run / "results"
    is_broker = method == "BrokerChain"
    filename = "broker_stats_detail_tx_info.csv" if is_broker else "relay_stats_detail_tx_info.csv"
    stages, lifetimes, hashes, cross_flags = [], [], set(), []
    for row in iter_csv(results / filename):
        key = row["OriginalHash"]
        if key in hashes:
            raise ValueError(f"duplicate original transaction: {run}")
        hashes.add(key)
        start, end = parse_time(row["Tx create time"]), parse_time(row["Tx finally commit time"])
        boundary_name = "Broker1 tx commit time" if is_broker else "Relay1 tx commit time"
        boundary = row[boundary_name]
        source = parse_time(boundary) if boundary else end
        stages.append(stage_partition(start, source, source, end))
        lifetimes.append((start, end))
        cross_flags.append(bool(boundary))
    if method == "Proposed":
        sources = {}
        for row in iter_csv(results / "aggregation_execution_metrics.csv"):
            if row["Phase"] == "source_debit":
                if row["PaymentID"] in sources:
                    raise ValueError(f"duplicate source-debit metric: {run}")
                sources[row["PaymentID"]] = int(row["CommittedAtUnixNano"])
        batches = {row["BatchID"]: int(row["BeaconFinalizedAtUnixNano"])
                   for row in iter_csv(results / "aggregation_batch_metrics.csv")}
        for row in iter_csv(results / "aggregation_tx_lifecycle.csv"):
            key = row["PaymentID"]
            if key in hashes:
                raise ValueError(f"duplicate original transaction: {run}")
            hashes.add(key)
            start, end = int(row["CreatedAtUnixNano"]), int(row["FinalCommittedAtUnixNano"])
            assert int(row["LatencyNs"]) == end - start
            stages.append(stage_partition(start, sources[key], batches[row["BatchID"]], end))
            lifetimes.append((start, end))
            cross_flags.append(True)
    assert len(lifetimes) == 300000
    stages, lifetimes = np.asarray(stages), np.asarray(lifetimes, dtype=np.int64)
    latencies = (lifetimes[:, 1] - lifetimes[:, 0]) / 1e9
    assert np.allclose(stages.sum(axis=1), latencies, atol=1e-8, rtol=0)
    assert np.isclose(latencies.mean(), record["latency_mean_s"], atol=1e-6, rtol=0)
    return dict(stages=stages, lifetimes=lifetimes, latencies=latencies,
                cross_flags=np.asarray(cross_flags, dtype=bool))


def collect_shard8(records):
    selected = [row for row in records if row["shards"] == 8 and row["repeat"] in ("repeat-01", "single")]
    assert len(selected) == 4
    metrics, pool_records, stage_rows, distributions, provenance = {}, [], [], [], []
    for record in selected:
        method, run = record["method"], run_for(record)
        print(f"Reading 8-shard {method}...", flush=True)
        values = collect_lifecycles(record)
        metrics[method] = values
        latency = values["latencies"]
        quantiles = np.quantile(latency, [0, .05, .25, .5, .75, .95, .99, 1], method="inverted_cdf")
        distributions.append(dict(method=method, n=len(latency), mean_s=float(latency.mean()),
                                  **dict(zip(("min_s", "p05_s", "p25_s", "median_s", "p75_s", "p95_s", "p99_s", "max_s"), quantiles))))
        # Both scopes are exported. The requested primary chart uses all originals,
        # matching the TPS/latency comparison and violin plot.
        for scope, mask in (("all_originals", np.ones(len(latency), dtype=bool)),
                            ("cross_shard_only", values["cross_flags"])):
            means = values["stages"][mask].mean(axis=0)
            stage_rows.append(dict(method=method, scope=scope, n=int(mask.sum()),
                                   **dict(zip(STAGES, means)), total_s=float(means.sum())))
        start, end = int(values["lifetimes"][:, 0].min()), int(values["lifetimes"][:, 1].max())
        series = []
        for shard in range(8):
            rows = sorted((parse_time(row["Timestamp"]), int(row["TxCount"]))
                          for row in iter_csv(run / f"block_record/shard={shard}_node=0/txpool_record.csv"))
            stamps, counts = np.asarray(rows, dtype=np.int64).T
            series.append((stamps, counts))
        if any(stamps[0] > start or stamps[-1] < end for stamps, _ in series):
            raise ValueError(f"pool records do not cover transaction completion: {run}")
        grid = np.arange(start, end + 1, 500_000_000, dtype=np.int64)
        if grid[-1] != end:
            grid = np.append(grid, end)
        total = aligned_pool(series, grid)
        values["pool_time_s"], values["pool_total"] = (grid - start) / 1e9, total
        pool_records.extend(dict(method=method, time_s=float(t), total_entries=int(count))
                            for t, count in zip(values["pool_time_s"], total))
        provenance.append(dict(method=method, shards=8, repeat=record["repeat"],
                               completed_txs=len(latency), start_ns=start, end_ns=end,
                               run=str(run.relative_to(ROOT)),
                               mean_s=float(latency.mean()), pool_peak_entries=int(total.max())))
    write_csv("shard8_latency_distribution.csv", distributions)
    write_csv("shard8_stage_latency.csv", stage_rows)
    write_csv("shard8_txpool_timeseries.csv", pool_records)
    write_csv("shard8_selected_runs.csv", provenance)
    np.savez_compressed(OUT / "shard8_latency_samples.npz", **{method: item["latencies"] for method, item in metrics.items()})
    return metrics, stage_rows


def collect_beacon(records, batches_by_run):
    rows, batch_rows = [], []
    for record in records:
        if record["method"] != "Proposed":
            continue
        run = run_for(record)
        batches = batches_by_run[str(run)]
        n, receipts = len(batches), sum(int(b["ReceiptCount"]) for b in batches)
        def total(field):
            return sum(int(b[field]) for b in batches)
        def avg_ms(field):
            return total(field) / n / 1e6
        build = total("BuildTimeNs")
        agg, merkle = total("AggregationTimeNs"), total("MerkleBuildTimeNs")
        if build < agg + merkle:
            raise ValueError(f"inconsistent nested build timing: {run}")
        network = sum(total(key) for key in ("PreprepareBytes", "PrepareBytes", "CommitBytes"))
        row = dict(shards=record["shards"], repeat=record["repeat"], batches=n, aggregated_ctx=receipts,
                   aggregation_ms_per_batch=avg_ms("AggregationTimeNs"),
                   merkle_ms_per_batch=avg_ms("MerkleBuildTimeNs"),
                   other_build_ms_per_batch=(build - agg - merkle) / n / 1e6,
                   local_validation_ms_per_batch=avg_ms("LocalValidationTimeNs"),
                   build_ms_per_batch=avg_ms("BuildTimeNs"),
                   local_processing_total_s=(build + total("LocalValidationTimeNs")) / 1e9,
                   consensus_mean_s=avg_ms("BeaconConsensusLatencyNs") / 1000,
                   consensus_p95_s=float(np.quantile([int(b["BeaconConsensusLatencyNs"]) / 1e9 for b in batches], .95, method="inverted_cdf")),
                   preprepare_mib=total("PreprepareBytes") / 2**20,
                   prepare_mib=total("PrepareBytes") / 2**20, commit_mib=total("CommitBytes") / 2**20,
                   estimated_pbft_total_mib=network / 2**20,
                   estimated_pbft_bytes_per_aggregated_ctx=network / receipts,
                   header_storage_mib=total("BatchStorageBytes") / 2**20,
                   sidecar_storage_mib=total("SidecarBytes") / 2**20,
                   proposal_mib=total("BatchProposalBytes") / 2**20,
                   window_mean_s=avg_ms("WindowOpenDurationNs") / 1000,
                   source=str(run.relative_to(ROOT)))
        rows.append(row)
        batch_rows.extend(dict(shards=record["shards"], repeat=record["repeat"], **batch) for batch in batches)
    write_csv("beacon_run_costs.csv", rows)
    write_csv("beacon_batch_metrics.csv", batch_rows)
    summary = []
    for shards in SHARDS:
        group = [row for row in rows if row["shards"] == shards]
        row = dict(shards=shards, n=len(group))
        for field in list(group[0])[2:-1]:
            values = [item[field] for item in group]
            row[field] = statistics.mean(values)
            row[field + "_std"] = statistics.stdev(values) if len(values) > 1 else ""
        summary.append(row)
    write_csv("beacon_cost_summary.csv", summary)
    return summary


def style():
    plt.rcParams.update({"font.family": "serif", "font.serif": ["Times New Roman", "DejaVu Serif"],
                         "font.size": 10, "axes.spines.top": False, "axes.spines.right": False,
                         "axes.axisbelow": True, "pdf.fonttype": 42, "ps.fonttype": 42,
                         "savefig.dpi": 300, "legend.frameon": False})


def save(fig, name, book):
    for suffix in ("png", "pdf", "svg"):
        fig.savefig(OUT / f"{name}.{suffix}", bbox_inches="tight")
    book.savefig(fig, bbox_inches="tight")
    plt.close(fig)


def savings_chart(summary, field, ylabel, title):
    fig, ax = plt.subplots(figsize=(5.5, 3.7))
    values = np.array([row[field] for row in summary])
    error = np.array([row[field + "_std"] or 0 for row in summary])
    x = np.arange(4)
    bars = ax.bar(x, values, width=.58, color=[COLORS["Proposed"]] * 3 + ["#E69F00"])
    indices = [i for i, row in enumerate(summary) if row["n"] > 1]
    ax.errorbar(x[indices], values[indices], yerr=error[indices], fmt="none", capsize=3, color="#333333")
    for bar, value in zip(bars, values):
        ax.text(bar.get_x() + bar.get_width()/2, value + max(values)*.03,
                f"{value:,.0f}", ha="center", fontsize=9)
    ax.set_xticks(x, [str(s) for s in SHARDS])
    ax.set_xlabel("Number of shards")
    ax.set_ylabel(ylabel)
    ax.set_title(title, fontweight="bold", pad=12)
    ax.yaxis.set_major_formatter(FuncFormatter(lambda v, _: f"{v:,.0f}"))
    ax.set_ylim(0, max(values + error) * 1.2)
    ax.grid(axis="y", alpha=.18)
    fig.text(.5, .035, "Relative to one destination entry / receiver update per original CTX.\n"
             "4/8/16: full aggregation, n=3; 24: 75% destination shards, n=1.",
             ha="center", fontsize=8, color="#444444")
    fig.tight_layout(rect=(0, .12, 1, 1))
    return fig


def pool_chart(metrics):
    fig, ax = plt.subplots(figsize=(7, 3.8))
    for method in METHODS:
        values = metrics[method]
        ax.plot(values["pool_time_s"], values["pool_total"], label=method, color=COLORS[method],
                linewidth=1.8, linestyle={"Monoxide": "--", "BrokerChain": "-.", "CLPA+Relay": ":", "Proposed": "-"}[method])
    ax.set_xlabel("Time since first original transaction creation (s)")
    ax.set_ylabel("Total transaction-pool entries")
    ax.set_title("8 shards: transaction-pool length", fontweight="bold")
    ax.grid(alpha=.18)
    ax.legend(ncol=2, fontsize=9)
    ax.set_xlim(left=0)
    ax.set_ylim(bottom=0)
    ax.yaxis.set_major_formatter(FuncFormatter(lambda v, _: f"{v:,.0f}"))
    fig.text(.5, .025, "First completed run per method; sum of 8 shard leaders, sampled every 0.5 s.\n"
             "Native pool entries; Beacon and in-flight transactions are outside this count.",
             ha="center", fontsize=8, color="#444444")
    fig.tight_layout(rect=(0, .12, 1, 1))
    return fig


def violin_chart(metrics):
    fig, ax = plt.subplots(figsize=(6.5, 4.0))
    arrays = [metrics[method]["latencies"] for method in METHODS]
    # KDE uses a deterministic sample; exact quantiles/means use all 300000 originals.
    rng = np.random.default_rng(20261027)
    samples = [rng.choice(a, min(30000, len(a)), replace=False) for a in arrays]
    parts = ax.violinplot(samples, positions=np.arange(4), widths=.8, showextrema=False, points=160)
    for body, method in zip(parts["bodies"], METHODS):
        body.set_facecolor(COLORS[method]); body.set_edgecolor(COLORS[method]); body.set_alpha(.7)
    for i, values in enumerate(arrays):
        low, q1, median, q3, high = np.quantile(values, [.05, .25, .5, .75, .95], method="inverted_cdf")
        ax.vlines(i, low, high, color="#333333", linewidth=1)
        ax.vlines(i, q1, q3, color="#333333", linewidth=4)
        ax.scatter([i], [median], color="white", s=23, zorder=4, edgecolor="#333333", linewidth=.5)
        ax.scatter([i], [values.mean()], marker="D", color="black", s=18, zorder=4)
    ax.set_xticks(np.arange(4), METHODS)
    ax.set_ylabel("Original-transaction confirmation latency (s)")
    ax.set_title("8 shards: confirmation-latency distribution", fontweight="bold")
    ax.set_ylim(bottom=0)
    ax.grid(axis="y", alpha=.18)
    fig.text(.5, .025, "300,000 originals per method, first completed run. KDE: seeded 30,000 samples.\n"
             "White dot: median; diamond: mean; thick line: IQR; thin line: P5–P95 (full data).",
             ha="center", fontsize=8, color="#444444")
    fig.tight_layout(rect=(0, .12, 1, 1))
    return fig


def stage_chart(rows, scope="all_originals"):
    fig, ax = plt.subplots(figsize=(6.8, 4.0))
    selected = {row["method"]: row for row in rows if row["scope"] == scope}
    bottoms, x = np.zeros(4), np.arange(4)
    for field, label, color in zip(STAGES, STAGE_LABELS, STAGE_COLORS):
        values = np.array([selected[method][field] for method in METHODS])
        ax.bar(x, values, bottom=bottoms, width=.6, label=label, color=color, edgecolor="white", linewidth=.5)
        for i, value in enumerate(values):
            if value > .5:
                ax.text(i, bottoms[i] + value/2, f"{value:.2f}", ha="center", va="center", fontsize=9)
        bottoms += values
    for i, value in enumerate(bottoms):
        ax.text(i, value + max(bottoms)*.025, f"{value:.2f} s", ha="center", fontsize=9)
    ax.set_xticks(x, METHODS)
    ax.set_ylabel("Mean confirmation latency (s)")
    ax.set_title("8 shards: transaction-stage latency" + (" (CTX only)" if scope != "all_originals" else ""), fontweight="bold")
    ax.set_ylim(0, max(bottoms) * 1.24)
    ax.grid(axis="y", alpha=.18)
    ax.legend(loc="upper center", bbox_to_anchor=(.5, 1), fontsize=8, ncol=3)
    fig.text(.5, .025, "First completed run; mean over " + ("all 300,000 original transactions" if scope == "all_originals" else "each method's cross-shard transactions") + ".\n"
             "Source + coordination + destination = end-to-end latency; CLPA pauses remain included.",
             ha="center", fontsize=8, color="#444444")
    fig.tight_layout(rect=(0, .12, 1, 1))
    return fig


def beacon_chart(rows):
    fig, axes = plt.subplots(2, 3, figsize=(11.2, 6.8))
    x = np.arange(4)
    ax = axes[0, 0]
    bottoms = np.zeros(4)
    fields = ("aggregation_ms_per_batch", "merkle_ms_per_batch", "other_build_ms_per_batch", "local_validation_ms_per_batch")
    for field, label, color in zip(fields, ("Aggregation", "Merkle", "Other build", "Local validation"),
                                   ("#0072B2", "#009E73", "#999999", "#E69F00")):
        values = np.array([row[field] for row in rows])
        ax.bar(x, values, bottom=bottoms, width=.6, label=label, color=color)
        bottoms += values
    ax.set_title("(a) Local processing per batch")
    ax.set_ylabel("Time (ms / batch)")
    ax.legend(fontsize=7, ncol=2)
    for ax, field, title, ylabel, color in (
        (axes[0, 1], "consensus_mean_s", "(b) PBFT consensus latency", "Mean time (s / batch)", "#0072B2"),
        (axes[1, 0], "estimated_pbft_bytes_per_aggregated_ctx", "(d) Normalized PBFT traffic", "Estimated bytes / aggregated CTX", "#D55E00"),
        (axes[1, 2], "batches", "(f) Finalized batch count", "Batches per run", "#009E73"),
    ):
        ax.bar(x, [row[field] for row in rows], width=.6, color=color)
        ids = [i for i, row in enumerate(rows) if row["n"] > 1]
        ax.errorbar(x[ids], [rows[i][field] for i in ids], yerr=[rows[i][field + "_std"] for i in ids],
                    fmt="none", ecolor="#333333", capsize=2)
        ax.set_title(title)
        ax.set_ylabel(ylabel)
    for ax, fields, labels, colors, title, ylabel in (
        (axes[0, 2], ("preprepare_mib", "prepare_mib", "commit_mib"), ("Preprepare", "Prepare", "Commit"),
         ("#0072B2", "#009E73", "#E69F00"), "(c) Total PBFT traffic estimate", "MiB per run (4 Beacon replicas)"),
        (axes[1, 1], ("header_storage_mib", "sidecar_storage_mib"), ("Block/header", "Sidecar"),
         ("#0072B2", "#E69F00"), "(e) Serialized storage payload", "MiB per run (one replica)"),
    ):
        bottoms = np.zeros(4)
        for field, label, color in zip(fields, labels, colors):
            values = np.array([row[field] for row in rows])
            ax.bar(x, values, bottom=bottoms, width=.6, label=label, color=color)
            bottoms += values
        ax.set_title(title)
        ax.set_ylabel(ylabel)
        ax.legend(fontsize=7)
    for ax in axes.flat:
        ax.set_xticks(x, [str(s) for s in SHARDS])
        ax.set_xlabel("Number of shards")
        ax.grid(axis="y", alpha=.18)
        ax.margins(y=.2)
    fig.suptitle("Beacon cost: full aggregation at 4/8/16; 75% destination shards at 24", fontweight="bold", fontsize=12)
    fig.text(.5, .017, "Mean across runs: n=3 at 4/8/16, n=1 at 24. Errors where shown: between-run SD.\n"
             "Traffic = instrumented PBFT size model; storage = serialized payload, excluding DB overhead. Processing is elapsed time, not CPU time.",
             ha="center", fontsize=8, color="#444444")
    fig.tight_layout(rect=(0, .075, 1, .96), h_pad=2, w_pad=1.5)
    return fig


def write_notes():
    note = """# Aggregation diagnostics

Configuration and data selection match figures/gen_fig_aggregation_scale_baselines.py.
Scale: 4/8/16 full_aggregation, repeats 01-03, from aggregation-scale-beacon1000-repeat3;
24 hot_destination_75, repeat 01, from aggregation-24shard-recheck-v1.
75% selects 18 of 24 destination shards, covering 78.84% of original CTXs.
All runs completed 300000 original transactions. The 24-shard routing policy differs
from 4/8/16, and source batches/executable revisions are not identical across dates.

## 1. Destination transaction entries saved

Per run: completed_ctx - destination_entries. The counterfactual is ordinary Relay,
one destination Relay2 entry per original CTX at the same shard assignment.
Proposed entries = remaining Relay2 + aggregation settlement transactions.
Verified against leader block-record counters and batch settlement counters.
This does not count dropped original transactions and is not savings versus CLPA
or BrokerChain, whose routing/partition mechanisms differ.

## 2. Receiver account updates saved

Per run: completed_ctx - receiver_updates. Receiver updates = remaining Relay2 +
sum(ReceiverUpdateCount for settlement execution records). Verified against the
summary and aggregated CreditCount. Counts balance-credit operations, not unique
accounts, sender debits, system metadata writes, or every physical database write.
Repeat means/sample SD are computed on run-level savings in figures 1 and 2.

## 3. Eight-shard transaction pools

First completed run for each method. Monoxide/Proposed: repeat-01 in
aggregation-scale-beacon1000-repeat3. BrokerChain/CLPA+Relay: interval20-hot5-v3.
Sum TxCount over node 0 of shards 0-7. Per leader, previous observation is carried
forward to a common 0.5-second wall-clock grid relative to first original creation.
Every series covers first creation through final commitment; it ends at its own
completion time. No extrapolation past logs, no asynchronous sample-index zip.
Pool entries are actual native entries, not logical-payment-equivalent workload.
Beacon queues, network in-flight transactions, and other pending states are excluded.

## 4. Eight-shard latency violin

All 300000 original transactions in each selected run. Relay/CLPA use final commit
minus original creation; BrokerChain follows OriginalHash once per original, not
both broker objects; Proposed combines inner relay rows with aggregation lifecycles.
No duplicate IDs; means checked against the earlier comparison summary.
KDE uses 30000 without-replacement samples per method, RNG seed 20261027.
Quantiles, mean markers, IQR, P5-P95 use full data. Full arrays are in NPZ.
The number of transactions is not the number of independent experimental repeats.

## 5. Eight-shard stages

Primary plot averages all original transactions, matching figure 4. A bonus CTX-only
plot and table are included, with each method's own observed cross-shard population.
Relay/CLPA: source = original creation -> Relay1 commit; destination = Relay1 commit
-> final commit. Inner-shard transactions contribute their whole latency to source.
BrokerChain: source = original creation -> Broker1 commit, including dispatch;
destination = Broker1 commit -> final commit. Direct originals contribute to source.
Proposed: source = original creation -> source_debit commit; coordination = source
commit -> BeaconFinalizedAt; destination = Beacon finalization -> original final commit.
Coordination includes ingress, window waiting, build/validation and Beacon consensus.
Other methods have no explicit Beacon coordination stage. Transfer/queue waits stay
in the adjacent source/destination stages. CLPA migration pauses are not subtracted.
Each per-transaction stage is nonnegative and stages sum to that transaction's full
latency. These are lifecycle intervals, not CPU execution time.

## 6. Beacon cost

Ten Proposed runs, same scale selection. Each batch counted once by BatchID.
Local processing = BuildTimeNs + LocalValidationTimeNs; build includes aggregation,
Merkle and other build work. BuildTime is not added again to its nested components.
ValidationTimeNs is a separate field often zero; it is not interpreted as validation
being free. LocalValidationTimeNs is the actual recorded leader local check.
These timers measure elapsed durations, not process CPU utilization/time. They do
not include all follower verification, scheduler load, RAM, energy or money.
Consensus timing = BeaconConsensusLatencyNs from received proposal to final commit;
local build/validation happens before that interval. Batch means are unweighted;
replicate means are equally weighted across runs. Total local seconds and per-batch
P95 consensus latency are exported in CSV alongside the plotted means.

PBFT estimate = PreprepareBytes + PrepareBytes + CommitBytes. Instrumentation uses
serialized-message size times n or n*n for n=4 Beacon replicas. This is a model,
not packet capture, and excludes receipt ingress, finalized header/package delivery,
TCP/gRPC framing, retransmits and other external traffic. It must not be labeled
complete measured network usage. Normalization divides by aggregated originals
(sum ReceiptCount), excluding 24-shard originals that follow ordinary Relay.

Storage = sum BatchStorageBytes plus SidecarBytes for one replica's serialized
block/header and sidecar values. Four replicas store copies; this does not include
BoltDB page slack, keys, index/metadata buckets or filesystem allocation. Sidecar
is not added to PBFT proposal traffic a second time: the proposal already contains it.

## Files / reproduction

01_destination_entries_saved, 02_receiver_updates_saved, 03_shard8_txpool,
04_shard8_latency_violin, 05_shard8_stage_latency, 06_beacon_cost are the six figures.
Each has PNG, vector PDF and SVG. diagnostics_all.pdf collects all six plus the
bonus CTX stage plot. CSVs retain numeric inputs, selected run paths and statistics.

MPLCONFIGDIR=/tmp/codex-mpl-cache /opt/anaconda3/bin/python3 figures/gen_fig_aggregation_diagnostics.py
"""
    (OUT / "README.md").write_text(note, encoding="utf-8")
    names = ["01_destination_entries_saved", "02_receiver_updates_saved", "03_shard8_txpool",
             "04_shard8_latency_violin", "05_shard8_stage_latency", "06_beacon_cost",
             "05b_shard8_ctx_stage_latency"]
    gallery = "<!doctype html><meta charset='utf-8'><title>Aggregation diagnostics</title>"
    gallery += "<style>body{font:16px system-ui;max-width:1150px;margin:32px auto;padding:0 20px}img{width:100%}section{margin:45px 0}a{color:#0072B2}</style>"
    gallery += "<h1>Aggregation diagnostics</h1><p>4/8/16: full aggregation; 24: 75% destination shards. <a href='diagnostics_all.pdf'>Combined PDF</a></p>"
    for name in names:
        gallery += f"<section><h2>{name.replace('_', ' ')}</h2><a href='{name}.pdf'>PDF</a> · <a href='{name}.svg'>SVG</a><br><img src='{name}.png'></section>"
    (OUT / "index.html").write_text(gallery, encoding="utf-8")


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    records, audit, _ = comparison.collect()
    write_csv("configuration_audit.csv", audit)
    print("Auditing scale savings and Beacon counters...", flush=True)
    savings, batches = collect_savings(records)
    beacon = collect_beacon(records, batches)
    metrics, stage_rows = collect_shard8(records)
    style()
    with PdfPages(OUT / "diagnostics_all.pdf") as book:
        save(savings_chart(savings, "destination_entries_saved", "Destination transaction entries saved",
                           "Destination transaction reduction"), "01_destination_entries_saved", book)
        save(savings_chart(savings, "receiver_updates_saved", "Receiver account updates saved",
                           "Receiver account-update reduction"), "02_receiver_updates_saved", book)
        save(pool_chart(metrics), "03_shard8_txpool", book)
        save(violin_chart(metrics), "04_shard8_latency_violin", book)
        save(stage_chart(stage_rows), "05_shard8_stage_latency", book)
        save(beacon_chart(beacon), "06_beacon_cost", book)
        save(stage_chart(stage_rows, "cross_shard_only"), "05b_shard8_ctx_stage_latency", book)
    write_notes()
    print(f"Saved six diagnostics plus CTX-stage supplement to {OUT}", flush=True)
    for row in savings:
        print(f"{row['shards']} shards: entries saved {row['destination_entries_saved']:,.0f}, "
              f"updates saved {row['receiver_updates_saved']:,.0f}", flush=True)


if __name__ == "__main__":
    main()
