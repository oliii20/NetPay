package beaconop

import (
	"errors"
	"math/big"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/config"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/beacon"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/window"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
)

func TestCompactProposalReconstructsLocallyAndRejectsUnavailableOrTamperedData(t *testing.T) {
	store, err := beacon.OpenStore(filepath.Join(t.TempDir(), "beacon.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	op := New(nil, nil, store, 2, 11, 1, config.AggregationCfg{CreditChunkSize: 512})
	payment := compactTestPayment()
	receipt := model.FinalizedBlockReceipt{
		ShardID: 0, Height: 1, ParentHash: compactTestHash(1), BlockHash: compactTestHash(2),
		Epoch: 1, Payments: []paymentmodel.CrossShardPayment{payment},
	}
	frozen := window.FrozenWindow{
		WindowID: 1,
		Cuts: []model.ShardCut{
			{ShardID: 0, EndHeight: 1, EndBlockHash: receipt.BlockHash},
			{ShardID: 1},
		},
		Receipts: []model.FinalizedBlockReceipt{receipt},
		Payments: []paymentmodel.CrossShardPayment{payment},
	}
	want, err := op.builder.Build(frozen, merkle.Hash{})
	require.NoError(t, err)
	require.NoError(t, op.validator.ValidateReceipt(receipt))
	require.NoError(t, op.recordReceipt(receipt))

	compact := message.WrapAggregationHeaderProposal(want.Header)
	require.NoError(t, op.ValidateProposal(t.Context(), compact))
	require.Equal(t, want, op.prepared[want.Header.BatchID])

	fullSize, err := encodedSize(message.WrapAggregationProposal(want))
	require.NoError(t, err)
	compactSize, err := encodedSize(compact)
	require.NoError(t, err)
	require.Less(t, compactSize, fullSize)

	delete(op.prepared, want.Header.BatchID)
	delete(op.receipts[receipt.ShardID], receipt.Height)
	err = op.ValidateProposal(t.Context(), compact)
	require.True(t, errors.Is(err, ErrMissingReceiptData), "unexpected error: %v", err)

	require.NoError(t, op.recordReceipt(receipt))
	tampered := want.Header
	tampered.AggregationRoot[0] ^= 1
	err = op.ValidateProposal(t.Context(), message.WrapAggregationHeaderProposal(tampered))
	require.True(t, errors.Is(err, ErrAggregationHeader), "unexpected error: %v", err)
}

func TestRecordReceiptRejectsConflictingContent(t *testing.T) {
	op := &Op{receipts: make(map[int64]map[uint64]model.FinalizedBlockReceipt)}
	receipt := model.FinalizedBlockReceipt{ShardID: 0, Height: 1, BlockHash: compactTestHash(2)}
	require.NoError(t, op.recordReceipt(receipt))
	require.NoError(t, op.recordReceipt(receipt.Clone()))

	conflict := receipt.Clone()
	conflict.StateRoot[0] = 1
	require.ErrorIs(t, op.recordReceipt(conflict), ErrConflictingReceipt)
}

func compactTestPayment() paymentmodel.CrossShardPayment {
	return paymentmodel.CrossShardPayment{
		Version: paymentmodel.CurrentVersion, ChainID: 11,
		Sender: account.Address{1}, Recipient: account.Address{2},
		SourceShard: 0, DestinationShard: 1, Amount: big.NewInt(7), ExpiryEpoch: 100,
	}
}

func compactTestHash(value byte) merkle.Hash {
	var result merkle.Hash
	result[0] = value
	return result
}

func TestCompactProposalDoesNotCarrySidecar(t *testing.T) {
	proposal := message.WrapAggregationHeaderProposal(model.AggregationBatchHeader{WindowID: 3})
	require.NotNil(t, proposal.AggregationHeader)
	require.Nil(t, proposal.AggregationBatch)
	require.Nil(t, proposal.Block)
}
