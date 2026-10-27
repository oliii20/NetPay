package committee

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"math"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/config"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/partition"
)

type aggregationWorkload struct {
	destinationShards    map[int64]bool
	enabled              bool
	shardCount           int64
	chainID              uint64
	maxAggregationWindow uint64
	maxWindowDuration    time.Duration
	maxInjectionDuration time.Duration
	injectionStartedAt   time.Time
	aggregationStartedAt time.Time
	finalizedWindowID    uint64
	now                  func() time.Time
	nonces               map[account.Address]uint64
	injected             map[paymentmodel.ID]struct{}
	completed            map[paymentmodel.ID]struct{}
	batchPayments        map[merkle.Hash][]paymentmodel.ID
	batchSettledReceipts map[merkle.Hash]int
	appliedChunks        map[aggregationChunkKey]struct{}
}

type aggregationChunkKey struct {
	batchID    merkle.Hash
	shardID    int64
	chunkIndex uint32
}

func newAggregationWorkload(cfg config.AggregationCfg, shardCount, chainID int64) (*aggregationWorkload, error) {
	if shardCount <= 0 {
		return nil, fmt.Errorf("aggregation shard count must be positive: %d", shardCount)
	}

	var destinations map[int64]bool
	if cfg.DestinationShards != nil {
		if !cfg.Enabled {
			return nil, fmt.Errorf("destination allowlist requires aggregation")
		}
		destinations = make(map[int64]bool)
		for _, id := range cfg.DestinationShards {
			if id < 0 || id >= shardCount || destinations[id] {
				return nil, fmt.Errorf("invalid or duplicate destination shard: %d", id)
			}
			destinations[id] = true
		}
	}
	return &aggregationWorkload{
		destinationShards:    destinations,
		enabled:              cfg.Enabled,
		shardCount:           shardCount,
		chainID:              uint64(chainID),
		maxAggregationWindow: cfg.MaxAggregationWindow,
		maxWindowDuration:    time.Duration(cfg.MaxWindowDurationMS) * time.Millisecond,
		maxInjectionDuration: time.Duration(cfg.MaxInjectionDurationMS) * time.Millisecond,
		now:                  time.Now,
		nonces:               make(map[account.Address]uint64),
		injected:             make(map[paymentmodel.ID]struct{}),
		completed:            make(map[paymentmodel.ID]struct{}),
		batchPayments:        make(map[merkle.Hash][]paymentmodel.ID),
		batchSettledReceipts: make(map[merkle.Hash]int),
		appliedChunks:        make(map[aggregationChunkKey]struct{}),
	}, nil
}

func (n *aggregationWorkload) convert(txs []transaction.Transaction) ([]transaction.Transaction, error) {
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
		if (n.destinationShards != nil && !n.destinationShards[destination]) || source == destination || tx.TxType() != transaction.NormalTxType ||
			!n.isAggregationWindowOpen(now) || !n.isAggregationInjectionOpen(now) {
			converted = append(converted, tx)
			continue
		}

		if n.aggregationStartedAt.IsZero() {
			n.aggregationStartedAt = now
		}

		payment := paymentmodel.CrossShardPayment{
			Version: paymentmodel.CurrentVersion, ChainID: n.chainID,
			Sender: tx.Sender, Recipient: tx.Recipient,
			SourceShard: source, DestinationShard: destination,
			AssetID: paymentmodel.NativeAssetID, Amount: tx.Value,
			Nonce: n.nonces[tx.Sender], ExpiryEpoch: math.MaxUint64,
		}

		id, err := payment.ID()
		if err != nil {
			return nil, fmt.Errorf("build cross-shard payment: %w", err)
		}

		n.nonces[tx.Sender]++
		n.injected[id] = struct{}{}

		converted = append(converted, *transaction.NewCrossShardPaymentTransaction(payment, tx.CreateTime))
	}

	return converted, nil
}

func (n *aggregationWorkload) isAggregationWindowOpen(now time.Time) bool {
	if n.maxAggregationWindow == 0 {
		return true
	}

	if n.finalizedWindowID >= n.maxAggregationWindow {
		return false
	}

	if n.maxWindowDuration <= 0 || n.aggregationStartedAt.IsZero() {
		return true
	}

	elapsed := max(now.Sub(n.aggregationStartedAt), 0)
	windowID := uint64(elapsed/n.maxWindowDuration) + 1

	return windowID <= n.maxAggregationWindow
}

func (n *aggregationWorkload) isAggregationInjectionOpen(now time.Time) bool {
	if n.maxInjectionDuration <= 0 || n.injectionStartedAt.IsZero() {
		return true
	}

	return max(now.Sub(n.injectionStartedAt), 0) < n.maxInjectionDuration
}

func (n *aggregationWorkload) handleFinalized(wrapped *rpcserver.WrappedMsg) error {
	if !n.enabled || wrapped.GetMsgType() != message.AggregationFinalizedMessageType {
		return nil
	}

	var finalized message.AggregationFinalizedMsg
	if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(&finalized); err != nil {
		return fmt.Errorf("decode aggregation finalized: %w", err)
	}

	if finalized.NodeID != 0 {
		return fmt.Errorf("aggregation finalization is not from Beacon leader: %d", finalized.NodeID)
	}

	if finalized.Header.WindowID > n.finalizedWindowID {
		n.finalizedWindowID = finalized.Header.WindowID
	}

	ids := make([]paymentmodel.ID, 0, len(finalized.PaymentIDs))
	for _, id := range finalized.PaymentIDs {
		if _, exists := n.injected[id]; exists {
			ids = append(ids, id)
		}
	}

	n.batchPayments[finalized.Header.BatchID] = ids
	n.completeSettledBatch(finalized.Header.BatchID)

	return nil
}

func (n *aggregationWorkload) handleApplied(wrapped *rpcserver.WrappedMsg) error {
	if !n.enabled || wrapped.GetMsgType() != message.AggregationAppliedMessageType {
		return nil
	}

	var msg message.AggregationAppliedMsg
	if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(&msg); err != nil {
		return fmt.Errorf("decode aggregation completion: %w", err)
	}

	if msg.NodeID != 0 {
		return fmt.Errorf("aggregation completion is not from a shard leader: %d", msg.NodeID)
	}

	for _, chunk := range msg.Chunks {
		key := aggregationChunkKey{batchID: chunk.BatchID, shardID: chunk.ShardID, chunkIndex: chunk.ChunkIndex}
		if _, exists := n.appliedChunks[key]; exists {
			continue
		}

		n.appliedChunks[key] = struct{}{}
		n.batchSettledReceipts[chunk.BatchID] += chunk.ReceiptCount
		n.completeSettledBatch(chunk.BatchID)
	}

	return nil
}

func (n *aggregationWorkload) completeSettledBatch(batchID merkle.Hash) {
	ids, known := n.batchPayments[batchID]
	if !known || n.batchSettledReceipts[batchID] < len(ids) {
		return
	}

	for _, id := range ids {
		n.completed[id] = struct{}{}
	}
}

func (n *aggregationWorkload) finished() bool {
	return !n.enabled || len(n.completed) == len(n.injected)
}
