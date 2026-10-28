#!/usr/bin/env python3
"""Audit and plot every repeat of a selected-method, four-shard scaling suite."""
import argparse
from collections import Counter
import json
from pathlib import Path
import sys

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.backends.backend_pdf import PdfPages
import numpy as np

from gen_fig_newcode_scaling import lifecycle

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "experiments/adaptive_aggregation"))
from common import read_rows, write_csv, write_json, sha256_file

MODES = ("adaptive", "full", "monoxide", "brokerchain", "clpa")
LABELS = ("Proposed (adaptive)", "Proposed (full)", "Monoxide", "BrokerChain", "CLPA")
COLORS = ("#D55E00", "#CC79A7", "#0072B2", "#E69F00", "#009E73")
MARKERS = ("o", "s", "^", "D", "v")
SHARDS = (4, 8, 16, 24)


def mean_sd(values):
    values = np.asarray(values, dtype=float)
    return float(values.mean()), float(values.std(ddof=1)) if len(values) > 1 else 0.0


def validate_grid(cases, repeats, modes=MODES):
    expected = Counter(
        (s, m, r) for s in SHARDS for m in modes for r in range(1, repeats + 1)
    )
    actual = Counter((c["shards"], c["mode"], c["repeat"]) for c in cases)
    if actual != expected:
        raise ValueError("suite must contain each shard/method/repeat exactly once")


