package batch_test

import (
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/receipt"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/window"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
)

func TestBuilderAggregatesBeforeChunking(t *testing.T) {
	t.Parallel()

	recipientA := address(0xa1)
	recipientB := address(0xb1)
	payments := []paymentmodel.CrossShardPayment{
		payment(address(1), recipientA, 3, 0),
		payment(address(2), recipientA, 5, 0),
		payment(address(3), recipientA, 7, 0),
		payment(address(4), recipientB, 11, 0),
	}
	receipt := model.FinalizedBlockReceipt{
		ShardID: 0, Height: 2, ParentHash: hash(0x10), BlockHash: hash(0x11),
		Epoch: 1, Payments: payments,
	}
	frozen := window.FrozenWindow{
		WindowID: 1, OpenedAt: time.Unix(1, 0), SealedAt: time.Unix(2, 0),
		CloseReason: "batch_size",
		Cuts: []model.ShardCut{
			{ShardID: 0, PreviousHeight: 1, EndHeight: 2, EndBlockHash: receipt.BlockHash},
			{ShardID: 1, PreviousHeight: 1, EndHeight: 1, EndBlockHash: hash(0x20)},
		},
		Receipts: []model.FinalizedBlockReceipt{receipt}, Payments: payments,
	}

	proposal, metric, err := (batch.Builder{CreditChunkSize: 1}).BuildMeasured(frozen, merkle.Hash{})
	require.NoError(t, err)
	require.NoError(t, batch.VerifyBatch(proposal))
	require.Equal(t, 4, metric.ReceiptCount)
	require.Equal(t, 2, metric.CreditCount)
	require.Equal(t, 2, metric.CollapsedReceiptCount)
	require.Equal(t, "26", metric.OriginalValue)
	require.Len(t, proposal.Sidecar.ShardAggregations, 2)

	creditsByRecipient := make(map[account.Address]model.AggregatedCredit)
	for _, aggregation := range proposal.Sidecar.ShardAggregations {
		require.Equal(t, int64(1), aggregation.ShardID)
		require.Equal(t, uint32(2), aggregation.ChunkCount)
		require.Len(t, aggregation.Credits, 1)
		credit := aggregation.Credits[0]
		creditsByRecipient[credit.Recipient] = credit
	}
	require.Equal(t, int64(15), creditsByRecipient[recipientA].Amount.Int64())
	require.Equal(t, uint32(3), creditsByRecipient[recipientA].ReceiptCount)
	require.Equal(t, int64(11), creditsByRecipient[recipientB].Amount.Int64())
	require.Equal(t, uint32(1), creditsByRecipient[recipientB].ReceiptCount)

	packages, err := batch.BuildAggregationPackages(proposal)
	require.NoError(t, err)
	require.Len(t, packages, 2)
	for _, pack := range packages {
		require.NoError(t, batch.VerifyAggregationPackage(pack, proposal.Header))
	}
}

func TestBuilderCountsOriginalPaymentsInSourceAbstract(t *testing.T) {
	t.Parallel()

	raw := []paymentmodel.CrossShardPayment{
		payment(address(1), address(9), 2, 0),
		payment(address(2), address(9), 3, 0),
	}
	abstracts, err := receipt.AggregatePayments(raw)
	require.NoError(t, err)
	require.Len(t, abstracts, 1)

	sourceReceipt := model.FinalizedBlockReceipt{
		ShardID: 0, Height: 1, ParentHash: hash(1), BlockHash: hash(2), Payments: abstracts,
	}
	proposal, metric, err := (batch.Builder{CreditChunkSize: 512}).BuildMeasured(window.FrozenWindow{
		WindowID: 1, OpenedAt: time.Unix(1, 0), SealedAt: time.Unix(2, 0),
		Cuts: []model.ShardCut{
			{ShardID: 0, EndHeight: 1, EndBlockHash: sourceReceipt.BlockHash},
			{ShardID: 1, EndBlockHash: hash(3)},
		},
		Receipts: []model.FinalizedBlockReceipt{sourceReceipt}, Payments: abstracts,
	}, merkle.Hash{})
	require.NoError(t, err)
	require.NoError(t, batch.VerifyBatch(proposal))
	require.Equal(t, 2, metric.ReceiptCount)
	require.Equal(t, 1, metric.SourceAbstractCount)
	require.Equal(t, 1, metric.SourceCollapsedCount)
	require.Equal(t, 1, metric.CreditCount)
	require.Equal(t, 1, metric.CollapsedReceiptCount)
	require.Len(t, proposal.Sidecar.ShardAggregations, 1)
	require.Equal(t, uint32(2), proposal.Sidecar.ShardAggregations[0].Credits[0].ReceiptCount)
	require.Equal(t, int64(5), proposal.Sidecar.ShardAggregations[0].Credits[0].Amount.Int64())
}

func TestBuilderIsDeterministicAcrossPaymentOrder(t *testing.T) {
	t.Parallel()

	p1 := payment(address(1), address(9), 2, 0)
	p2 := payment(address(2), address(9), 3, 0)
	build := func(payments []paymentmodel.CrossShardPayment) model.AggregationBatch {
		receipt := model.FinalizedBlockReceipt{
			ShardID: 0, Height: 1, ParentHash: hash(1), BlockHash: hash(2), Payments: payments,
		}
		proposal, err := (batch.Builder{}).Build(window.FrozenWindow{
			WindowID: 1,
			Cuts: []model.ShardCut{
				{ShardID: 0, EndHeight: 1, EndBlockHash: receipt.BlockHash},
				{ShardID: 1, EndBlockHash: hash(3)},
			},
			Receipts: []model.FinalizedBlockReceipt{receipt}, Payments: payments,
		}, merkle.Hash{})
		require.NoError(t, err)
		return proposal
	}

	forward := build([]paymentmodel.CrossShardPayment{p1, p2})
	reverse := build([]paymentmodel.CrossShardPayment{p2, p1})
	require.Equal(t, forward.Header, reverse.Header)
	require.Equal(t, forward.Sidecar.ShardAggregations, reverse.Sidecar.ShardAggregations)
}

func payment(sender, recipient account.Address, amount int64, nonce uint64) paymentmodel.CrossShardPayment {
	return paymentmodel.CrossShardPayment{
		Version: paymentmodel.CurrentVersion, ChainID: 1, Sender: sender, Recipient: recipient,
		SourceShard: 0, DestinationShard: 1, AssetID: paymentmodel.NativeAssetID,
		Amount: big.NewInt(amount), Nonce: nonce, ExpiryEpoch: 100,
	}
}

func address(value byte) account.Address {
	var result account.Address
	result[0] = value
	return result
}

func hash(value byte) merkle.Hash {
	var result merkle.Hash
	result[0] = value
	return result
}
