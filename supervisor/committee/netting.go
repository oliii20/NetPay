package committee

import (
	"bytes"
	"encoding/csv"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/config"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/partition"
)

type nettingWorkload struct {
	enabled              bool
	shardCount           int64
	chainID              uint64
	hotShards            map[int64]struct{}
	pairAllowlist        map[shardPair]struct{}
	maxNettingWindow     uint64
	maxWindowDuration    time.Duration
	maxInjectionDuration time.Duration
	injectionStartedAt   time.Time
	nettingStartedAt     time.Time
	finalizedWindowID    uint64
	now                  func() time.Time
	nonces               map[account.Address]uint64
	injected             map[intent.ID]struct{}
	completed            map[intent.ID]struct{}
}

type shardPair struct {
	left  int64
	right int64
}

func newNettingWorkload(cfg config.NettingCfg, shardCount, chainID int64) (*nettingWorkload, error) {
	hotShards := make(map[int64]struct{}, len(cfg.HotShards))
	for _, shardID := range cfg.HotShards {
		hotShards[shardID] = struct{}{}
	}

	pairAllowlist, err := loadNettingPairAllowlist(cfg)
	if err != nil {
		return nil, err
	}

	return &nettingWorkload{
		enabled:              cfg.Enabled,
		shardCount:           shardCount,
		chainID:              uint64(chainID),
		hotShards:            hotShards,
		pairAllowlist:        pairAllowlist,
		maxNettingWindow:     cfg.MaxNettingWindow,
		maxWindowDuration:    time.Duration(cfg.MaxWindowDurationMS) * time.Millisecond,
		maxInjectionDuration: time.Duration(cfg.MaxInjectionDurationMS) * time.Millisecond,
		now:                  time.Now,
		nonces:               make(map[account.Address]uint64),
		injected:             make(map[intent.ID]struct{}),
		completed:            make(map[intent.ID]struct{}),
	}, nil
}

func (n *nettingWorkload) convert(txs []transaction.Transaction) ([]transaction.Transaction, error) {
	if !n.enabled {
		return txs, nil
	}
	converted := make([]transaction.Transaction, 0, len(txs))
	now := n.now()
	if n.injectionStartedAt.IsZero() {
		n.injectionStartedAt = now
	}
	for _, tx := range txs {
		source := partition.DefaultAccountLoc(tx.Sender, n.shardCount)
		destination := partition.DefaultAccountLoc(tx.Recipient, n.shardCount)
		if source == destination || tx.TxType() != transaction.NormalTxType ||
			!n.isNettingShardPair(source, destination) || !n.isNettingWindowOpen(now) ||
			!n.isNettingInjectionOpen(now) {
			converted = append(converted, tx)
			continue
		}
		if n.nettingStartedAt.IsZero() {
			n.nettingStartedAt = now
		}
		payment := intent.PaymentIntent{
			Version: intent.CurrentVersion, ChainID: n.chainID,
			Sender: tx.Sender, Recipient: tx.Recipient,
			SourceShard: source, DestinationShard: destination,
			AssetID: intent.NativeAssetID, Amount: tx.Value,
			Nonce: n.nonces[tx.Sender], ExpiryEpoch: math.MaxUint64,
		}
		id, err := payment.ID()
		if err != nil {
			return nil, fmt.Errorf("build payment intent: %w", err)
		}
		n.nonces[tx.Sender]++
		n.injected[id] = struct{}{}
		converted = append(converted, *transaction.NewIntentTransaction(payment, tx.CreateTime))
	}

	return converted, nil
}

func (n *nettingWorkload) isNettingShardPair(source, destination int64) bool {
	if len(n.hotShards) == 0 {
		return n.isAllowedPair(source, destination)
	}

	_, sourceHot := n.hotShards[source]
	_, destinationHot := n.hotShards[destination]

	if !(sourceHot && destinationHot) {
		return false
	}

	return n.isAllowedPair(source, destination)
}

func (n *nettingWorkload) isAllowedPair(source, destination int64) bool {
	if n.pairAllowlist == nil {
		return true
	}
	_, exists := n.pairAllowlist[newShardPair(source, destination)]

	return exists
}

func (n *nettingWorkload) isNettingWindowOpen(now time.Time) bool {
	if n.maxNettingWindow == 0 {
		return true
	}
	if n.finalizedWindowID >= n.maxNettingWindow {
		return false
	}
	if n.maxWindowDuration <= 0 || n.nettingStartedAt.IsZero() {
		return true
	}
	elapsed := now.Sub(n.nettingStartedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	windowID := uint64(elapsed/n.maxWindowDuration) + 1

	return windowID <= n.maxNettingWindow
}

func (n *nettingWorkload) isNettingInjectionOpen(now time.Time) bool {
	if n.maxInjectionDuration <= 0 || n.injectionStartedAt.IsZero() {
		return true
	}
	elapsed := now.Sub(n.injectionStartedAt)
	if elapsed < 0 {
		elapsed = 0
	}

	return elapsed < n.maxInjectionDuration
}

func newShardPair(left, right int64) shardPair {
	if left <= right {
		return shardPair{left: left, right: right}
	}

	return shardPair{left: right, right: left}
}

func loadNettingPairAllowlist(cfg config.NettingCfg) (map[shardPair]struct{}, error) {
	if cfg.PairRateFile == "" {
		return nil, nil
	}
	file, err := os.Open(cfg.PairRateFile)
	if err != nil {
		return nil, fmt.Errorf("open netting pair rate file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read netting pair rate header: %w", err)
	}
	pairCol, rateCol := -1, -1
	for idx, column := range header {
		switch normalizeCSVHeader(column) {
		case "pair":
			pairCol = idx
		case "matched_intent_rate", "matched_intentrate":
			rateCol = idx
		}
	}
	if pairCol < 0 || rateCol < 0 {
		return nil, fmt.Errorf("netting pair rate file must include pair and matched_intent_rate columns")
	}

	allowlist := make(map[shardPair]struct{})
	for {
		row, readErr := reader.Read()
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return nil, fmt.Errorf("read netting pair rate row: %w", readErr)
		}
		if pairCol >= len(row) || rateCol >= len(row) {
			return nil, fmt.Errorf("netting pair rate row has too few columns: %v", row)
		}
		rate, parseErr := strconv.ParseFloat(strings.TrimSpace(row[rateCol]), 64)
		if parseErr != nil {
			return nil, fmt.Errorf("parse matched intent rate %q: %w", row[rateCol], parseErr)
		}
		if rate < cfg.PairRateThreshold {
			continue
		}
		pair, parseErr := parseShardPair(row[pairCol])
		if parseErr != nil {
			return nil, parseErr
		}
		allowlist[pair] = struct{}{}
	}

	return allowlist, nil
}