def pool_average(runs):
    # Use the common covered horizon; never invent zero queues after a run ends.
    end = min(r["time"][-1] for r in runs)
    times = np.arange(0, end + 1e-9, 0.5)
    matrix = np.array(
        [r["pool"][np.searchsorted(r["time"], times, side="right") - 1] for r in runs]
    )
    sd = matrix.std(axis=0, ddof=1) if len(runs) > 1 else np.zeros(len(times))
    return times, matrix.mean(axis=0), sd


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("suite", type=Path)
    p.add_argument("--repeats", type=int, default=5)
    p.add_argument("--output", type=Path)
    p.add_argument("--methods", default=",".join(MODES))
    args = p.parse_args()
    selected_modes = tuple(args.methods.split(","))
    if (
        len(set(selected_modes)) != len(selected_modes)
        or not {"adaptive", "full"}.issubset(selected_modes)
        or any(m not in MODES for m in selected_modes)
    ):
        p.error("methods must be unique supported modes including adaptive and full")
    suite = args.suite.resolve()
    out = (args.output or suite / "figures").resolve()
    if out.exists():
        raise ValueError(f"figure output already exists: {out}")
    plan = json.loads((suite / "plan.json").read_text())
    cases = plan["cases"]
    validate_grid(cases, args.repeats, selected_modes)
    settings = None
    for c in cases:
        run = suite / f"shards-{c['shards']}/repeat-{c['repeat']:02}/{c['mode']}"
        c["path"] = str(run)
        c["summary"] = json.loads((run / "summary.json").read_text())
        v = json.loads((run / "validation.json").read_text())
        m = json.loads((run / "manifest.json").read_text())
        if (
            v["completed_originals"] != v["expected"]
            or v["duplicate_originals"]
            or v["warning_error_count"]
        ):
            raise ValueError(f"invalid run: {run}")
        for key in ("dataset_sha256", "binary_sha256"):
            if m[key] != plan["provenance"][key]:
                raise ValueError(f"mixed {key}: {run}")
        if sha256_file(run / "config.yaml") != m["config_sha256"]:
            raise ValueError(f"config changed: {run}")
        signature = {
            k: m["settings"].get(k)
            for k in (
                "tx_number",
                "per_shard_rate",
                "block_limit",
                "block_interval_ms",
                "beacon_interval_ms",
                "window_ms",
                "freeze_high_ms",
                "freeze_low_ms",
                "cooldown_ms",
                "recovery_window_ms",
                "recovery_windows",
                "recovery_change_percent",
                "min_destination_percent",
                "beacon_work_us",
            )
        }
        if settings is None:
            settings = signature
        if settings != signature:
            raise ValueError(f"mixed workload settings: {run}")
        if c["mode"] == "brokerchain" and m.get("broker_sha256") != plan[
            "provenance"
        ].get("broker_sha256"):
            raise ValueError(f"mixed broker list: {run}")
    out.mkdir(parents=True)
    write_json(
        out / "source_audit.json",
        dict(
            settings=settings,
            provenance=plan["provenance"],
            cases=len(cases),
            repeats=args.repeats,
            statistics="mean and between-run sample SD",
        ),
    )
    records = [
        dict(shards=c["shards"], repeat=c["repeat"], **c["summary"]) for c in cases
    ]
    write_csv(out / "performance_runs.csv", records)
    summaries = []
    for shard in SHARDS:
        for mode in selected_modes:
            rr = [r for r in records if r["shards"] == shard and r["mode"] == mode]
            result = dict(shards=shard, mode=mode, repeats=len(rr))
            for key in (
                "logical_tps",
                "latency_mean_ms",
                "latency_p95_ms",
                "latency_p99_ms",
            ):
                result[key + "_mean"], result[key + "_sd"] = mean_sd(
                    [r[key] for r in rr]
                )
            summaries.append(result)
    write_csv(out / "performance_summary.csv", summaries)
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
        }
    )
    note = f"{settings['tx_number']:,} valid originals/run; {args.repeats} repeats; error bars/shading: between-run sample SD."
    book = PdfPages(out / "all_figures.pdf")
    names = []

    def save(fig, name, caption=note):
        fig.text(0.5, 0.01, caption, ha="center", fontsize=8, color="#555555")
        fig.tight_layout(rect=(0, 0.07, 1, 1))
        for ext in ("png", "pdf", "svg"):
            fig.savefig(out / f"{name}.{ext}", bbox_inches="tight")
        book.savefig(fig, bbox_inches="tight")
        plt.close(fig)
        names.append(name)

    def scaling(ax, data, key, divisor=1, modes=selected_modes):
        for mode in modes:
            i = MODES.index(mode)
            stats = [
                mean_sd(
                    [
                        r[key] / divisor
                        for r in data
                        if r["shards"] == s and r["mode"] == mode
                    ]
                )
                for s in SHARDS
            ]
            ax.errorbar(
                SHARDS,
                [v[0] for v in stats],
                yerr=[v[1] for v in stats],
                color=COLORS[i],
                marker=MARKERS[i],
                capsize=3,
                label=LABELS[i],
                ls="--" if mode == "adaptive" else "-",
            )
        ax.set_xticks(SHARDS)
        ax.set_xlabel("Number of shards")
        ax.set_ylim(bottom=0)
        ax.grid(alpha=0.18)

    for key, name, label, divisor in (
        ("logical_tps", "01_tps", "Throughput (original tx/s)", 1),
        ("latency_mean_ms", "02_mean_latency", "Mean confirmation latency (s)", 1000),
        ("latency_p95_ms", "02b_p95_latency", "P95 confirmation latency (s)", 1000),
        ("latency_p99_ms", "02c_p99_latency", "P99 confirmation latency (s)", 1000),
    ):
        fig, ax = plt.subplots(figsize=(7, 4.3))
        scaling(ax, records, key, divisor)
        ax.set_ylabel(label)
        ax.legend(ncol=2, fontsize=8)
        save(fig, name)
    fig, axes = plt.subplots(1, 2, figsize=(12, 4.3))
    for ax, key, label, divisor in zip(
        axes,
        ["logical_tps", "latency_mean_ms"],
        ["Throughput (original tx/s)", "Mean latency (s)"],
        [1, 1000],
    ):
        scaling(ax, records, key, divisor)
        ax.set_ylabel(label)
    axes[0].legend(ncol=2, fontsize=8)
    save(fig, "00_tps_latency")

    savings, costs, routing = [], [], []
    for c in cases:
        if c["mode"] not in ("full", "adaptive"):
            continue
        s, run = c["summary"], Path(c["path"])
        base = dict(shards=c["shards"], repeat=c["repeat"], mode=c["mode"])
        savings.append(
            dict(
                **base,
                original_ctx=s["Relay1TxCount"],
                destination_entries=s["destination_entries"],
                target_entries_saved=s["Relay1TxCount"] - s["destination_entries"],
                receiver_updates=s["receiver_updates"],
                receiver_updates_saved=s["Relay1TxCount"] - s["receiver_updates"],
            )
        )
        batches = read_rows(run / "results/aggregation_batch_metrics.csv")
        batches = list(batches)
        if sum(int(r["ReceiptCount"]) for r in batches) != s["aggregation_originals"]:
            raise ValueError(f"batch count mismatch: {run}")
        if not batches:
            raise ValueError(f"no Beacon batches for cost analysis: {run}")
        total = lambda k: sum(int(r[k]) for r in batches)
        costs.append(
            dict(
                **base,
                batches=len(batches),
                build_ms=total("BuildTimeNs") / len(batches) / 1e6,
                validation_ms=total("LocalValidationTimeNs") / len(batches) / 1e6,
                processing_ms=(total("BuildTimeNs") + total("LocalValidationTimeNs"))
                / len(batches)
                / 1e6,
                consensus_s=total("BeaconConsensusLatencyNs") / len(batches) / 1e9,
                pbft_mib=sum(
                    total(k) for k in ("PreprepareBytes", "PrepareBytes", "CommitBytes")
                )
                / 2**20,
                storage_mib=(total("BatchStorageBytes") + total("SidecarBytes"))
                / 2**20,
            )
        )
        policies = list(read_rows(run / "results/aggregation_routing_policy.csv"))
        queue = list(read_rows(run / "results/aggregation_queue_timeseries.csv"))
        routing.append(
            dict(
                **base,
                reductions=sum(
                    r["Reason"] == "frozen_queue_overload" for r in policies
                ),
                recoveries=sum(
                    r["Reason"] == "frozen_queue_recovered" for r in policies
                ),
                relay2=s["Relay2TxCount"],
                mean_wait_s=s["service_queue_wait_mean_ms"] / 1000,
                peak_wait_s=s["service_queue_wait_max_ms"] / 1000,
                peak_queued=max(int(r["QueuedOriginals"]) for r in queue),
            )
        )
    for name, data in [
        ("savings", savings),
        ("beacon_cost", costs),
        ("routing_runs", routing),
    ]:
        write_csv(out / f"{name}.csv", data)
    for key, name, label in [
        (
            "target_entries_saved",
            "03_destination_entries_saved",
            "Destination transaction entries saved",
        ),
        (
            "receiver_updates_saved",
            "04_receiver_updates_saved",
            "Receiver account updates saved",
        ),
    ]:
        fig, ax = plt.subplots(figsize=(7, 4.3))
        scaling(ax, savings, key, modes=("adaptive", "full"))
        ax.set_ylabel(label)
        ax.legend()
        save(
            fig,
            name,
            note
            + "\nSavings relative to one target entry/update per original CTX in each run.",
        )
    for data, keys, labels, name in [
        (
            costs,
            ["processing_ms", "consensus_s", "pbft_mib", "storage_mib"],
            [
                "Build + validation (ms/batch)",
                "PBFT consensus (s/batch)",
                "Estimated PBFT traffic (MiB/run)",
                "Serialized block + sidecar (MiB/replica)",
            ],
            "08_beacon_cost",
        ),
        (
            routing,
            ["mean_wait_s", "peak_wait_s", "peak_queued", "relay2"],
            [
                "Mean service queue wait (s)",
                "Maximum service queue wait (s)",
                "Peak queued originals",
                "Ordinary Relay2 originals",
            ],
            "09_queue_and_routing",
        ),
    ]:
        fig, axes = plt.subplots(2, 2, figsize=(11, 7.2))
        for ax, key, label in zip(axes.flat, keys, labels):
            scaling(ax, data, key, modes=("adaptive", "full"))
            ax.set_ylabel(label)
        axes[0, 0].legend(fontsize=8)
        save(
            fig,
            name,
            note
            + (
                "\nElapsed processing, not CPU time; traffic is a size model, storage excludes DB overhead."
                if data is costs
                else ""
            ),
        )

    # Every 8-shard repeat contributes, rather than silently selecting repeat 1.
    stages, distributions, pool_rows, arrays = [], [], [], []
    fig_pool, ax_pool = plt.subplots(figsize=(8, 4.5))
    for mode in selected_modes:
        i = MODES.index(mode)
        runs = []
        for c in sorted(
            (c for c in cases if c["shards"] == 8 and c["mode"] == mode),
            key=lambda c: c["repeat"],
        ):
            print(f"Read 8-shard {mode} repeat {c['repeat']}", flush=True)
            v = lifecycle(c)
            runs.append(v)
            for scope, mask in [
                ("all_originals", np.ones(len(v["latency"]), dtype=bool)),
                ("cross_shard_only", v["cross"]),
            ]:
                means = v["stages"][mask].mean(axis=0)
                stages.append(
                    dict(
                        mode=mode,
                        repeat=c["repeat"],
                        scope=scope,
                        n=int(mask.sum()),
                        source_s=means[0],
                        coordination_s=means[1],
                        destination_s=means[2],
                        total_s=sum(means),
                    )
                )
            distributions.append(
                dict(
                    mode=mode,
                    repeat=c["repeat"],
                    n=len(v["latency"]),
                    mean_s=v["latency"].mean(),
                    **dict(
                        zip(
                            ("p05_s", "p25_s", "median_s", "p75_s", "p95_s", "p99_s"),
                            np.quantile(
                                v["latency"],
                                [0.05, 0.25, 0.5, 0.75, 0.95, 0.99],
                                method="inverted_cdf",
                            ),
                        )
                    ),
                )
            )
            pool_rows.extend(
                dict(mode=mode, repeat=c["repeat"], time_s=t, entries=int(n))
                for t, n in zip(v["time"], v["pool"])
            )
        arrays.append(np.concatenate([r["latency"] for r in runs]))
        times, mean, sd = pool_average(runs)
        ax_pool.plot(
            times,
            mean,
            color=COLORS[i],
            label=LABELS[i],
            ls="--" if mode == "adaptive" else "-",
        )
        ax_pool.fill_between(
            times, np.maximum(0, mean - sd), mean + sd, color=COLORS[i], alpha=0.12
        )
    for name, data in [
        ("shard8_stage_latency_runs", stages),
        ("shard8_latency_distribution_runs", distributions),
        ("shard8_txpool_runs", pool_rows),
    ]:
        write_csv(out / f"{name}.csv", data)
    ax_pool.set(
        xlabel="Time since first original creation (s)",
        ylabel="Total transaction-pool entries",
        ylim=(0, None),
    )
    ax_pool.grid(alpha=0.18)
    ax_pool.legend(ncol=2, fontsize=8)
    save(
        fig_pool,
        "05_shard8_txpool",
        note
        + "\n8 leaders summed; each method shown only over the horizon covered by all its repeats.",
    )
    fig, ax = plt.subplots(figsize=(9, 4.6))
    rng = np.random.default_rng(20261028)
    parts = ax.violinplot(
        [rng.choice(v, min(30000, len(v)), replace=False) for v in arrays],
        positions=np.arange(len(selected_modes)),
        showextrema=False,
        points=140,
    )
    for i, (body, v) in enumerate(zip(parts["bodies"], arrays)):
        color = COLORS[MODES.index(selected_modes[i])]
        body.set_facecolor(color)
        body.set_edgecolor(color)
        body.set_alpha(0.7)
        lo, q1, median, q3, hi = np.quantile(
            v, [0.05, 0.25, 0.5, 0.75, 0.95], method="inverted_cdf"
        )
        ax.vlines(i, lo, hi, color="#333333", lw=1)
        ax.vlines(i, q1, q3, color="#333333", lw=4)
        ax.scatter(i, median, color="white", edgecolor="#333333", s=22, zorder=4)
        ax.scatter(i, v.mean(), color="black", marker="D", s=16, zorder=4)
    ax.set(
        xticks=np.arange(len(selected_modes)),
        xticklabels=[
            LABELS[MODES.index(m)].replace(" (", "\n(") for m in selected_modes
        ],
        ylabel="Confirmation latency (s)",
        ylim=(0, None),
    )
    ax.grid(axis="y", alpha=0.18)
    save(
        fig,
        "06_shard8_latency_violin",
        f"8 shards; all {args.repeats} repeats pooled equally by original count. KDE: seeded 30k sample/method.\nExact pooled mean, median, IQR and P5–P95 overlays; not confidence intervals.",
    )
    for scope, name in [
        ("all_originals", "07_shard8_stage_latency"),
        ("cross_shard_only", "07b_shard8_ctx_stage_latency"),
    ]:
        fig, ax = plt.subplots(figsize=(9, 4.6))
        bottom = np.zeros(len(selected_modes))
        rr = [r for r in stages if r["scope"] == scope]
        for key, label, color in [
            ("source_s", "Source confirmation", "#0072B2"),
            ("coordination_s", "Beacon coordination", "#E69F00"),
            ("destination_s", "Post-source / destination", "#009E73"),
        ]:
            values = np.array(
                [
                    mean_sd([r[key] for r in rr if r["mode"] == m])[0]
                    for m in selected_modes
                ]
            )
            ax.bar(
                np.arange(len(selected_modes)),
                values,
                bottom=bottom,
                color=color,
                label=label,
                width=0.6,
            )
            bottom += values
        sd = [
            mean_sd([r["total_s"] for r in rr if r["mode"] == m])[1]
            for m in selected_modes
        ]
        ax.errorbar(
            np.arange(len(selected_modes)),
            bottom,
            yerr=sd,
            fmt="none",
            ecolor="#333333",
            capsize=4,
        )
        ax.set(
            xticks=np.arange(len(selected_modes)),
            xticklabels=[
                LABELS[MODES.index(m)].replace(" (", "\n(") for m in selected_modes
            ],
            ylabel="Mean stage latency (s)",
            ylim=(0, max(bottom + sd) * 1.28),
        )
        ax.legend(ncol=3, fontsize=8)
        ax.grid(axis="y", alpha=0.18)
        save(
            fig,
            name,
            note
            + f"\n8 shards, {scope.replace('_',' ')}; total-latency SD shown. Relay/Broker transit remains post-source.",
        )

    # Preserve per-repeat switches rather than averaging policy transition times.
    events = []
    for c in cases:
        if c["mode"] != "adaptive":
            continue
        run = Path(c["path"])
        start = c["summary"]["start_ns"]
        fig, axes = plt.subplots(3, 1, figsize=(10, 7.5), sharex=True)
        end = 0
        for mode in ("full", "adaptive"):
            other = run.parent / mode
            summary = json.loads((other / "summary.json").read_text())
            q = list(read_rows(other / "results/aggregation_queue_timeseries.csv"))
            t = np.array(
                [(int(r["ObservedAtUnixNano"]) - summary["start_ns"]) / 1e9 for r in q]
            )
            end = max(end, float(t[-1]))
            i = MODES.index(mode)
            axes[0].step(
                t,
                [int(r["QueuedOriginals"]) / 1000 for r in q],
                where="post",
                color=COLORS[i],
                label=LABELS[i],
            )
            axes[1].step(
                t,
                [int(r["FrozenQueueWaitNs"]) / 1e9 for r in q],
                where="post",
                color=COLORS[i],
            )
            for ax in axes:
                ax.axvline(
                    summary["injection_span_s"], color=COLORS[i], ls=":", alpha=0.6
                )
        pp = list(read_rows(run / "results/aggregation_routing_policy.csv"))
        times = [0]
        counts = [c["shards"]]
        for r in pp:
            t = (int(r["IssuedAtUnixNano"]) - start) / 1e9
            count = len(r["DestinationShards"].split(";"))
            down = count < counts[-1]
            events.append(
                dict(
                    shards=c["shards"],
                    repeat=c["repeat"],
                    time_s=t,
                    before=counts[-1],
                    after=count,
                    reason=r["Reason"],
                )
            )
            times.append(t)
            counts.append(count)
            for ax in axes:
                ax.axvline(t, color="#777777", ls="--", alpha=0.5)
            axes[2].annotate(
                f"{t:.2f}s",
                (t, count),
                xytext=(3, 7),
                textcoords="offset points",
                fontsize=8,
            )
            axes[2].scatter(
                t, count, marker="v" if down else "^", color="#333333", zorder=4
            )
        axes[2].step(
            times + [end], counts + [counts[-1]], where="post", color=COLORS[0]
        )
        axes[0].set_ylabel("Queued originals (k)")
        axes[0].legend(fontsize=8)
        axes[1].set_ylabel("Oldest frozen wait (s)")
        axes[2].set(
            ylabel="Aggregation destinations",
            xlabel="Time since first creation in each run (s)",
            ylim=(0, c["shards"] + 3),
        )
        for ax in axes:
            ax.grid(alpha=0.15)
            ax.set_xlim(0, end)
        axes[0].set_title(
            f"{c['shards']} shards, repeat {c['repeat']}: frozen queues and adaptive policy"
        )
        save(
            fig,
            f"10_shards{c['shards']}_repeat{c['repeat']:02}_switching",
            "Dashed: switches (▼ shedding, ▲ recovery). Dotted: input ends. Independent runs aligned by first creation.",
        )
    if events:
        write_csv(out / "switching_events.csv", events)
    else:
        (out / "switching_events.csv").write_text(
            "shards,repeat,time_s,before,after,reason\n"
        )
    book.close()
    text = f"# Repeated scaling figures\n\n{note}\n\nSource: {suite}\n\nAll 8-shard repeats contribute to pool, violin and stage figures. Error bars are sample SD, not confidence intervals. Raw per-run values are retained in CSV. No-switch cases remain full aggregation and are not evidence of switching gains. Ordinary post-source stages include transit and destination queueing.\n\n"
    text += "\n".join(f"- [{n}.pdf]({n}.pdf)" for n in names) + "\n"
    (out / "README.md").write_text(text)
    (out / "index.html").write_text(
        "<!doctype html><meta charset='utf-8'><title>Repeated scaling</title><style>body{max-width:1100px;margin:32px auto;font:16px sans-serif}img{width:100%}</style><h1>Repeated scaling</h1><p>"
        + note
        + "</p>"
        + "".join(
            f"<h2>{n}</h2><a href='{n}.pdf'>PDF</a><img src='{n}.png'>" for n in names
        )
    )
    print(f"Figures: {out}", flush=True)


if __name__ == "__main__":
    main()
