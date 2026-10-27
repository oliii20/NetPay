// Package aggregationstats collects Beacon aggregation and target settlement
// measurements into deterministic CSV snapshots.
package aggregationstats

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/routing"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/csvwrite"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
)

const (
	BatchMetricsFile     = "aggregation_batch_metrics.csv"
	ExecutionMetricsFile = "aggregation_execution_metrics.csv"
	LifecycleMetricsFile = "aggregation_tx_lifecycle.csv"
	RoutingMetricsFile   = "aggregation_routing_policy.csv"
)

var batchHeaders = []string{
	"WindowID", "BatchID", "CloseReason", "ReceiptCount", "SourceAbstractCount", "SourceCollapsedPaymentCount", "CreditCount",
	"CollapsedReceiptCount", "SettlementTxCount", "ShardBlockCounts", "Cuts",
	"WatermarkSkew", "WindowOpenDurationNs", "AggregationTimeNs", "MerkleBuildTimeNs",
	"BatchProposalBytes", "SidecarBytes", "ValidationTimeNs", "BeaconConsensusLatencyNs",
	"PreprepareBytes", "PrepareBytes", "CommitBytes", "BatchStorageBytes", "BeaconFinalizedAtUnixNano",
	"WindowOpenedAtUnixNano", "WindowSealedAtUnixNano", "BuildStartedAtUnixNano", "ProposalReadyAtUnixNano",
	"FrozenQueueWaitNs", "BuildTimeNs", "LocalValidationTimeNs", "FrozenWindowQueueLength",
	"ReceiptTimingCount", "SourceToHandlerMeanNs", "HandlerToBuildMeanNs",
	"SupervisorNotifyTimeNs", "HeaderBroadcastTimeNs", "PackageDispatchTimeNs", "CommitDeliveryTimeNs",
}

var executionHeaders = []string{
	"Phase", "PaymentID", "BatchID", "WindowID", "ShardID", "ChunkIndex", "ItemCount", "UniqueAccountCount",
	"CreatedAtUnixNano", "CommittedAtUnixNano", "StateReadCount", "StateWriteCount",
	"ProofVerificationTimeNs", "ReceiverUpdateCount",
}

var lifecycleHeaders = []string{
	"PaymentID", "BatchID", "CreatedAtUnixNano", "FinalCommittedAtUnixNano", "LatencyNs",
}

type batchRecord struct {
	batch             model.AggregationBatchMetric
	beacon            model.AggregationBeaconMetric
	settlementTxCount int
}

type Collector struct {
	routingPolicies      map[uint64]routing.Policy
	outputDir            string
	mu                   sync.Mutex
	batches              map[merkle.Hash]*batchRecord
	execution            []model.AggregationExecutionMetric
	seen                 map[string]struct{}
	sourceCreated        map[paymentmodel.ID]time.Time
	batchPayments        map[merkle.Hash][]paymentmodel.ID
	batchAppliedReceipts map[merkle.Hash]int
	batchCommittedAt     map[merkle.Hash]time.Time
	completedAt          map[paymentmodel.ID]time.Time
	paymentBatch         map[paymentmodel.ID]merkle.Hash
	paymentSettlements   map[merkle.Hash][]message.PaymentSettlement
	appliedChunks        map[string]time.Time
}

func New(outputDir string) *Collector {
	return &Collector{
		routingPolicies:      make(map[uint64]routing.Policy),
		outputDir:            outputDir,
		paymentSettlements:   make(map[merkle.Hash][]message.PaymentSettlement),
		appliedChunks:        make(map[string]time.Time),
		batches:              make(map[merkle.Hash]*batchRecord),
		seen:                 make(map[string]struct{}),
		sourceCreated:        make(map[paymentmodel.ID]time.Time),
		batchPayments:        make(map[merkle.Hash][]paymentmodel.ID),
		batchAppliedReceipts: make(map[merkle.Hash]int),
		batchCommittedAt:     make(map[merkle.Hash]time.Time),
		completedAt:          make(map[paymentmodel.ID]time.Time),
		paymentBatch:         make(map[paymentmodel.ID]merkle.Hash),
	}
}

