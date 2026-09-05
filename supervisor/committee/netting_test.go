package committee

import (
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
)

func TestNettingWorkloadConvertsCrossShardTransactionsAndTracksCompletion(t *testing.T) {
	t.Parallel()

	var shard0, shard1 account.Address
	shard1[len(shard1)-1] = 1
	workload := newNettingWorkload(true, 4, 11)
	txs := []transaction.Transaction{
		*transaction.NewTransaction(shard0, shard1, big.NewInt(5), big.NewInt(0), 99, time.Unix(1, 0)),
		*transaction.NewTransaction(shard0, shard1, big.NewInt(6), big.NewInt(0), 99, time.Unix(2, 0)),
		*transaction.NewTransaction(shard0, shard0, big.NewInt(7), big.NewInt(0), 99, time.Unix(3, 0)),
	}
	converted, err := workload.convert(txs)
	require.NoError(t, err)
	require.Len(t, converted, 3)
	require.NotNil(t, converted[0].Intent)
	require.Equal(t, uint64(0), converted[0].Intent.Nonce)
	require.Equal(t, uint64(1), converted[1].Intent.Nonce)
	require.Nil(t, converted[2].Intent)
	require.Equal(t, 2, workload.injectedCount())
	require.False(t, workload.finished())

	firstID, err := converted[0].Intent.ID()
	require.NoError(t, err)
	secondID, err := converted[1].Intent.ID()
	require.NoError(t, err)
	wrapped, err := message.WrapMsg(&message.NettingProgressMsg{NodeID: 0, IntentIDs: []intent.ID{firstID, secondID}})
	require.NoError(t, err)
	require.NoError(t, workload.handleProgress(wrapped))
	require.Equal(t, 2, workload.completedCount())
	require.True(t, workload.finished())
	require.NoError(t, workload.handleProgress(wrapped))
	require.Equal(t, 2, workload.completedCount())

	fallbackOnly := newNettingWorkload(true, 4, 11)
	converted, err = fallbackOnly.convert(txs[:1])
	require.NoError(t, err)
	fallbackID, err := converted[0].Intent.ID()
	require.NoError(t, err)
	fallbackWrapped, err := message.WrapMsg(&message.FallbackCompletedMsg{
		NodeID: 0,
		Key:    model.FallbackKey{IntentID: fallbackID},
	})
	require.NoError(t, err)
	require.NoError(t, fallbackOnly.handleFallbackCompleted(fallbackWrapped))
	require.True(t, fallbackOnly.finished())
	require.NoError(t, fallbackOnly.handleFallbackCompleted(fallbackWrapped))
	require.Equal(t, 1, fallbackOnly.completedCount())

	executionOnly := newNettingWorkload(true, 4, 11)
	converted, err = executionOnly.convert(txs[:1])
	require.NoError(t, err)
	executionID, err := converted[0].Intent.ID()
	require.NoError(t, err)
	executionWrapped, err := message.WrapMsg(&message.NettingExecutionMetricMsg{
		NodeID: 0,
		Metrics: []model.NettingExecutionMetric{{
			Phase:    model.MetricPhaseSettlement,
			IntentID: executionID,
			Final:    false,
		}},
	})
	require.NoError(t, err)
	require.NoError(t, executionOnly.handleExecutionMetric(executionWrapped))
	require.True(t, executionOnly.finished())
}
