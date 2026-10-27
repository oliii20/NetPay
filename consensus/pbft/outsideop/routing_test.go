package outsideop_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/consensus/pbft/outsideop"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/routing"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

func TestRoutingBroadcastUpdatesSharedDeliveryTable(t *testing.T) {
	table := routing.NewTable(4)
	handler := outsideop.NewAggregationOutsideOp(nil, nil)
	handler.SetRoutingTable(table)
	msg := &message.AggregationRoutingPolicyMsg{NodeID: 0, ShardID: nodetopo.BeaconShardID,
		Policy: routing.Policy{Version: 1, Destinations: []int64{1, 2, 3}}}
	wrapped, err := message.WrapMsg(msg)
	require.NoError(t, err)
	require.NoError(t, handler.HandleMsgOutsideShard(context.Background(), wrapped))
	require.False(t, table.Snapshot().Aggregates(0))
	require.True(t, table.Snapshot().Aggregates(1))
	msg.NodeID = 1
	wrapped, err = message.WrapMsg(msg)
	require.NoError(t, err)
	require.Error(t, handler.HandleMsgOutsideShard(context.Background(), wrapped))
	msg.NodeID, msg.ShardID = 0, 0
	wrapped, err = message.WrapMsg(msg)
	require.NoError(t, err)
	require.Error(t, handler.HandleMsgOutsideShard(context.Background(), wrapped))
}
