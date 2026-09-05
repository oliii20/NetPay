// Package nettingstats aggregates asynchronous protocol events into stable
// per-batch and per-intent CSV records.
package nettingstats

import (
	"bytes"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"math/big"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/csvwrite"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
)

const (
	BatchMetricsFile  = "netting_batch_metrics.csv"
	IntentMetricsFile = "netting_intent_metrics.csv"
)

var batchHeaders = []string{
	"WindowID", "BatchID", "CloseReason", "IntentCount", "ShardBlockCounts", "Cuts",
	"WatermarkSkew", "WindowOpenDurationNs", "FrozenWindowQueueLength",
	"ExactMatchedIntentCount", "BestFitMatchedIntentCount", "SplitMatchedIntentCount",
	"SplitAllocationCount", "MatchedIntentRatio", "MatchedValueRatio", "FallbackIntentRatio",
	"FallbackValueRatio", "MatchTimeNs", "MerkleBuildTimeNs", "BatchProposalBytes", "SidecarBytes",
	"BatchValidationTimeNs", "BeaconConsensusLatencyNs", "PreprepareBytes", "PrepareBytes", "CommitBytes",
	"MatchRootStorageBytes", "SettlementLatencyNs", "FallbackLatencyNs", "EndToEndLatencyNs",
	"StateReadCount", "StateWriteCount", "ProofVerificationTimeNs",
}

var intentHeaders = []string{
	"IntentID", "BatchID", "CreatedAt", "ReservedAt", "SettledAt", "FallbackAt", "CompletedAt",
	"ReservationLatencyNs", "CapitalLockDurationNs", "SettlementLatencyNs", "FallbackLatencyNs",
	"EndToEndLatencyNs", "UsedFallback",
}

type batchRecord struct {
	id     merkle.Hash
	batch  model.NettingBatchMetric
	beacon model.NettingBeaconMetric
}

type intentRecord struct {
	id           intent.ID
	batchID      merkle.Hash
	createdAt    time.Time
	reservedAt   time.Time
	settledAt    time.Time
	fallbackAt   time.Time
	completedAt  time.Time
	usedFallback bool
	reads        int64
	writes       int64
	proofTime    time.Duration
}

type Collector struct {
	outputDir string
	batches   map[merkle.Hash]*batchRecord
	intents   map[intent.ID]*intentRecord
	seen      map[string]struct{}
}

func New(outputDir string) *Collector {
	return &Collector{
		outputDir: outputDir,
		batches:   make(map[merkle.Hash]*batchRecord), intents: make(map[intent.ID]*intentRecord),
		seen: make(map[string]struct{}),
	}
}

func (c *Collector) UpdateMeasureRecord(wrapped *rpcserver.WrappedMsg) error {
	switch wrapped.GetMsgType() {
	case message.NettingBatchMetricMessageType:
		var msg message.NettingBatchMetricMsg
		if err := decode(wrapped, &msg); err != nil {
			return err
		}
		record := c.batch(msg.Metric.BatchID)
		record.batch = msg.Metric
	case message.NettingBeaconMetricMessageType:
		var msg message.NettingBeaconMetricMsg
		if err := decode(wrapped, &msg); err != nil {
			return err
		}
		record := c.batch(msg.Metric.BatchID)
		record.beacon = msg.Metric
	case message.NettingExecutionMetricMessageType:
		var msg message.NettingExecutionMetricMsg
		if err := decode(wrapped, &msg); err != nil {
			return err
		}
		for _, metric := range msg.Metrics {
			c.updateExecution(metric)
		}
	}

	return nil
}

func (c *Collector) OutputResultAndClose() error {
	if err := csvwrite.WriteAllToCSV(filepath.Join(c.outputDir, BatchMetricsFile), batchHeaders, c.batchRows()); err != nil {
		return fmt.Errorf("write netting batch metrics: %w", err)
	}
	if err := csvwrite.WriteAllToCSV(filepath.Join(c.outputDir, IntentMetricsFile), intentHeaders, c.intentRows()); err != nil {
		return fmt.Errorf("write netting intent metrics: %w", err)
	}

	return nil
}

