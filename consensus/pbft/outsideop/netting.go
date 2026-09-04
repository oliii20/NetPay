package outsideop

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/settlement"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
)

type NettingOutsideOp struct {
	base   ShardOutsideMsgHandler
	inbox  *settlement.Inbox
	txPool settlement.TransactionAdder
}

func NewNettingOutsideOp(
	base ShardOutsideMsgHandler,
	inbox *settlement.Inbox,
	txPool settlement.TransactionAdder,
) *NettingOutsideOp {
	return &NettingOutsideOp{base: base, inbox: inbox, txPool: txPool}
}

func (n *NettingOutsideOp) HandleMsgOutsideShard(ctx context.Context, wrapped *rpcserver.WrappedMsg) error {
	switch wrapped.GetMsgType() {
	case message.MatchRootFinalizedMessageType:
		var msg message.MatchRootFinalizedMsg
		if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(&msg); err != nil {
			return fmt.Errorf("decode finalized MatchRoot: %w", err)
		}
		if msg.NodeID != 0 {
			return fmt.Errorf("finalized MatchRoot is not from Beacon leader: node %d", msg.NodeID)
		}
		return n.inbox.Confirm(ctx, msg.Header)
	case message.SettlementPackageMessageType:
		var msg message.SettlementPackageMsg
		if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(&msg); err != nil {
			return fmt.Errorf("decode settlement package: %w", err)
		}
		if msg.NodeID != 0 {
			return fmt.Errorf("settlement package is not from Solver: node %d", msg.NodeID)
		}
		return n.inbox.AddPackage(msg.Package)
	case message.FallbackTxMessageType:
		var msg message.FallbackTxMsg
		if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(&msg); err != nil {
			return fmt.Errorf("decode reserved fallback: %w", err)
		}
		if msg.NodeID != 0 {
			return fmt.Errorf("fallback is not from source leader: node %d", msg.NodeID)
		}
		tx := transaction.NewReservedFallbackTransaction(msg.Fallback, time.Now())
		return n.txPool.AddTxs([]transaction.Transaction{*tx})
	case message.FallbackCompletedMessageType:
		var msg message.FallbackCompletedMsg
		if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(&msg); err != nil {
			return fmt.Errorf("decode fallback completion: %w", err)
		}
		if msg.NodeID != 0 {
			return fmt.Errorf("fallback completion is not from destination leader: node %d", msg.NodeID)
		}
		tx := transaction.NewFallbackCompletedTransaction(msg.Key, time.Now())
		return n.txPool.AddTxs([]transaction.Transaction{*tx})
	default:
		return n.base.HandleMsgOutsideShard(ctx, wrapped)
	}
}
