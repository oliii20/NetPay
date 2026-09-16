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

func TestPackTxsCapsHeavyNettingLifecycleTransactions(t *testing.T) {
	t.Parallel()

	pool, err := NewTxPool(config.TxPoolCfg{Type: config.TxPoolNumType})
	require.NoError(t, err)

	txs := make([]transaction.Transaction, 0, maxHeavyNettingTxsPerNumBlock+5)
	for idx := 0; idx < maxHeavyNettingTxsPerNumBlock+2; idx++ {
		txs = append(txs, settlementTx(byte(idx)))
	}
	txs = append(txs, normalTx(100), normalTx(101), normalTx(102))
	require.NoError(t, pool.AddTxs(txs))

	packed, err := pool.PackTxs(maxHeavyNettingTxsPerNumBlock + 3)
	require.NoError(t, err)
	require.Len(t, packed, maxHeavyNettingTxsPerNumBlock+3)
	require.Equal(t, maxHeavyNettingTxsPerNumBlock, countTxType(packed, transaction.SettlementTxType))
	require.Equal(t, []uint64{100, 101, 102}, txNonces(normalTxs(packed)))

	packed, err = pool.PackTxs(10)
	require.NoError(t, err)
	require.Len(t, packed, 2)
	require.Equal(t, 2, countTxType(packed, transaction.SettlementTxType))
}

func settlementTx(id byte) transaction.Transaction {
	return *transaction.NewSettlementTransaction(model.SettlementPackage{
		Header: model.MatchRootBlockBody{BatchID: merkle.Hash{id}},
		Settlement: model.ShardSettlement{
			BatchID: merkle.Hash{id}, ShardID: 1, ChunkCount: 1,
		},
	}, time.Unix(int64(id), 0))
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

func countTxType(txs []transaction.Transaction, txType byte) int {
	count := 0
	for idx := range txs {
		if txs[idx].TxType() == txType {
			count++
		}
	}

	return count
}

func normalTxs(txs []transaction.Transaction) []transaction.Transaction {
	filtered := make([]transaction.Transaction, 0)
	for idx := range txs {
		if txs[idx].TxType() == transaction.NormalTxType {
			filtered = append(filtered, txs[idx])
		}
	}

	return filtered
}

func txNonces(txs []transaction.Transaction) []uint64 {
	nonces := make([]uint64, len(txs))
	for idx := range txs {
		nonces[idx] = txs[idx].Nonce
	}

	return nonces
}
