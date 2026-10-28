from pathlib import Path
import csv
import tempfile
import socket
import unittest
from unittest.mock import patch
from run import parser, make_config
from analyze import relief, area, routing_trace
from common import validate_and_summarize, check_ports
from run_suite import prepare_binaries


class ExperimentsTest(unittest.TestCase):
    def test_port_probe_reuses_closed_connections_but_rejects_live_listener(self):
        with socket.socket() as listener:
            listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            listener.bind(("127.0.0.1", 0))
            listener.listen(1)
            address = listener.getsockname()
            table = {"0": {"0": f"127.0.0.1:{address[1]}"}}
            with self.assertRaises(OSError):
                check_ports(table)
            with socket.socket() as client:
                client.connect(address)
                accepted, _ = listener.accept()
                accepted.close()
                self.assertEqual(client.recv(1), b"")
        # The server actively closed an established connection: TIME_WAIT may
        # outlive its listener. A new Go-style reusable listener is still valid.
        check_ports(table)

    def test_full_control_completes_zero_value_relay_and_positive_aggregation(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory)

            def table(name, fields, rows):
                path = output / name
                path.parent.mkdir(parents=True, exist_ok=True)
                with path.open("w", newline="") as handle:
                    writer = csv.writer(handle)
                    writer.writerow(fields)
                    writer.writerows(rows)

            table(
                "results/relay_stats_detail_tx_info.csv",
                [
                    "OriginalHash",
                    "Tx create time",
                    "Tx finally commit time",
                    "Is cross-shard tx or not",
                ],
                [["a" * 64, "1970-01-01T00:00:01Z", "1970-01-01T00:00:02Z", "true"]],
            )
            table(
                "results/aggregation_tx_lifecycle.csv",
                [
                    "PaymentID",
                    "CreatedAtUnixNano",
                    "FinalCommittedAtUnixNano",
                    "LatencyNs",
                ],
                [["b" * 64, 1000000000, 3000000000, 2000000000]],
            )
            table("results/aggregation_routing_policy.csv", ["Version"], [])
            table(
                "results/aggregation_batch_metrics.csv",
                ["ServiceQueueWaitNs"],
                [[1000000]],
            )
            table(
                "results/aggregation_execution_metrics.csv",
                ["Phase", "ReceiverUpdateCount"],
                [["settlement", 1]],
            )
            fields = [
                "Relay1TxCount",
                "Relay2TxCount",
                "CrossShardPaymentTxCount",
                "AggregationSettlementTxCount",
                "Broker2TxCount",
                "ZeroValueRelay1TxCount",
                "ZeroValueRelay2TxCount",
            ]
            table(
                "block_record/shard=0_node=0/block_record.csv",
                fields,
                [[2, 1, 0, 1, 0, 1, 1]],
            )
            summary = validate_and_summarize(output, "full", 2)
            self.assertEqual(summary["completed_txs"], 2)
            self.assertEqual(summary["ZeroValueRelay2TxCount"], 1)
            self.assertEqual(summary["eligible_relay2_count"], 0)
            table(
                "block_record/shard=0_node=0/block_record.csv",
                fields,
                [[2, 1, 0, 1, 0, 0, 0]],
            )
            with self.assertRaisesRegex(ValueError, "always-full"):
                validate_and_summarize(output, "full", 2)
            # A zero-only workload legitimately finalizes no Beacon batches.
            table(
                "results/aggregation_tx_lifecycle.csv",
                [
                    "PaymentID",
                    "CreatedAtUnixNano",
                    "FinalCommittedAtUnixNano",
                    "LatencyNs",
                ],
                [],
            )
            table("results/aggregation_batch_metrics.csv", ["ServiceQueueWaitNs"], [])
            table(
                "results/aggregation_execution_metrics.csv",
                ["Phase", "ReceiverUpdateCount"],
                [],
            )
            table(
                "block_record/shard=0_node=0/block_record.csv",
                fields,
                [[1, 1, 0, 0, 0, 1, 1]],
            )
            summary = validate_and_summarize(output, "full", 1)
            self.assertEqual(summary["batches"], 0)
            self.assertIsNone(summary["service_queue_wait_mean_ms"])

    def test_zero_value_relay_is_not_counted_as_adaptive_shedding(self):
        with tempfile.TemporaryDirectory() as directory:
            run = Path(directory)
            log = run / "logs/shard=0_node=0/info.log"
            log.parent.mkdir(parents=True)
            log.write_text(
                'time=1970-01-01T00:00:02Z msg="source block routed after commitment" height=1 routing_version=1 aggregation_count=4 relay_count=3 zero_value_relay_count=3\n'
            )
            trace = routing_trace(run, 1000000000)
            self.assertEqual(trace[0]["relay2_sent"], 3)
            self.assertEqual(trace[0]["eligible_relay2_sent"], 0)

    def test_reuse_preserves_original_experiment_binaries(self):
        with tempfile.TemporaryDirectory() as directory:
            bins = Path(directory)
            for name in ("consensusnode", "beaconnode", "supervisor"):
                (bins / name).write_bytes(b"original experimental binary")
            with patch("run_suite.build_binaries") as build:
                self.assertEqual(
                    prepare_binaries(bins, bins / "unused"), bins.resolve()
                )
                build.assert_not_called()
            self.assertEqual(
                (bins / "consensusnode").read_bytes(), b"original experimental binary"
            )
            (bins / "supervisor").unlink()
            with self.assertRaisesRegex(ValueError, "missing reusable binary"):
                prepare_binaries(bins, bins / "unused")

    def test_same_path_control(self):
        args = parser().parse_args([])
        configs = []
        for mode in ("full", "adaptive"):
            args.mode = mode
            configs.append(make_config(args, Path("/tmp/case")))
        self.assertEqual(
            configs[0]["aggregation"]["adaptive_routing"].pop(
                "min_destination_percent"
            ),
            100,
        )
        self.assertEqual(
            configs[1]["aggregation"]["adaptive_routing"].pop(
                "min_destination_percent"
            ),
            25,
        )
        self.assertEqual(configs[0], configs[1])
        self.assertTrue(configs[0]["aggregation"]["adaptive_routing"]["enabled"])

    def test_baselines_have_no_beacon(self):
        for mode, consensus in (
            ("monoxide", "static_relay"),
            ("brokerchain", "static_broker"),
            ("clpa", "clpa_relay"),
        ):
            args = parser().parse_args(["--mode", mode, "--beacon-work-us", "100"])
            config = make_config(args, Path("/tmp/case"))
            self.assertEqual(config["system"]["consensus_type"], consensus)
            self.assertFalse(config["aggregation"]["enabled"])
            self.assertEqual(config["aggregation"]["experiment_work_us_per_payment"], 0)

    def test_every_method_uses_positive_payment_workload(self):
        for mode in ("monoxide", "brokerchain", "clpa", "full", "adaptive"):
            args = parser().parse_args(["--mode", mode])
            config = make_config(args, Path("/tmp/case"))
            self.assertTrue(
                config["supervisor"]["tx_source"].get("positive_value_only", False)
            )

    def test_final_drain_is_not_relief(self):
        rows = [
            dict(t=i, version=1, queued=q, wait=q)
            for i, q in enumerate((10, 10, 10, 8, 6, 4, 0))
        ]
        self.assertFalse(relief(rows, 3))
        self.assertTrue(relief(rows, 7))
        self.assertEqual(area(rows, 2), 20)

    def test_audit_rejects_duplicate_or_incomplete_originals(self):
        for duplicate in (False, True):
            with self.subTest(
                duplicate=duplicate
            ), tempfile.TemporaryDirectory() as directory:
                output = Path(directory)
                (output / "results").mkdir()
                with (output / "results/relay_stats_detail_tx_info.csv").open(
                    "w", newline=""
                ) as handle:
                    writer = csv.writer(handle)
                    writer.writerow(
                        ["OriginalHash", "Tx create time", "Tx finally commit time"]
                    )
                    row = ["a" * 64, "2026-10-27T00:00:00Z", "2026-10-27T00:00:01Z"]
                    writer.writerow(row)
                    if duplicate:
                        writer.writerow(row)
                with self.assertRaisesRegex(ValueError, "duplicate|completed 1"):
                    validate_and_summarize(output, "monoxide", 2)


if __name__ == "__main__":
    unittest.main()
