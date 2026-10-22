package committee

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/config"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
)

func TestAggregationAppliedCompletesBatchOnce(t *testing.T) {
	workload, err := newAggregationWorkload(config.AggregationCfg{Enabled: true}, 2, 1)
	require.NoError(t, err)
	var batchID merkle.Hash
	batchID[0] = 1
	var first, second paymentmodel.ID
	first[0], second[0] = 1, 2
	workload.injected[first] = struct{}{}
	workload.injected[second] = struct{}{}
	workload.batchPayments[batchID] = []paymentmodel.ID{first, second}

	applied, err := message.WrapMsg(&message.AggregationAppliedMsg{
		NodeID: 0,
		Chunks: []message.AggregationApplied{
			{BatchID: batchID, ShardID: 1, ChunkIndex: 0, ReceiptCount: 1},
		},
	})
	require.NoError(t, err)
	require.NoError(t, workload.handleApplied(applied))
	require.NoError(t, workload.handleApplied(applied), "duplicate delivery must be idempotent")
	require.False(t, workload.finished())
	require.Equal(t, 1, workload.batchSettledReceipts[batchID])

	secondChunk, err := message.WrapMsg(&message.AggregationAppliedMsg{
		NodeID: 0,
		Chunks: []message.AggregationApplied{
			{BatchID: batchID, ShardID: 1, ChunkIndex: 1, ReceiptCount: 1},
		},
	})
	require.NoError(t, err)
	require.NoError(t, workload.handleApplied(secondChunk))
	require.True(t, workload.finished())
}
