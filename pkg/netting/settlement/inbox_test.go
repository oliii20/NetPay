package settlement_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/settlement"
)

func TestInboxHandlesPackageAndRootInEitherOrderIdempotently(t *testing.T) {
	t.Parallel()

	confirmer := &fakeConfirmer{}
	pool := &fakePool{}
	inbox := settlement.NewInbox(1, confirmer, pool)
	header := model.MatchRootBlockBody{BatchID: inboxHash(1)}
	pack := model.SettlementPackage{
		Header: header,
		Settlement: model.ShardSettlement{
			BatchID: header.BatchID, ShardID: 1, ChunkIndex: 0, ChunkCount: 1,
		},
	}

	require.NoError(t, inbox.AddPackage(pack))
	require.Empty(t, pool.txs)
	require.NoError(t, inbox.Confirm(context.Background(), header))
	require.Len(t, pool.txs, 1)
	require.Equal(t, transaction.SettlementTxType, pool.txs[0].TxType())
	require.NoError(t, inbox.AddPackage(pack))
	require.Len(t, pool.txs, 1)
	require.Len(t, confirmer.headers, 1)

	wrong := pack.Clone()
	wrong.Settlement.ShardID = 2
	require.ErrorIs(t, inbox.AddPackage(wrong), settlement.ErrPackageWrongShard)
}

type fakeConfirmer struct {
	headers []model.MatchRootBlockBody
}

func (f *fakeConfirmer) ConfirmMatchRoot(_ context.Context, header model.MatchRootBlockBody) error {
	f.headers = append(f.headers, header)
	return nil
}

type fakePool struct {
	txs []transaction.Transaction
}

func (f *fakePool) AddTxs(txs []transaction.Transaction) error {
	f.txs = append(f.txs, txs...)
	return nil
}

func inboxHash(value byte) merkle.Hash {
	var hash merkle.Hash
	hash[0] = value

	return hash
}
