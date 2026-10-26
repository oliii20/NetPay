package beaconop

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/config"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/window"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
)

func TestPaymentSettlementMapping(t *testing.T) {
	for _, batchOnly := range []bool{false, true} {
		name := "full"
		if batchOnly {
			name = "batch_only"
		}
		t.Run(name, func(t *testing.T) {
			var recipient account.Address
			recipient[19] = 4
			payments := make([]paymentmodel.CrossShardPayment, 2)
			for i := range payments {
				var sender account.Address
				sender[19] = byte(i + 1)
				payments[i] = paymentmodel.CrossShardPayment{Sender: sender, Recipient: recipient, Amount: big.NewInt(int64(i + 1)), SourceShard: 0, DestinationShard: 1}
			}
			receipt := model.FinalizedBlockReceipt{ShardID: 0, Height: 1, Payments: payments}
			frozen := window.FrozenWindow{WindowID: 1, Cuts: []model.ShardCut{{ShardID: 0, EndHeight: 1}, {ShardID: 1}}, Receipts: []model.FinalizedBlockReceipt{receipt}, Payments: payments}
			proposal, err := (batch.Builder{BatchOnly: batchOnly, CreditChunkSize: 1}).Build(frozen, merkle.Hash{})
			require.NoError(t, err)
			require.NoError(t, batch.VerifyBatch(proposal))
			assignments := paymentSettlements(proposal)
			require.Len(t, assignments, 2)
			require.Equal(t, int64(1), assignments[0].ShardID)
			if batchOnly {
				require.NotEqual(t, assignments[0].ChunkIndex, assignments[1].ChunkIndex)
			} else {
				require.Equal(t, assignments[0].ChunkIndex, assignments[1].ChunkIndex)
			}
			// Input order must not alter the committed batch.
			frozen.Payments[0], frozen.Payments[1] = frozen.Payments[1], frozen.Payments[0]
			reversed, err := (batch.Builder{BatchOnly: batchOnly, CreditChunkSize: 1}).Build(frozen, merkle.Hash{})
			require.NoError(t, err)
			require.Equal(t, proposal.Header.BatchID, reversed.Header.BatchID)
		})
	}
}

func TestAggregationOnlyUsesOneCreditDespiteConfiguredChunkSize(t *testing.T) {
	op := New(nil, nil, nil, 2, 11, 0, config.AggregationCfg{AggregationOnly: true, CreditChunkSize: 512})
	payments := []paymentmodel.CrossShardPayment{
		{Recipient: account.Address{1}, SourceShard: 0, DestinationShard: 1, Amount: big.NewInt(3), Nonce: 1},
		{Recipient: account.Address{1}, SourceShard: 0, DestinationShard: 1, Amount: big.NewInt(5), Nonce: 2},
		{Recipient: account.Address{2}, SourceShard: 0, DestinationShard: 1, Amount: big.NewInt(7), Nonce: 3},
	}
	receipt := model.FinalizedBlockReceipt{ShardID: 0, Height: 1, Payments: payments}
	proposal, err := op.builder.Build(window.FrozenWindow{WindowID: 1, Cuts: []model.ShardCut{{ShardID: 0, EndHeight: 1}, {ShardID: 1}}, Payments: payments, Receipts: []model.FinalizedBlockReceipt{receipt}}, merkle.Hash{})
	require.NoError(t, err)
	require.NoError(t, batch.VerifyBatch(proposal))
	require.Equal(t, uint32(1), proposal.Header.CreditChunkSize)
	require.False(t, proposal.Header.BatchOnly)
	require.Len(t, proposal.Sidecar.ShardAggregations, 2)
	total := new(big.Int)
	for _, aggregation := range proposal.Sidecar.ShardAggregations {
		require.Len(t, aggregation.Credits, 1)
		total.Add(total, aggregation.Credits[0].Amount)
	}
	require.Equal(t, int64(15), total.Int64())
	packs, err := batch.BuildAggregationPackages(proposal)
	require.NoError(t, err)
	for _, pack := range packs {
		require.NoError(t, batch.VerifyAggregationPackage(pack, proposal.Header))
	}
	require.Len(t, paymentSettlements(proposal), 3)
}
