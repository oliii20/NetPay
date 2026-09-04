package outsideop

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/settlement"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
)

type NettingOutsideOp struct {
	base  ShardOutsideMsgHandler
	inbox *settlement.Inbox
}

func NewNettingOutsideOp(base ShardOutsideMsgHandler, inbox *settlement.Inbox) *NettingOutsideOp {
	return &NettingOutsideOp{base: base, inbox: inbox}
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
	default:
		return n.base.HandleMsgOutsideShard(ctx, wrapped)
	}
}
