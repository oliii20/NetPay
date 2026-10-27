# Aggregation diagnostics

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
