#!/usr/bin/env python3
"""Resume a suite after startup failure without rerunning completed cases."""
import argparse
import fcntl
import json
from pathlib import Path
import time

import yaml

from common import sha256_file, write_csv
from run import execute, make_config
from run_suite import finish_suite


def read_json(path):
    return Path(path).read_text(encoding="utf-8")


def resume_suite(root):
    root = Path(root).resolve()
    plan = json.loads(read_json(root / "plan.json"))
    shared = plan["provenance"]
    if not plan["cases"] or not shared:
        raise ValueError("cannot resume an empty or dry-run plan")
    # Keep a second continuation from renaming the same pending case.
    with (root / ".resume.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        manifests = [Path(c["path"]) / "manifest.json" for c in plan["cases"]]
        first = next((p for p in manifests if p.exists()), None)
        if first is None:
            raise ValueError("no original run manifest available")
        original_settings = json.loads(read_json(first))["settings"]
        bins = Path(original_settings["bins"]).resolve()
        for name, digest in shared["binary_sha256"].items():
            if sha256_file(bins / name) != digest:
                raise ValueError(f"original binary changed: {name}")
        dataset = Path(original_settings["dataset"]).resolve()
        print("Checking original dataset and binary hashes...", flush=True)
        if sha256_file(dataset) != shared["dataset_sha256"]:
            raise ValueError("original dataset changed")
        records = []
        for c in plan["cases"]:
            case = Path(c["path"]).resolve()
            if not case.is_relative_to(root):
                raise ValueError(f"case is outside suite: {case}")
            manifest_path = case / "manifest.json"
            manifest = (
                json.loads(read_json(manifest_path)) if manifest_path.exists() else None
            )
            settings = dict(manifest["settings"] if manifest else original_settings)
            settings.update(
                mode=c["mode"], shards=c["shards"], output=str(case), bins=str(bins)
            )
            args = argparse.Namespace(**settings)
            for key in ("dataset", "bins", "output", "broker_file"):
                setattr(args, key, Path(getattr(args, key)))
            if args.dataset.resolve() != dataset:
                raise ValueError("case has a different dataset")
            if manifest:
                if any(
                    manifest[k] != shared[k]
                    for k in ("dataset_sha256", "binary_sha256")
                ):
                    raise ValueError("case provenance differs from suite")
                if sha256_file(case / "config.yaml") != manifest["config_sha256"]:
                    raise ValueError(f"saved config changed: {case}")
                if yaml.safe_load((case / "config.yaml").read_text()) != make_config(
                    args, case
                ):
                    raise ValueError(
                        f"current generator differs from saved config: {case}"
                    )
            summary_path = case / "summary.json"
            if summary_path.exists():
                validation = json.loads(read_json(case / "validation.json"))
                summary = json.loads(read_json(summary_path))
                if (
                    not manifest
                    or validation["expected"] != args.tx_number
                    or validation["completed_originals"] != args.tx_number
                    or validation["duplicate_originals"]
                    or validation["warning_error_count"]
                    or summary["completed_txs"] != args.tx_number
                ):
                    raise ValueError(f"completed run failed validation: {case}")
                print(
                    f"Keeping completed {c['mode']} {c['shards']} shards: {case}",
                    flush=True,
                )
            else:
                if case.exists():
                    allowed = {
                        "config.yaml",
                        "ip_table.json",
                        "manifest.json",
                        "process_logs",
                        "failure.json",
                    }
                    if (
                        not (case / "failure.json").is_file()
                        or any(p.name not in allowed for p in case.iterdir())
                        or (
                            (case / "process_logs").exists()
                            and any((case / "process_logs").iterdir())
                        )
                    ):
                        raise ValueError(
                            f"incomplete run contains node output; refusing to replace it: {case}"
                        )
                    # Preserve even the failed preflight config and traceback.
                    backup = case.with_name(
                        case.name + f".failed-startup-{time.time_ns()}"
                    )
                    case.rename(backup)
                    print(f"Preserved startup failure: {backup}", flush=True)
                summary = execute(args, shared)
            records.append(
                dict(shards=c["shards"], repeat=c["repeat"], path=str(case), **summary)
            )
            write_csv(root / "comparison_runs.csv", records)
        finish_suite(root, records, plan["suite"])


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("suite", type=Path)
    resume_suite(parser.parse_args().suite)
