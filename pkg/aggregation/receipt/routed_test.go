package receipt_test

import (
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/receipt"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/routing"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/window"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
)

func sourceBlock(t *testing.T) *block.Block {
	t.Helper()
	var sender, hot, cold account.Address
	sender[19], hot[19], cold[19] = 4, 1, 2
	b := &block.Block{Header: block.Header{Number: 1, Type: block.TxBlockType,
		ParentBlockHash: make([]byte, 32), StateRoot: make([]byte, 32)}}
	for i, recipient := range []account.Address{hot, cold, hot} {
		tx := *transaction.NewTransaction(sender, recipient, big.NewInt(int64(i+2)), big.NewInt(0), uint64(i), time.Unix(100, 0))
		hash, err := tx.Hash()
		require.NoError(t, err)
		tx.ROriginalHash, tx.RelayStage = hash, transaction.Relay1Tx
		b.TxList = append(b.TxList, tx)
	}
	return b
}

func TestPostCommitRoutingKeepsOriginalStructure(t *testing.T) {
	for _, compact := range []bool{false, true} {
		t.Run(map[bool]string{false: "individual attestations", true: "compacted attestations"}[compact], func(t *testing.T) {
			b := sourceBlock(t)
			before, err := b.Hash()
			require.NoError(t, err)
			routed, relay, err := receipt.BuildRouted(0, 0, 11, 4, b, time.Unix(110, 0),
				routing.Policy{Version: 1, Destinations: []int64{1}}, compact)
			require.NoError(t, err)
			require.Len(t, relay, 1)
			require.Equal(t, b.TxList[1], relay[0])
			after, err := b.Hash()
			require.NoError(t, err)
			require.Equal(t, before, after, "routing cannot mutate committed source block")
			require.Equal(t, before, routed.BlockHash[:])
			require.Len(t, routed.DestinationStats, 2)
			require.Equal(t, int64(2), routed.DestinationStats[0].PaymentCount)
			require.Equal(t, int64(1), routed.DestinationStats[0].RecipientCount)
			require.Equal(t, int64(1), routed.DestinationStats[1].PaymentCount, "relay-only destinations remain observable")
			ids, total := map[paymentmodel.ID]bool{}, new(big.Int)
			for _, p := range routed.Payments {
				require.NoError(t, p.ValidateSourceAbstract(paymentmodel.ValidationContext{ChainID: 11, ShardID: 0, ShardCount: 4}))
				total.Add(total, p.Amount)
				logical, err := p.LogicalIDs()
				require.NoError(t, err)
				for _, id := range logical {
					require.False(t, ids[id])
					ids[id] = true
				}
			}
			require.Len(t, ids, 2)
			require.Equal(t, int64(6), total.Int64())
			var relayID paymentmodel.ID
			copy(relayID[:], relay[0].ROriginalHash)
			require.False(t, ids[relayID], "an original cannot be on both paths")
			for _, tx := range b.TxList {
				require.Nil(t, tx.Payment)
				require.Equal(t, transaction.RelayTxType, tx.TxType())
			}
			proposal, err := (batch.Builder{CreditChunkSize: 512}).Build(window.FrozenWindow{
				WindowID: 1, Receipts: []model.FinalizedBlockReceipt{routed}, Payments: routed.Payments,
				Cuts: []model.ShardCut{{ShardID: 0, EndHeight: 1, EndBlockHash: routed.BlockHash}, {ShardID: 1}, {ShardID: 2}, {ShardID: 3}},
			}, merkle.Hash{})
			require.NoError(t, err)
			require.NoError(t, batch.VerifyBatch(proposal))
		})
	}
}

func TestAllRelayReceiptRetainsSourceWatermark(t *testing.T) {
	b := sourceBlock(t)
	routed, relay, err := receipt.BuildRouted(0, 0, 11, 4, b, time.Now(), routing.Policy{Destinations: []int64{3}}, true)
	require.NoError(t, err)
	require.Empty(t, routed.Payments)
	require.Len(t, relay, 3)
	require.Equal(t, b.Number, routed.Height)
	require.Len(t, routed.DestinationStats, 2)
}