func (c *Collector) updateExecution(metric model.NettingExecutionMetric) {
	key := fmt.Sprintf("%s:%x:%x:%d", metric.Phase, metric.BatchID, metric.IntentID, metric.ShardID)
	if _, exists := c.seen[key]; exists {
		return
	}
	c.seen[key] = struct{}{}
	entry := c.intents[metric.IntentID]
	if entry == nil {
		entry = &intentRecord{id: metric.IntentID}
		c.intents[metric.IntentID] = entry
	}
	entry.reads += metric.StateReadCount
	entry.writes += metric.StateWriteCount
	entry.proofTime += metric.ProofVerificationTime
	switch metric.Phase {
	case model.MetricPhaseReservation:
		entry.createdAt = earlier(entry.createdAt, metric.CreatedAt)
		entry.reservedAt = earlier(entry.reservedAt, metric.CommittedAt)
	case model.MetricPhaseSettlement:
		entry.batchID = metric.BatchID
		entry.settledAt = earlier(entry.settledAt, metric.CommittedAt)
		if metric.Final {
			entry.completedAt = earlier(entry.completedAt, metric.CommittedAt)
		} else {
			entry.usedFallback = true
			entry.fallbackAt = earlier(entry.fallbackAt, metric.CommittedAt)
			if entry.completedAt.IsZero() {
				entry.completedAt = metric.CommittedAt
			}
		}
	case model.MetricPhaseFallback:
		entry.batchID = metric.BatchID
		entry.usedFallback = true
		entry.fallbackAt = earlier(entry.fallbackAt, metric.CommittedAt)
		if entry.completedAt.IsZero() || metric.CommittedAt.After(entry.completedAt) {
			entry.completedAt = metric.CommittedAt
		}
	}
}

func (c *Collector) batch(id merkle.Hash) *batchRecord {
	record := c.batches[id]
	if record == nil {
		record = &batchRecord{id: id}
		c.batches[id] = record
	}

	return record
}

func (c *Collector) batchRows() [][]string {
	records := make([]*batchRecord, 0, len(c.batches))
	for _, record := range c.batches {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return windowID(records[i]) < windowID(records[j]) })
	rows := make([][]string, 0, len(records))
	for _, record := range records {
		batch := record.batch
		beacon := record.beacon
		batchID := record.id
		settlementLatency, fallbackLatency, endToEnd := c.batchLatencies(batchID)
		reads, writes, proofTime := c.batchExecution(batchID)
		rows = append(rows, []string{
			strconv.FormatUint(windowID(record), 10), batchID.String(), batch.CloseReason,
			strconv.Itoa(batch.IntentCount), shardCounts(batch.Shards), cutHeights(batch.Shards),
			strconv.FormatUint(batch.WatermarkSkew, 10), duration(batch.WindowOpenDuration),
			strconv.Itoa(batch.FrozenWindowQueueLength), strconv.Itoa(batch.ExactMatchedIntentCount),
			strconv.Itoa(batch.BestFitMatchedIntentCount), strconv.Itoa(batch.SplitMatchedIntentCount),
			strconv.Itoa(batch.SplitAllocationCount), ratioInt(batch.MatchedIntentCount, batch.IntentCount),
			ratioBig(batch.MatchedValue, batch.OriginalValue), ratioInt(batch.FallbackIntentCount, batch.IntentCount),
			ratioBig(batch.FallbackValue, batch.OriginalValue), duration(batch.MatchTime), duration(batch.MerkleBuildTime),
			strconv.Itoa(batch.BatchProposalBytes), strconv.Itoa(batch.SidecarBytes), duration(beacon.ValidationTime),
			duration(beacon.BeaconConsensusLatency), strconv.Itoa(beacon.PreprepareBytes), strconv.Itoa(beacon.PrepareBytes),
			strconv.Itoa(beacon.CommitBytes), strconv.Itoa(beacon.MatchRootStorageBytes), duration(settlementLatency),
			duration(fallbackLatency), duration(endToEnd), strconv.FormatInt(reads, 10),
			strconv.FormatInt(writes, 10), duration(proofTime),
		})
	}

	return rows
}

func windowID(record *batchRecord) uint64 {
	if record.batch.WindowID != 0 {
		return record.batch.WindowID
	}

	return record.beacon.WindowID
}

