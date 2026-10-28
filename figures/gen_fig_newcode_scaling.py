#!/usr/bin/env python3
"""Reproducible figures from a completed new-code scaling suite only."""
import argparse
import csv
import json
from pathlib import Path
import sys

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.backends.backend_pdf import PdfPages
import numpy as np

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "experiments/adaptive_aggregation"))
from common import read_rows, write_csv, write_json, time_parser

MODES = ("monoxide", "brokerchain", "clpa", "full", "adaptive")
LABELS = ("Monoxide", "BrokerChain", "CLPA+Relay", "Ours (full)", "Ours (adaptive)")
COLORS = ("#0072B2", "#E69F00", "#009E73", "#D55E00", "#CC79A7")
MARKERS = ("o", "s", "^", "D", "v")
SHARDS = (4, 8, 16, 24)
parse_time = time_parser()


def partition(start, source, beacon, end):
    if not start <= source <= beacon <= end:
        raise ValueError(f"nonmonotonic stages: {start,source,beacon,end}")
    return ((source - start) / 1e9, (beacon - source) / 1e9, (end - beacon) / 1e9)


def lifecycle(case):
    run = Path(case["path"])
    results = run / "results"
    stages, spans, cross, ids = [], [], [], set()

    def add(key, start, source, beacon, end, is_cross):
        if key in ids:
            raise ValueError(f"duplicate original: {key}")
        ids.add(key)
        stages.append(partition(start, source, beacon, end))
        spans.append((start, end))
        cross.append(is_cross)

    broker = case["mode"] == "brokerchain"
    detail = (
        "broker_stats_detail_tx_info.csv"
        if broker
        else "relay_stats_detail_tx_info.csv"
    )
    boundary = "Broker1 tx commit time" if broker else "Relay1 tx commit time"
    for row in read_rows(results / detail):
        start, end = parse_time(row["Tx create time"]), parse_time(
            row["Tx finally commit time"]
        )
        source = parse_time(row[boundary]) if row[boundary] else end
        add(row["OriginalHash"], start, source, source, end, bool(row[boundary]))
    if case["mode"] in ("full", "adaptive"):
        source = {
            r["PaymentID"]: int(r["CommittedAtUnixNano"])
            for r in read_rows(results / "aggregation_execution_metrics.csv")
            if r["Phase"] == "source_debit"
        }
        beacon = {
            r["BatchID"]: int(r["BeaconFinalizedAtUnixNano"])
            for r in read_rows(results / "aggregation_batch_metrics.csv")
        }
        for row in read_rows(results / "aggregation_tx_lifecycle.csv"):
            add(
                row["PaymentID"],
                int(row["CreatedAtUnixNano"]),
                source[row["PaymentID"]],
                beacon[row["BatchID"]],
                int(row["FinalCommittedAtUnixNano"]),
                True,
            )
    expected = case["summary"]["completed_txs"]
    if len(ids) != expected:
        raise ValueError(f"incomplete lifecycles: {run}")
    stages, spans = np.asarray(stages), np.asarray(spans, dtype=np.int64)
    latency = (spans[:, 1] - spans[:, 0]) / 1e9
    if not np.allclose(
        stages.sum(axis=1), latency, atol=1e-8, rtol=0
    ) or not np.isclose(
        latency.mean() * 1000, case["summary"]["latency_mean_ms"], atol=1e-5, rtol=0
    ):
        raise ValueError(f"lifecycle summary mismatch: {run}")
    start, end = spans[:, 0].min(), spans[:, 1].max()
    grid = np.append(np.arange(start, end, 500_000_000, dtype=np.int64), end)
    pool = np.zeros(len(grid), dtype=np.int64)
    for shard in range(8):
        rows = sorted(
            (parse_time(r["Timestamp"]), int(r["TxCount"]))
            for r in read_rows(
                run / f"block_record/shard={shard}_node=0/txpool_record.csv"
            )
        )
        stamps, counts = np.asarray(rows, dtype=np.int64).T
        if stamps[0] > start or stamps[-1] < end:
            raise ValueError(f"pool coverage incomplete: {run}, shard {shard}")
        pool += counts[np.searchsorted(stamps, grid, side="right") - 1]
    return dict(
        stages=stages,
        latency=latency,
        cross=np.asarray(cross, dtype=bool),
        pool=pool,
        time=(grid - start) / 1e9,
    )


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("suite", type=Path)
    p.add_argument(
        "--output", type=Path, default=ROOT / "figures/newcode-scaling-20261027-215607"
    )
    a = p.parse_args()
    suite, out = a.suite.resolve(), a.output.resolve()
    out.mkdir(parents=True, exist_ok=False)
    plan = json.loads((suite / "plan.json").read_text())
    cases = plan["cases"]
    if {(c["shards"], c["mode"]) for c in cases} != {
        (s, m) for s in SHARDS for m in MODES
    }:
        raise ValueError("suite is missing requested shard/method combinations")
    for c in cases:
        # Resolve from the supplied suite so the plan can also be inspected locally.
        c["path"] = str(
            suite / f"shards-{c['shards']}/repeat-{c['repeat']:02}/{c['mode']}"
        )
        run = Path(c["path"])
        v = json.loads((run / "validation.json").read_text())
        c["summary"] = json.loads((run / "summary.json").read_text())
        if (
            v["completed_originals"] != v["expected"]
            or v["duplicate_originals"]
            or v["warning_error_count"]
        ):
            raise ValueError(f"invalid run: {run}")
        manifest = json.loads((run / "manifest.json").read_text())
        if any(
            manifest[k] != plan["provenance"][k]
            for k in ("dataset_sha256", "binary_sha256")
        ):
            raise ValueError(f"mixed dataset/binaries: {run}")
    write_json(
        out / "source_audit.json",
        dict(
            source=str(suite),
            cases=len(cases),
            policy_versions=[
                dict(
                    shards=c["shards"],
                    repeat=c["repeat"],
                    mode=c["mode"],
                    versions=c["summary"]["policy_versions"],
                    relay2=c["summary"]["Relay2TxCount"],
                )
                for c in cases
                if c["mode"] in ("full", "adaptive")
            ],
        ),
    )
    write_csv(
        out / "performance_runs.csv",
        [dict(shards=c["shards"], repeat=c["repeat"], **c["summary"]) for c in cases],
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
            "svg.fonttype": "none",
            "savefig.dpi": 300,
            "axes.axisbelow": True,
        }
    )
    book = PdfPages(out / "all_figures.pdf")
    names = []

    def save(fig, name, note):
        fig.text(
            0.5, 0.012, note, ha="center", va="bottom", fontsize=8, color="#555555"
        )
        fig.tight_layout(rect=(0, 0.09, 1, 1))
        for suffix in ("png", "pdf", "svg"):
            fig.savefig(out / f"{name}.{suffix}", bbox_inches="tight")
        book.savefig(fig, bbox_inches="tight")
        plt.close(fig)
        names.append(name)

    def grid(ax, xlabel="Number of shards"):
        ax.set_xlabel(xlabel)
        ax.grid(alpha=0.18)
        ax.set_ylim(bottom=0)

    def line(ax, field, divisor=1, modes=MODES):
        for m in modes:
            i = MODES.index(m)
            means, errors = [], []
            for shard in SHARDS:
                values = [
                    c["summary"][field] / divisor
                    for c in cases
                    if c["mode"] == m and c["shards"] == shard
                ]
                means.append(np.mean(values))
                errors.append(np.std(values, ddof=1) if len(values) > 1 else 0)
            ax.errorbar(
                SHARDS,
                means,
                yerr=errors if any(errors) else None,
                label=LABELS[i],
                color=COLORS[i],
                marker=MARKERS[i],
                linestyle="--" if m == "adaptive" else "-",
                capsize=3,
            )
        ax.set_xticks(SHARDS)
        grid(ax)

    counts = sorted(
        {
            sum(c["shards"] == s and c["mode"] == m for c in cases)
            for s in SHARDS
            for m in MODES
        }
    )
    note = "300,000 original transactions per run; " + (
        "one run per configuration (no error bars)."
        if counts == [1]
        else "error bars: between-run sample SD."
    )
    for field, name, ylabel in (
        ("logical_tps", "01_tps", "Throughput (original tx/s)"),
        ("latency_mean_ms", "02_mean_latency", "Mean confirmation latency (s)"),
        ("latency_p95_ms", "02b_p95_latency", "P95 confirmation latency (s)"),
    ):
        fig, ax = plt.subplots(figsize=(6.6, 4))
        line(ax, field, 1 if field == "logical_tps" else 1000)
        ax.set_ylabel(ylabel)
        ax.legend(ncol=2, fontsize=9)
        save(fig, name, note)
    fig, axes = plt.subplots(1, 2, figsize=(11, 3.8))
    line(axes[0], "logical_tps")
    axes[0].set_ylabel("Throughput (original tx/s)")
    line(axes[1], "latency_mean_ms", 1000)
    axes[1].set_ylabel("Mean confirmation latency (s)")
    axes[0].legend(ncol=2, fontsize=8)
    save(fig, "00_tps_latency", note)
    savings, beacon = [], []
    for c in cases:
        if c["mode"] not in ("full", "adaptive"):
            continue
        s = c["summary"]
        base = s["Relay1TxCount"]
        savings.append(
            dict(
                shards=c["shards"],
                repeat=c["repeat"],
                mode=c["mode"],
                original_ctx=base,
                destination_entries=s["destination_entries"],
                destination_entries_saved=base - s["destination_entries"],
                receiver_updates=s["receiver_updates"],
                receiver_updates_saved=base - s["receiver_updates"],
            )
        )
        batches = list(
            read_rows(Path(c["path"]) / "results/aggregation_batch_metrics.csv")
        )
        total = lambda k: sum(int(r[k]) for r in batches)
        if (
            total("ReceiptCount") != s["aggregation_originals"]
            or total("SettlementTxCount") != s["AggregationSettlementTxCount"]
        ):
            raise ValueError("batch counters do not match completed originals")
        n = len(batches)
        beacon.append(
            dict(
                shards=c["shards"],
                repeat=c["repeat"],
                mode=c["mode"],
                batches=n,
                build_ms=total("BuildTimeNs") / n / 1e6,
                validation_ms=total("LocalValidationTimeNs") / n / 1e6,
                processing_ms=(total("BuildTimeNs") + total("LocalValidationTimeNs"))
                / n
                / 1e6,
                consensus_s=total("BeaconConsensusLatencyNs") / n / 1e9,
                pbft_mib=sum(
                    total(k) for k in ("PreprepareBytes", "PrepareBytes", "CommitBytes")
                )
                / 2**20,
                storage_mib=(total("BatchStorageBytes") + total("SidecarBytes"))
                / 2**20,
            )
        )
    write_csv(out / "savings.csv", savings)
    write_csv(out / "beacon_cost.csv", beacon)
    for field, name, ylabel in (
        (
            "destination_entries_saved",
            "03_destination_entries_saved",
            "Destination transaction entries saved",
        ),
        (
            "receiver_updates_saved",
            "04_receiver_updates_saved",
            "Receiver account updates saved",
        ),
    ):
        fig, ax = plt.subplots(figsize=(6.6, 4))
        for m in ("full", "adaptive"):
            i = MODES.index(m)
            values = [
                np.mean(
                    [r[field] for r in savings if r["mode"] == m and r["shards"] == s]
                )
                for s in SHARDS
            ]
            ax.plot(
                SHARDS,
                values,
                color=COLORS[i],
                marker=MARKERS[i],
                linestyle="--" if m == "adaptive" else "-",
                label=LABELS[i],
            )
        ax.set_xticks(SHARDS)
        grid(ax)
        ax.set_ylabel(ylabel)
        ax.legend()
        save(
            fig,
            name,
            "Within-run counterfactual: one target entry / receiver update per original cross-shard transaction.",
        )
    metrics = {}
    stage_rows = []
    distribution = []
    pool_rows = []
    for m in MODES:
        c = sorted(
            (c for c in cases if c["shards"] == 8 and c["mode"] == m),
            key=lambda c: c["repeat"],
        )[0]
        print(f"Reading 8-shard lifecycles: {m}", flush=True)
        v = lifecycle(c)
        metrics[m] = v
        for scope, mask in (
            ("all_originals", np.ones(len(v["latency"]), dtype=bool)),
            ("cross_shard_only", v["cross"]),
        ):
            means = v["stages"][mask].mean(axis=0)
            stage_rows.append(
                dict(
                    mode=m,
                    scope=scope,
                    n=int(mask.sum()),
                    source_s=means[0],
                    coordination_s=means[1],
                    destination_s=means[2],
                    total_s=sum(means),
                )
            )
        q = np.quantile(
            v["latency"], [0.05, 0.25, 0.5, 0.75, 0.95, 0.99], method="inverted_cdf"
        )
        distribution.append(
            dict(
                mode=m,
                n=len(v["latency"]),
                mean_s=v["latency"].mean(),
                **dict(
                    zip(("p05_s", "p25_s", "median_s", "p75_s", "p95_s", "p99_s"), q)
                ),
            )
        )
        pool_rows.extend(
            dict(mode=m, time_s=t, pool_entries=int(n))
            for t, n in zip(v["time"], v["pool"])
        )
    write_csv(out / "shard8_stage_latency.csv", stage_rows)
    write_csv(out / "shard8_latency_distribution.csv", distribution)
    write_csv(out / "shard8_txpool_timeseries.csv", pool_rows)
    fig, ax = plt.subplots(figsize=(7, 4))
    for i, m in enumerate(MODES):
        ax.plot(
            metrics[m]["time"],
            metrics[m]["pool"],
            label=LABELS[i],
            color=COLORS[i],
            linestyle="--" if m == "adaptive" else "-",
        )
    grid(ax, "Time since first original transaction creation (s)")
    ax.set_ylabel("Total transaction-pool entries")
    ax.legend(ncol=2)
    save(
        fig,
        "05_shard8_txpool",
        "8 shards; sum of 8 leaders, previous-observation alignment at 0.5 s. Beacon / in-flight work excluded.",
    )
    fig, ax = plt.subplots(figsize=(7.5, 4))
    rng = np.random.default_rng(20261028)
    arrays = [metrics[m]["latency"] for m in MODES]
    parts = ax.violinplot(
        [rng.choice(v, min(30000, len(v)), replace=False) for v in arrays],
        showextrema=False,
        positions=np.arange(5),
        points=140,
    )
    for i, (body, v) in enumerate(zip(parts["bodies"], arrays)):
        body.set_facecolor(COLORS[i])
        body.set_edgecolor(COLORS[i])
        body.set_alpha(0.7)
        lo, q1, median, q3, hi = np.quantile(
            v, [0.05, 0.25, 0.5, 0.75, 0.95], method="inverted_cdf"
        )
        ax.vlines(i, lo, hi, color="#333333", lw=1)
        ax.vlines(i, q1, q3, color="#333333", lw=4)
        ax.scatter(i, median, c="white", edgecolor="#333333", s=22, zorder=4)
        ax.scatter(i, v.mean(), c="black", marker="D", s=16, zorder=4)
    ax.set_xticks(np.arange(5), LABELS)
    ax.set_ylabel("Confirmation latency (s)")
    grid(ax, "")
    save(
        fig,
        "06_shard8_latency_violin",
        "8 shards; 300,000 originals per method. KDE: seeded 30,000 sample; exact median / mean / IQR / P5-P95 overlays.",
    )
    for scope, name in (
        ("all_originals", "07_shard8_stage_latency"),
        ("cross_shard_only", "07b_shard8_ctx_stage_latency"),
    ):
        fig, ax = plt.subplots(figsize=(7.5, 4.3))
        bottom = np.zeros(5)
        selected = {r["mode"]: r for r in stage_rows if r["scope"] == scope}
        for key, label, color in (
            ("source_s", "Source confirmation", "#0072B2"),
            ("coordination_s", "Beacon coordination", "#E69F00"),
            ("destination_s", "Post-source / destination", "#009E73"),
        ):
            values = np.array([selected[m][key] for m in MODES])
            ax.bar(
                np.arange(5), values, bottom=bottom, label=label, color=color, width=0.6
            )
            bottom += values
        for i, v in enumerate(bottom):
            ax.text(i, v + 0.3, f"{v:.2f}", ha="center", fontsize=9)
        ax.set_xticks(np.arange(5), LABELS)
        ax.set_ylabel("Mean stage latency (s)")
        grid(ax, "")
        ax.set_ylim(0, max(bottom) * 1.25)
        ax.legend(ncol=3, fontsize=8, loc="upper center")
        save(
            fig,
            name,
            "8 shards; "
            + scope.replace("_", " ")
            + ". Relay/Broker transit is included in post-source time; stages sum to end-to-end latency.",
        )
    fig, axes = plt.subplots(2, 2, figsize=(10, 7))
    for ax, key, ylabel in zip(
        axes.flat,
        ("processing_ms", "consensus_s", "pbft_mib", "storage_mib"),
        (
            "Build + local validation (ms/batch)",
            "PBFT consensus (s/batch)",
            "Estimated PBFT traffic (MiB/run)",
            "Serialized block + sidecar (MiB/replica)",
        ),
    ):
        for m in ("full", "adaptive"):
            i = MODES.index(m)
            ax.plot(
                SHARDS,
                [
                    np.mean(
                        [r[key] for r in beacon if r["mode"] == m and r["shards"] == s]
                    )
                    for s in SHARDS
                ],
                label=LABELS[i],
                color=COLORS[i],
                marker=MARKERS[i],
                linestyle="--" if m == "adaptive" else "-",
            )
        ax.set_xticks(SHARDS)
        ax.set_ylabel(ylabel)
        grid(ax)
    axes[0, 0].legend(fontsize=8)
    save(
        fig,
        "08_beacon_cost",
        "Elapsed processing time, not CPU time. PBFT traffic is the instrumented size model; storage excludes database overhead.",
    )
    book.close()
    switches = sum(
        c["summary"]["policy_versions"] for c in cases if c["mode"] == "adaptive"
    )
    text = f"# New-code scaling figures\n\nSource: {suite}\n\n{note}\n\nAdaptive policy versions across runs: {switches}. Full/adaptive differences without switches are run-to-run differences, not evidence of adaptive-routing benefit. All runs passed completion/duplicate/warning checks; dataset and binary hashes match the suite provenance.\n\nSavings use each proposed run's original CTX count as a counterfactual, not another baseline's account placement. Only receiver updates are claimed; source debits are not counted as saved. Violin and stage/pool figures use the first 8-shard run per method. Ordinary routes have no isolated coordination boundary, so transit and target queueing remain in the post-source stage.\n\nReproduce:\n\n```bash\n/opt/anaconda3/bin/python3 figures/gen_fig_newcode_scaling.py '{suite}' --output /path/to/new/output\n```\n\n"
    text += "\n".join(f"- [{n}.pdf]({n}.pdf)" for n in names) + "\n"
    (out / "README.md").write_text(text)
    (out / "index.html").write_text(
        "<!doctype html><meta charset='utf-8'><title>New-code scaling figures</title><style>body{max-width:1100px;margin:32px auto;font:16px sans-serif}img{width:100%}section{margin:40px 0}</style><h1>New-code scaling figures</h1><p>"
        + note
        + f" Adaptive policy versions: {switches}.</p>"
        + "".join(
            f"<section><h2>{n}</h2><a href='{n}.pdf'>PDF</a><img src='{n}.png'></section>"
            for n in names
        )
    )
    print(f"Figures: {out}", flush=True)


if __name__ == "__main__":
    main()
