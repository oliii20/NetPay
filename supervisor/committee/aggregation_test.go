package committee

import (
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/config"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/routing"
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

func TestStaticDestinationRoutesMixedPayments(t *testing.T) {
	// Addresses map by their final four bytes, as in the real CSV workload.
	var sender, hot, cold [20]byte
	sender[19], hot[19], cold[19] = 4, 1, 2
	w, err := newAggregationWorkload(config.AggregationCfg{Enabled: true, DestinationShards: []int64{1}}, 4, 11)
	require.NoError(t, err)
	txs := []transaction.Transaction{
		*transaction.NewTransaction(sender, hot, big.NewInt(2), big.NewInt(0), 1, time.Now()),
		*transaction.NewTransaction(sender, cold, big.NewInt(3), big.NewInt(0), 2, time.Now()),
		*transaction.NewTransaction(sender, sender, big.NewInt(4), big.NewInt(0), 3, time.Now()),
		*transaction.NewTransaction(sender, hot, big.NewInt(5), big.NewInt(0), 4, time.Now()),
	}
	got, err := w.convert(txs)
	require.NoError(t, err)
	require.Equal(t, transaction.CrossShardPaymentTxType, got[0].TxType())
	require.Equal(t, transaction.NormalTxType, got[1].TxType())
	require.Equal(t, transaction.NormalTxType, got[2].TxType())
	require.Equal(t, uint64(1), got[3].Payment.Nonce, "relay transactions must not consume outbox nonces")
	require.Equal(t, txs[1], got[1], "cold relay transaction must remain unchanged")
	require.Len(t, w.injected, 2)
	// Empty allowlist routes everything through Relay; nil keeps existing Full.
	empty, err := newAggregationWorkload(config.AggregationCfg{Enabled: true, DestinationShards: []int64{}}, 4, 11)
	require.NoError(t, err)
	unchanged, err := empty.convert(txs)
	require.NoError(t, err)
	require.Equal(t, txs, unchanged)
	full, err := newAggregationWorkload(config.AggregationCfg{Enabled: true}, 4, 11)
	require.NoError(t, err)
	all, err := full.convert(txs)
	require.NoError(t, err)
	require.Equal(t, transaction.CrossShardPaymentTxType, all[1].TxType())
	for _, ids := range [][]int64{{-1}, {4}, {1, 1}} {
		_, err = newAggregationWorkload(config.AggregationCfg{Enabled: true, DestinationShards: ids}, 4, 11)
		require.Error(t, err)
	}
}

func TestAdaptiveMixedCompletionDoesNotStopEarly(t *testing.T) {
	var sender, first, second [20]byte
	sender[19], first[19], second[19] = 4, 1, 2
	w, err := newAggregationWorkload(config.AggregationCfg{Enabled: true, AdaptiveRouting: routing.Config{Enabled: true}}, 4, 11)
	require.NoError(t, err)
	txs := []transaction.Transaction{
		*transaction.NewTransaction(sender, first, big.NewInt(2), big.NewInt(0), 1, time.Now()),
		*transaction.NewTransaction(sender, second, big.NewInt(3), big.NewInt(0), 2, time.Now()),
	}
	converted, err := w.convert(txs)
	require.NoError(t, err)
	require.Equal(t, txs, converted, "no upfront conversion or route marking")
	var ids [2]paymentmodel.ID
	for i := range txs {
		h, hashErr := txs[i].Hash()
		require.NoError(t, hashErr)
		copy(ids[i][:], h)
	}
	require.False(t, w.finished())
	r2 := txs[0]
	r2.RelayStage, r2.ROriginalHash = transaction.Relay2Tx, ids[0][:]
	w.handleRelayed([]transaction.Transaction{r2, r2})
	require.False(t, w.finished(), "must still wait for aggregation settlement")
	batchID := merkle.Hash{1}
	applied, err := message.WrapMsg(&message.AggregationAppliedMsg{Chunks: []message.AggregationApplied{
		{BatchID: batchID, ShardID: 2, ChunkIndex: 0, ReceiptCount: 1},
	}})
	require.NoError(t, err)
	require.NoError(t, w.handleApplied(applied))
	require.False(t, w.finished(), "applied-before-finalized must reconcile later")
	finalized, err := message.WrapMsg(&message.AggregationFinalizedMsg{Header: model.AggregationBatchHeader{BatchID: batchID}, PaymentIDs: []paymentmodel.ID{ids[1]}})
	require.NoError(t, err)
	require.NoError(t, w.handleFinalized(finalized))
	require.NoError(t, w.handleFinalized(finalized))
	require.True(t, w.finished())
	require.Len(t, w.completed, 2)
}
