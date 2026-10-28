#!/usr/bin/env python3
"""Audit paired congestion evidence without mistaking final drain for relief."""
import argparse
import json
import re
from datetime import datetime
import yaml
from pathlib import Path
from common import read_rows, sha256_file, write_json, write_csv


def comparable_config(case):
    def normalize(value):
        if isinstance(value, dict):
            return {k: normalize(v) for k, v in value.items()}
        if isinstance(value, list):
            return [normalize(v) for v in value]
        return value.replace(str(case), "{CASE}") if isinstance(value, str) else value

    config = normalize(yaml.safe_load((case / "config.yaml").read_text()))
    config["aggregation"]["adaptive_routing"]["min_destination_percent"] = 25
    return config


def routing_trace(case, start):
    trace = []
    for path in (case / "logs").rglob("info.log"):
        with path.open() as handle:
            for line in handle:
                if 'msg="source block routed after commitment"' not in line:
                    continue
                fields = dict(
                    re.findall(
                        r"\b(time|height|routing_version|aggregation_count|relay_count|zero_value_relay_count)=(\S+)",
                        line,
                    )
                )
                trace.append(
                    dict(
                        t=datetime.fromisoformat(fields["time"]).timestamp()
                        - start / 1e9,
                        source=path.parent.name,
                        height=int(fields["height"]),
                        version=int(fields["routing_version"]),
                        aggregation_abstracts=int(fields["aggregation_count"]),
                        relay2_sent=int(fields["relay_count"]),
                        zero_value_relay2_sent=int(
                            fields.get("zero_value_relay_count", 0)
                        ),
                        eligible_relay2_sent=int(fields["relay_count"])
                        - int(fields.get("zero_value_relay_count", 0)),
                    )
                )
    return sorted(trace, key=lambda r: r["t"])


def samples(case):
    s = json.loads((case / "summary.json").read_text())
    return s, [
        dict(
            t=(int(r["ObservedAtUnixNano"]) - s["start_ns"]) / 1e9,
            wait=int(r["FrozenQueueWaitNs"]) / 1e9,
            queued=int(r["QueuedOriginals"]),
            version=int(r["PolicyVersion"]),
            destinations=int(r["AggregationDestinations"]),
        )
        for r in read_rows(case / "results/aggregation_queue_timeseries.csv")
    ]


def area(rows, horizon):
    return sum(
        a["wait"] * max(0, min(b["t"], horizon) - max(0, a["t"]))
        for a, b in zip(rows, rows[1:])
        if a["t"] < horizon and b["t"] > 0
    )


def relief(rows, injection_end):
    switched = [r for r in rows if r["version"] > 0 and 0 <= r["t"] < injection_end]
    # Require persistent reductions of both queued work and wait while input continues.
    peak = None
    for i, r in enumerate(switched):
        if peak is None or r["queued"] > peak["queued"]:
            peak = r
        following = switched[i : i + 3]
        if (
            peak["queued"] > 0
            and peak["wait"] > 0
            and len(following) == 3
            and all(
                x["queued"] < peak["queued"] and x["wait"] < peak["wait"]
                for x in following
            )
        ):
            return True
    return False


def analyze_suite(root):
    root = Path(root)
    plan = json.loads((root / "plan.json").read_text())
    reports = []
    for c in plan["cases"]:
        if c["mode"] != "adaptive":
            continue
        adaptive = Path(c["path"])
        full = adaptive.parent / "full"
        a, ar = samples(adaptive)
        f, fr = samples(full)
        am = json.loads((adaptive / "manifest.json").read_text())
        fm = json.loads((full / "manifest.json").read_text())
        ignored = {"mode", "output"}
        same_control = all(
            am[k] == fm[k] for k in ("dataset_sha256", "binary_sha256")
        ) and {k: v for k, v in am["settings"].items() if k not in ignored} == {
            k: v for k, v in fm["settings"].items() if k not in ignored
        }
        same_control = same_control and comparable_config(
            adaptive
        ) == comparable_config(full)
        for case, manifest in ((adaptive, am), (full, fm)):
            if "config_sha256" in manifest:
                same_control = (
                    same_control
                    and sha256_file(case / "config.yaml") == manifest["config_sha256"]
                )
        trace = routing_trace(adaptive, a["start_ns"])
        relay_after_switch = sum(r["relay2_sent"] for r in trace if r["version"] > 0)
        eligible_relay_after_switch = sum(
            r["eligible_relay2_sent"] for r in trace if r["version"] > 0
        )

        def eligible_count(summary):
            return summary["Relay2TxCount"] - summary.get("ZeroValueRelay2TxCount", 0)

        horizon = min(a["injection_span_s"], f["injection_span_s"])
        aa, fa = area(ar, horizon), area(fr, horizon)
        report = dict(
            shards=c["shards"],
            repeat=c["repeat"],
            same_code_workload_and_settings=same_control,
            both_complete=a["completed_txs"]
            == f["completed_txs"]
            == json.loads((adaptive / "validation.json").read_text())["expected"],
            full_is_always_aggregated=f["policy_versions"] == 0
            and eligible_count(f) == 0,
            switched=a["policy_versions"] > 0,
            ordinary_relay2_increased=eligible_count(a) > eligible_count(f),
            relay2_sent_after_switch=relay_after_switch,
            eligible_relay2_sent_after_switch=eligible_relay_after_switch,
            relief_during_injection=relief(ar, horizon),
            matched_injection_horizon_s=horizon,
            adaptive_queue_wait_area_s2=aa,
            full_queue_wait_area_s2=fa,
            lower_queue_wait_area=aa < fa,
            adaptive_tps=a["logical_tps"],
            full_tps=f["logical_tps"],
        )
        report["effective_evidence"] = all(
            report[k]
            for k in (
                "both_complete",
                "same_code_workload_and_settings",
                "full_is_always_aggregated",
                "switched",
                "ordinary_relay2_increased",
                "relief_during_injection",
                "lower_queue_wait_area",
            )
        )
        report["effective_evidence"] = (
            report["effective_evidence"] and eligible_relay_after_switch > 0
        )
        reports.append(report)
        write_csv(adaptive / "queue_relative_time.csv", ar)
        write_csv(full / "queue_relative_time.csv", fr)
        write_csv(adaptive / "source_routing_timeseries.csv", trace)
    write_json(root / "dynamic_validation.json", reports)
    write_csv(root / "dynamic_validation.csv", reports)
    print(json.dumps(reports, indent=2))
    return reports


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("output", type=Path)
    analyze_suite(p.parse_args().output)
