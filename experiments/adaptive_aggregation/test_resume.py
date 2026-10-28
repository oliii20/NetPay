import argparse
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import yaml

from common import sha256_file, write_json
from run import parser, make_config
from resume import resume_suite


class ResumeTest(unittest.TestCase):
    def fixture(self, root):
        dataset = root / "data.csv"
        dataset.write_text("original dataset\n")
        bins = root / "bin"
        bins.mkdir()
        for name in ("consensusnode", "beaconnode", "supervisor"):
            (bins / name).write_bytes(b"original binary")
        shared = dict(
            dataset_sha256=sha256_file(dataset),
            binary_sha256={
                name: sha256_file(bins / name)
                for name in ("consensusnode", "beaconnode", "supervisor")
            },
        )
        base = parser().parse_args(
            [
                "--dataset",
                str(dataset),
                "--bins",
                str(bins),
                "--tx-number",
                "2",
                "--shards",
                "24",
            ]
        )
        cases = []
        for mode in ("full", "adaptive"):
            case = root / f"shards-24/repeat-01/{mode}"
            case.mkdir(parents=True)
            args = argparse.Namespace(**vars(base))
            args.mode, args.output = mode, case
            (case / "config.yaml").write_text(yaml.safe_dump(make_config(args, case)))
            write_json(
                case / "manifest.json",
                dict(
                    shared,
                    config_sha256=sha256_file(case / "config.yaml"),
                    settings={
                        k: str(v) if isinstance(v, Path) else v
                        for k, v in vars(args).items()
                    },
                ),
            )
            if mode == "full":
                write_json(case / "summary.json", dict(mode=mode, completed_txs=2))
                write_json(
                    case / "validation.json",
                    dict(
                        expected=2,
                        completed_originals=2,
                        duplicate_originals=0,
                        warning_error_count=0,
                    ),
                )
            else:
                (case / "process_logs").mkdir()
                write_json(
                    case / "failure.json",
                    dict(error="[Errno 48] Address already in use"),
                )
            cases.append(dict(shards=24, repeat=1, mode=mode, path=str(case)))
        write_json(
            root / "plan.json", dict(suite="congestion", provenance=shared, cases=cases)
        )
        return cases, dataset, bins

    def test_resume_keeps_full_and_retries_only_unstarted_adaptive(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve()
            cases, _, bins = self.fixture(root)
            full = Path(cases[0]["path"]) / "summary.json"
            original = full.read_bytes()
            with patch(
                "resume.execute", return_value=dict(mode="adaptive", completed_txs=2)
            ) as execute, patch("resume.finish_suite") as finish:
                resume_suite(root)
                execute.assert_called_once()
                args = execute.call_args.args[0]
                self.assertEqual(args.mode, "adaptive")
                self.assertEqual(args.bins, bins)
                self.assertEqual(args.beacon_work_us, 0)
                finish.assert_called_once()
            self.assertEqual(full.read_bytes(), original)
            backups = list(
                (root / "shards-24/repeat-01").glob("adaptive.failed-startup-*")
            )
            self.assertEqual(len(backups), 1)
            self.assertIn(
                "Address already in use", (backups[0] / "failure.json").read_text()
            )

    def test_resume_rejects_changed_dataset_or_node_output(self):
        for changed_dataset in (True, False):
            with self.subTest(
                changed_dataset=changed_dataset
            ), tempfile.TemporaryDirectory() as directory:
                root = Path(directory).resolve()
                cases, dataset, _ = self.fixture(root)
                if changed_dataset:
                    dataset.write_text("changed")
                else:
                    (Path(cases[1]["path"]) / "process_logs/node.log").write_text(
                        "node already started"
                    )
                with patch("resume.execute") as execute, self.assertRaisesRegex(
                    ValueError, "dataset changed|contains node output"
                ):
                    resume_suite(root)
                execute.assert_not_called()
                self.assertTrue(Path(cases[1]["path"]).exists())


if __name__ == "__main__":
    unittest.main()
