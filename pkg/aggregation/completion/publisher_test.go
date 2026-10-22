package completion

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
)

func TestAppliedChunksReportsCommittedSettlement(t *testing.T) {
	var batchID merkle.Hash
	batchID[0] = 1
	pack := model.AggregationPackage{
		Header: model.AggregationBatchHeader{BatchID: batchID, WindowID: 7},
		Aggregation: model.ShardAggregation{
			BatchID: batchID, WindowID: 7, ChunkIndex: 3,
			Credits: []model.AggregatedCredit{{ReceiptCount: 2}, {ReceiptCount: 4}},
		},
	}
	committed := &block.Block{Body: block.Body{TxList: []transaction.Transaction{
		*transaction.NewAggregationSettlementTransaction(pack, testTime()),
	}}}

	committedAt := testTime().Add(time.Second)
	chunks := appliedChunks(2, committed, committedAt)
	require.Len(t, chunks, 1)
	require.Equal(t, pack.Header.BatchID, chunks[0].BatchID)
	require.Equal(t, uint64(7), chunks[0].WindowID)
	require.Equal(t, int64(2), chunks[0].ShardID)
	require.Equal(t, uint32(3), chunks[0].ChunkIndex)
	require.Equal(t, 6, chunks[0].ReceiptCount)
	require.Equal(t, committedAt, chunks[0].CommittedAt)
}

func testTime() time.Time { return time.Unix(1, 0) }
