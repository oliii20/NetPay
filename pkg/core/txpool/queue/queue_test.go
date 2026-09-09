package queue

import (
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/config"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
)

func TestPackTxsPrioritizesNettingLifecycleTransactions(t *testing.T) {
	t.Parallel()

	pool, err := NewTxPool(config.TxPoolCfg{Type: config.TxPoolNumType})
	require.NoError(t, err)

	normal1 := normalTx(1)
	settlement := *transaction.NewSettlementTransaction(model.SettlementPackage{
		Header: model.MatchRootBlockBody{BatchID: merkle.Hash{1}},
		Settlement: model.ShardSettlement{
			BatchID: merkle.Hash{1}, ShardID: 1, ChunkCount: 1,
		},
	}, time.Unix(1, 0))
	normal2 := normalTx(2)
	reserved := *transaction.NewReservedFallbackTransaction(model.ReservedFallback{
		IntentID: intent.ID{1}, BatchID: merkle.Hash{2}, Amount: big.NewInt(3),
	}, time.Unix(2, 0))
	completed := *transaction.NewFallbackCompletedTransaction(model.FallbackKey{
		IntentID: intent.ID{1}, BatchID: merkle.Hash{2},
	}, time.Unix(3, 0))
	normal3 := normalTx(3)

	require.NoError(t, pool.AddTxs([]transaction.Transaction{
		normal1, settlement, normal2, reserved, completed, normal3,
	}))

	packed, err := pool.PackTxs(3)
	require.NoError(t, err)
	require.Equal(t, []byte{
		transaction.SettlementTxType,
		transaction.ReservedFallbackTxType,
		transaction.FallbackCompletedTxType,
	}, txTypes(packed))

	packed, err = pool.PackTxs(10)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 2, 3}, txNonces(packed))
}

func normalTx(nonce uint64) transaction.Transaction {
	tx := transaction.NewTransaction(
		account.Address{}, account.Address{}, big.NewInt(1), big.NewInt(0), nonce, time.Unix(int64(nonce), 0),
	)

	return *tx
}

func txTypes(txs []transaction.Transaction) []byte {
	types := make([]byte, len(txs))
	for idx := range txs {
		types[idx] = txs[idx].TxType()
	}

	return types
}

func txNonces(txs []transaction.Transaction) []uint64 {
	nonces := make([]uint64, len(txs))
	for idx := range txs {
		nonces[idx] = txs[idx].Nonce
	}

	return nonces
}
