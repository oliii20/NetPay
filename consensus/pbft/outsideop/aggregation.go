package outsideop

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/routing"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/settlement"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

type AggregationOutsideOp struct {
	base   ShardOutsideMsgHandler
	inbox  *settlement.Inbox
	routes *routing.Table
}

func NewAggregationOutsideOp(
	base ShardOutsideMsgHandler,
	inbox *settlement.Inbox,
) *AggregationOutsideOp {
	return &AggregationOutsideOp{base: base, inbox: inbox}
}

func (n *AggregationOutsideOp) SetRoutingTable(table *routing.Table) { n.routes = table }

func (n *AggregationOutsideOp) HandleMsgOutsideShard(ctx context.Context, wrapped *rpcserver.WrappedMsg) error {
	switch wrapped.GetMsgType() {
	case message.AggregationRoutingPolicyMessageType:
		if n.routes == nil {
			return fmt.Errorf("adaptive routing is disabled")
		}

		var msg message.AggregationRoutingPolicyMsg
		if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(&msg); err != nil {
			return fmt.Errorf("decode aggregation routing policy: %w", err)
		}

		if msg.NodeID != 0 || msg.ShardID != nodetopo.BeaconShardID {
			return fmt.Errorf("routing policy is not from Beacon leader")
		}

		return n.routes.Apply(msg.Policy)
	case message.AggregationFinalizedMessageType:
		var msg message.AggregationFinalizedMsg
		if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(&msg); err != nil {
			return fmt.Errorf("decode finalized aggregation: %w", err)
		}

		if msg.NodeID != 0 {
			return fmt.Errorf("finalized aggregation is not from Beacon leader: node %d", msg.NodeID)
		}

		return n.inbox.Confirm(ctx, msg.Header)
	case message.AggregationPackageMessageType:
		var msg message.AggregationPackageMsg
		if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(&msg); err != nil {
			return fmt.Errorf("decode aggregation package: %w", err)
		}

		if msg.NodeID != 0 {
			return fmt.Errorf("aggregation package is not from Beacon leader: node %d", msg.NodeID)
		}

		return n.inbox.AddPackage(msg.Package)
	default:
		return n.base.HandleMsgOutsideShard(ctx, wrapped)
	}
}