func (c *Collector) UpdateMeasureRecord(wrapped *rpcserver.WrappedMsg) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch wrapped.GetMsgType() {
	case message.AggregationRoutingPolicyMessageType:
		var msg message.AggregationRoutingPolicyMsg
		if err := decode(wrapped, &msg); err != nil {
			return err
		}

		if msg.NodeID != 0 {
			return fmt.Errorf("routing metric not from Beacon leader")
		}

		c.routingPolicies[msg.Policy.Version] = msg.Policy.Clone()
	case message.AggregationBatchMetricMessageType:
		var msg message.AggregationBatchMetricMsg
		if err := decode(wrapped, &msg); err != nil {
			return err
		}

		c.batch(msg.Metric.BatchID).batch = msg.Metric
	case message.AggregationBeaconMetricMessageType:
		var msg message.AggregationBeaconMetricMsg
		if err := decode(wrapped, &msg); err != nil {
			return err
		}

		c.batch(msg.Metric.BatchID).beacon = msg.Metric
	case message.AggregationExecutionMetricMessageType:
		var msg message.AggregationExecutionMetricMsg
		if err := decode(wrapped, &msg); err != nil {
			return err
		}

		for _, metric := range msg.Metrics {
			key := fmt.Sprintf(
				"%s:%x:%x:%d:%d:%d:%d",
				metric.Phase,
				metric.PaymentID,
				metric.BatchID,
				metric.ShardID,
				metric.ChunkIndex,
				metric.ItemCount,
				metric.CommittedAt.UnixNano(),
			)
			if _, exists := c.seen[key]; exists {
				continue
			}

			c.seen[key] = struct{}{}

			c.execution = append(c.execution, metric)
			if metric.Phase == model.MetricPhaseSourceDebit {
				c.sourceCreated[metric.PaymentID] = metric.CreatedAt
			}
		}

		for _, metric := range msg.SettlementTxs {
			key := fmt.Sprintf("tx:%x:%d:%d", metric.BatchID, metric.ShardID, metric.ChunkIndex)
			if _, exists := c.seen[key]; exists {
				continue
			}

			c.seen[key] = struct{}{}
			c.batch(metric.BatchID).settlementTxCount++
		}
	case message.AggregationFinalizedMessageType:
		var msg message.AggregationFinalizedMsg
		if err := decode(wrapped, &msg); err != nil {
			return err
		}

		c.paymentSettlements[msg.Header.BatchID] = msg.Settlements

		c.batchPayments[msg.Header.BatchID] = append([]paymentmodel.ID(nil), msg.PaymentIDs...)
		for _, id := range msg.PaymentIDs {
			c.paymentBatch[id] = msg.Header.BatchID
		}

		c.reconcileBatch(msg.Header.BatchID)
	case message.AggregationAppliedMessageType:
		var msg message.AggregationAppliedMsg
		if err := decode(wrapped, &msg); err != nil {
			return err
		}

		for _, chunk := range msg.Chunks {
			key := fmt.Sprintf("applied:%x:%d:%d", chunk.BatchID, chunk.ShardID, chunk.ChunkIndex)
			if _, exists := c.seen[key]; exists {
				continue
			}

			c.seen[key] = struct{}{}
			c.appliedChunks[key] = chunk.CommittedAt

			c.batchAppliedReceipts[chunk.BatchID] += chunk.ReceiptCount
			if chunk.CommittedAt.After(c.batchCommittedAt[chunk.BatchID]) {
				c.batchCommittedAt[chunk.BatchID] = chunk.CommittedAt
			}

			c.reconcileBatch(chunk.BatchID)
		}
	}

	return nil
}

func (c *Collector) OutputResultAndClose() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := csvwrite.WriteAllToCSVReplace(filepath.Join(c.outputDir, BatchMetricsFile), batchHeaders, c.batchRows()); err != nil {
		return fmt.Errorf("write aggregation batch metrics: %w", err)
	}

	if err := csvwrite.WriteAllToCSVReplace(filepath.Join(c.outputDir, ExecutionMetricsFile), executionHeaders, c.executionRows()); err != nil {
		return fmt.Errorf("write aggregation execution metrics: %w", err)
	}

	if err := csvwrite.WriteAllToCSVReplace(filepath.Join(c.outputDir, LifecycleMetricsFile), lifecycleHeaders, c.lifecycleRows()); err != nil {
		return fmt.Errorf("write aggregation lifecycle metrics: %w", err)
	}

	if err := c.writeRoutingPolicies(); err != nil {
		return err
	}

	return nil
}

func (c *Collector) reconcileBatch(batchID merkle.Hash) {
	if assignments := c.paymentSettlements[batchID]; len(assignments) > 0 {
		for _, assignment := range assignments {
			key := fmt.Sprintf("applied:%x:%d:%d", batchID, assignment.ShardID, assignment.ChunkIndex)
			if committedAt, ok := c.appliedChunks[key]; ok {
				c.completedAt[assignment.PaymentID] = committedAt
			}
		}

		return
	}

	ids, exists := c.batchPayments[batchID]
	if !exists || c.batchAppliedReceipts[batchID] < len(ids) || c.batchCommittedAt[batchID].IsZero() {
		return
	}

	for _, id := range ids {
		c.completedAt[id] = c.batchCommittedAt[batchID]
	}
}

func (c *Collector) batch(id merkle.Hash) *batchRecord {
	record := c.batches[id]
	if record == nil {
		record = &batchRecord{}
		c.batches[id] = record
	}

	return record
}

