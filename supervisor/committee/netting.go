package committee

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"math"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/partition"
)

type nettingWorkload struct {
	enabled    bool
	shardCount int64
	chainID    uint64
	nonces     map[account.Address]uint64
	injected   map[intent.ID]struct{}
	completed  map[intent.ID]struct{}
}

func newNettingWorkload(enabled bool, shardCount, chainID int64) *nettingWorkload {
	return &nettingWorkload{
		enabled: enabled, shardCount: shardCount, chainID: uint64(chainID),
		nonces:   make(map[account.Address]uint64),
		injected: make(map[intent.ID]struct{}), completed: make(map[intent.ID]struct{}),
	}
}

func (n *nettingWorkload) convert(txs []transaction.Transaction) ([]transaction.Transaction, error) {
	if !n.enabled {
		return txs, nil
	}
	converted := make([]transaction.Transaction, 0, len(txs))
	for _, tx := range txs {
		source := partition.DefaultAccountLoc(tx.Sender, n.shardCount)
		destination := partition.DefaultAccountLoc(tx.Recipient, n.shardCount)
		if source == destination || tx.TxType() != transaction.NormalTxType {
			converted = append(converted, tx)
			continue
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
		case model.MetricPhaseSettlement, model.MetricPhaseFallback:
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