func normalizeCSVHeader(column string) string {
	column = strings.TrimSpace(strings.ToLower(column))
	column = strings.ReplaceAll(column, " ", "_")

	return column
}

func parseShardPair(value string) (shardPair, error) {
	cleaned := strings.TrimSpace(value)
	cleaned = strings.TrimPrefix(cleaned, "s")
	cleaned = strings.ReplaceAll(cleaned, "_s", "-")
	cleaned = strings.ReplaceAll(cleaned, "->", "-")
	parts := strings.Split(cleaned, "-")
	if len(parts) != 2 {
		return shardPair{}, fmt.Errorf("parse netting shard pair %q", value)
	}
	left, err := strconv.ParseInt(strings.TrimPrefix(parts[0], "s"), 10, 64)
	if err != nil {
		return shardPair{}, fmt.Errorf("parse netting shard pair %q: %w", value, err)
	}
	right, err := strconv.ParseInt(strings.TrimPrefix(parts[1], "s"), 10, 64)
	if err != nil {
		return shardPair{}, fmt.Errorf("parse netting shard pair %q: %w", value, err)
	}

	return newShardPair(left, right), nil
}

func (n *nettingWorkload) handleBatchMetric(wrapped *rpcserver.WrappedMsg) error {
	if !n.enabled || wrapped.GetMsgType() != message.NettingBatchMetricMessageType {
		return nil
	}
	var msg message.NettingBatchMetricMsg
	if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(&msg); err != nil {
		return fmt.Errorf("decode netting batch metric: %w", err)
	}
	if msg.Metric.WindowID > n.finalizedWindowID {
		n.finalizedWindowID = msg.Metric.WindowID
	}

	return nil
}

func (n *nettingWorkload) handleProgress(wrapped *rpcserver.WrappedMsg) error {
	if !n.enabled || wrapped.GetMsgType() != message.NettingProgressMessageType {
		return nil
	}
	var progress message.NettingProgressMsg
	if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(&progress); err != nil {
		return fmt.Errorf("decode netting progress: %w", err)
	}
	if progress.NodeID != 0 {
		return fmt.Errorf("netting progress is not from a shard leader: %d", progress.NodeID)
	}
	for _, id := range progress.IntentIDs {
		if _, exists := n.injected[id]; exists {
			n.completed[id] = struct{}{}
		}
	}

	return nil
}

func (n *nettingWorkload) handleFallbackCompleted(wrapped *rpcserver.WrappedMsg) error {
	if !n.enabled || wrapped.GetMsgType() != message.FallbackCompletedMessageType {
		return nil
	}
	var completed message.FallbackCompletedMsg
	if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(&completed); err != nil {
		return fmt.Errorf("decode fallback completed: %w", err)
	}
	if completed.NodeID != 0 {
		return fmt.Errorf("fallback completion is not from a shard leader: %d", completed.NodeID)
	}
	if _, exists := n.injected[completed.Key.IntentID]; exists {
		n.completed[completed.Key.IntentID] = struct{}{}
	}

	return nil
}

func (n *nettingWorkload) handleExecutionMetric(wrapped *rpcserver.WrappedMsg) error {
	if !n.enabled || wrapped.GetMsgType() != message.NettingExecutionMetricMessageType {
		return nil
	}
	var msg message.NettingExecutionMetricMsg
	if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(&msg); err != nil {
		return fmt.Errorf("decode netting execution metric: %w", err)
	}
	if msg.NodeID != 0 {
		return fmt.Errorf("netting execution metric is not from a shard leader: %d", msg.NodeID)
	}
	for _, metric := range msg.Metrics {
		switch metric.Phase {
		case model.MetricPhaseSettlement:
			if !metric.Final {
				continue
			}
			if _, exists := n.injected[metric.IntentID]; exists {
				n.completed[metric.IntentID] = struct{}{}
			}
		case model.MetricPhaseFallback:
			if _, exists := n.injected[metric.IntentID]; exists {
				n.completed[metric.IntentID] = struct{}{}
			}
		}
	}

	return nil
}

func (n *nettingWorkload) finished() bool {
	return !n.enabled || len(n.completed) == len(n.injected)
}

func (n *nettingWorkload) injectedCount() int {
	return len(n.injected)
}

func (n *nettingWorkload) completedCount() int {
	return len(n.completed)
}