func (c *Collector) batchRows() [][]string {
	ids := make([]merkle.Hash, 0, len(c.batches))
	for id := range c.batches {
		ids = append(ids, id)
	}

	sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })

	rows := make([][]string, 0, len(ids))
	for _, id := range ids {
		record := c.batches[id]
		batch, beacon := record.batch, record.beacon

		windowID := batch.WindowID
		if windowID == 0 {
			windowID = beacon.WindowID
		}

		rows = append(rows, []string{
			strconv.FormatUint(windowID, 10), id.String(), batch.CloseReason,
			strconv.Itoa(batch.ReceiptCount), strconv.Itoa(batch.SourceAbstractCount),
			strconv.Itoa(batch.SourceCollapsedCount), strconv.Itoa(batch.CreditCount),
			strconv.Itoa(batch.CollapsedReceiptCount), strconv.Itoa(record.settlementTxCount),
			shardValues(batch.Shards, false), shardValues(batch.Shards, true),
			strconv.FormatUint(batch.WatermarkSkew, 10), strconv.FormatInt(batch.WindowOpenDuration.Nanoseconds(), 10),
			strconv.FormatInt(
				batch.AggregationTime.Nanoseconds(),
				10,
			), strconv.FormatInt(batch.MerkleBuildTime.Nanoseconds(), 10),
			strconv.Itoa(batch.BatchProposalBytes), strconv.Itoa(batch.SidecarBytes),
			strconv.FormatInt(
				beacon.ValidationTime.Nanoseconds(),
				10,
			), strconv.FormatInt(beacon.BeaconConsensusLatency.Nanoseconds(), 10),
			strconv.Itoa(beacon.PreprepareBytes), strconv.Itoa(beacon.PrepareBytes), strconv.Itoa(beacon.CommitBytes),
			strconv.Itoa(beacon.BatchStorageBytes), strconv.FormatInt(beacon.FinalizedAt.UnixNano(), 10),
			strconv.FormatInt(
				batch.WindowOpenedAt.UnixNano(),
				10,
			), strconv.FormatInt(batch.WindowSealedAt.UnixNano(), 10),
			strconv.FormatInt(
				batch.BuildStartedAt.UnixNano(),
				10,
			), strconv.FormatInt(batch.ProposalReadyAt.UnixNano(), 10),
			strconv.FormatInt(
				batch.FrozenQueueWait.Nanoseconds(),
				10,
			), strconv.FormatInt(batch.BuildTime.Nanoseconds(), 10),
			strconv.FormatInt(batch.LocalValidationTime.Nanoseconds(), 10), strconv.Itoa(batch.FrozenWindowQueueLength),
			strconv.FormatInt(
				batch.ReceiptTimingCount,
				10,
			), strconv.FormatInt(batch.SourceToHandlerMean.Nanoseconds(), 10),
			strconv.FormatInt(batch.HandlerToBuildMean.Nanoseconds(), 10),
			strconv.FormatInt(
				beacon.SupervisorNotifyTime.Nanoseconds(),
				10,
			), strconv.FormatInt(beacon.HeaderBroadcastTime.Nanoseconds(), 10),
			strconv.FormatInt(
				beacon.PackageDispatchTime.Nanoseconds(),
				10,
			), strconv.FormatInt(beacon.CommitDeliveryTime.Nanoseconds(), 10),
		})
	}

	return rows
}

func (c *Collector) executionRows() [][]string {
	rows := make([][]string, 0, len(c.execution))
	for _, metric := range c.execution {
		rows = append(rows, []string{
			metric.Phase, fmt.Sprintf("%x", metric.PaymentID), metric.BatchID.String(),
			strconv.FormatUint(metric.WindowID, 10),
			strconv.FormatInt(
				metric.ShardID,
				10,
			), strconv.FormatUint(uint64(metric.ChunkIndex), 10), strconv.Itoa(metric.ItemCount),
			strconv.Itoa(metric.UniqueAccountCount),
			strconv.FormatInt(metric.CreatedAt.UnixNano(), 10), strconv.FormatInt(metric.CommittedAt.UnixNano(), 10),
			strconv.FormatInt(metric.StateReadCount, 10), strconv.FormatInt(metric.StateWriteCount, 10),
			strconv.FormatInt(metric.ProofVerificationTime.Nanoseconds(), 10), strconv.Itoa(metric.ReceiverUpdateCount),
		})
	}

	return rows
}

func (c *Collector) lifecycleRows() [][]string {
	ids := make([]paymentmodel.ID, 0, len(c.completedAt))
	for id := range c.completedAt {
		ids = append(ids, id)
	}

	sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })

	rows := make([][]string, 0, len(ids))
	for _, id := range ids {
		createdAt, exists := c.sourceCreated[id]
		if !exists {
			continue
		}

		committedAt := c.completedAt[id]
		rows = append(rows, []string{
			fmt.Sprintf("%x", id), c.paymentBatch[id].String(),
			strconv.FormatInt(createdAt.UnixNano(), 10), strconv.FormatInt(committedAt.UnixNano(), 10),
			strconv.FormatInt(committedAt.Sub(createdAt).Nanoseconds(), 10),
		})
	}

	return rows
}

func shardValues(shards []model.ShardWindowMetric, cuts bool) string {
	parts := make([]string, len(shards))
	for idx, shard := range shards {
		value := shard.BlockCount
		if cuts {
			value = shard.CutHeight
		}

		parts[idx] = fmt.Sprintf("%d:%d", shard.ShardID, value)
	}

	return strings.Join(parts, ";")
}

func decode(wrapped *rpcserver.WrappedMsg, target any) error {
	if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", wrapped.GetMsgType(), err)
	}

	return nil
}