func (c *Collector) intentRows() [][]string {
	records := make([]*intentRecord, 0, len(c.intents))
	for _, record := range c.intents {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return bytes.Compare(records[i].id[:], records[j].id[:]) < 0 })
	rows := make([][]string, 0, len(records))
	for _, record := range records {
		rows = append(rows, []string{
			hex.EncodeToString(record.id[:]), record.batchID.String(), timestamp(record.createdAt), timestamp(record.reservedAt),
			timestamp(record.settledAt), timestamp(record.fallbackAt), timestamp(record.completedAt),
			durationBetween(record.createdAt, record.reservedAt), durationBetween(record.reservedAt, record.completedAt),
			durationBetween(record.reservedAt, record.settledAt), durationBetween(record.settledAt, record.fallbackAt),
			durationBetween(record.createdAt, record.completedAt), strconv.FormatBool(record.usedFallback),
		})
	}

	return rows
}

func (c *Collector) batchLatencies(id merkle.Hash) (time.Duration, time.Duration, time.Duration) {
	var settlement, fallback, endToEnd time.Duration
	var settlementCount, fallbackCount, completedCount int
	for _, record := range c.intents {
		if record.batchID != id {
			continue
		}
		if validInterval(record.reservedAt, record.settledAt) {
			settlement += record.settledAt.Sub(record.reservedAt)
			settlementCount++
		}
		if validInterval(record.settledAt, record.fallbackAt) {
			fallback += record.fallbackAt.Sub(record.settledAt)
			fallbackCount++
		}
		if validInterval(record.createdAt, record.completedAt) {
			endToEnd += record.completedAt.Sub(record.createdAt)
			completedCount++
		}
	}

	return average(settlement, settlementCount), average(fallback, fallbackCount), average(endToEnd, completedCount)
}

func (c *Collector) batchExecution(id merkle.Hash) (int64, int64, time.Duration) {
	var reads, writes int64
	var proofTime time.Duration
	for _, record := range c.intents {
		if record.batchID != id {
			continue
		}
		reads += record.reads
		writes += record.writes
		proofTime += record.proofTime
	}

	return reads, writes, proofTime
}

func decode(wrapped *rpcserver.WrappedMsg, target any) error {
	if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", wrapped.GetMsgType(), err)
	}

	return nil
}

func earlier(current, candidate time.Time) time.Time {
	if current.IsZero() || (!candidate.IsZero() && candidate.Before(current)) {
		return candidate
	}

	return current
}

func shardCounts(shards []model.ShardWindowMetric) string {
	parts := make([]string, len(shards))
	for idx, shard := range shards {
		parts[idx] = fmt.Sprintf("%d:%d", shard.ShardID, shard.BlockCount)
	}

	return strings.Join(parts, ";")
}

func cutHeights(shards []model.ShardWindowMetric) string {
	parts := make([]string, len(shards))
	for idx, shard := range shards {
		parts[idx] = fmt.Sprintf("%d:%d", shard.ShardID, shard.CutHeight)
	}

	return strings.Join(parts, ";")
}

func ratioInt(numerator, denominator int) string {
	if denominator == 0 {
		return "0"
	}

	return strconv.FormatFloat(float64(numerator)/float64(denominator), 'f', 6, 64)
}

func ratioBig(numerator, denominator string) string {
	left, leftOK := new(big.Int).SetString(numerator, 10)
	right, rightOK := new(big.Int).SetString(denominator, 10)
	if !leftOK || !rightOK || right.Sign() == 0 {
		return "0"
	}

	value, _ := new(big.Rat).SetFrac(left, right).Float64()
	return strconv.FormatFloat(value, 'f', 6, 64)
}

func timestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}

	return value.Format(time.RFC3339Nano)
}

func duration(value time.Duration) string {
	return strconv.FormatInt(value.Nanoseconds(), 10)
}

func durationBetween(start, end time.Time) string {
	if !validInterval(start, end) {
		return ""
	}

	return duration(end.Sub(start))
}

func validInterval(start, end time.Time) bool {
	return !start.IsZero() && !end.IsZero() && !end.Before(start)
}

func average(total time.Duration, count int) time.Duration {
	if count == 0 {
		return 0
	}

	return total / time.Duration(count)
}
