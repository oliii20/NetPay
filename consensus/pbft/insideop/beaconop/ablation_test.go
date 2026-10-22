package beaconop

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

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
