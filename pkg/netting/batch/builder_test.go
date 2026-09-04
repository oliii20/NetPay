package batch_test

import (
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/window"
)

func TestBuilderCreatesDeterministicTwoLevelBatch(t *testing.T) {
	t.Parallel()

	forward := payment(0, 1, 10, 1)
	reverse := payment(1, 0, 7, 2)
	frozen := window.FrozenWindow{
		WindowID: 3,
		OpenedAt: time.Unix(1, 0),
		SealedAt: time.Unix(2, 0),
		Cuts: []model.ShardCut{
			{ShardID: 1, PreviousHeight: 4, EndHeight: 5, EndBlockHash: hash(0x21)},
			{ShardID: 0, PreviousHeight: 3, EndHeight: 5, EndBlockHash: hash(0x11)},
		},
		Intents: []intent.PaymentIntent{forward, reverse},
	}
	previous := hash(0xaa)

	proposal, err := (batch.Builder{}).Build(frozen, previous)
	require.NoError(t, err)
	require.Equal(t, uint64(3), proposal.Header.WindowID)
	require.Equal(t, previous, proposal.Header.PreviousBatchID)
	require.NotZero(t, proposal.Header.MatchRoot)
	require.NotZero(t, proposal.Header.BatchID)
	require.Equal(t, int64(0), proposal.Header.Cuts[0].ShardID)
	require.Len(t, proposal.Sidecar.IntentResults, 2)
	require.Len(t, proposal.Sidecar.ShardSettlements, 2)

	bySource := make(map[int64]model.IntentResult)
	for _, result := range proposal.Sidecar.IntentResults {
		bySource[result.Intent.SourceShard] = result
	}
	require.Equal(t, int64(7), bySource[0].MatchedAmount.Int64())
	require.Equal(t, int64(3), bySource[0].FallbackAmount.Int64())
	require.Equal(t, int64(7), bySource[1].MatchedAmount.Int64())
	require.Zero(t, bySource[1].FallbackAmount.Sign())

	rebuilt, err := (batch.Builder{}).Build(frozen, previous)
	require.NoError(t, err)
	require.Equal(t, proposal, rebuilt)

	frozen.Intents[0].Amount.SetInt64(999)
	require.Equal(t, int64(10), bySource[0].Intent.Amount.Int64())
}

func TestBuilderRejectsEmptyWindowAndMissingShard(t *testing.T) {
	t.Parallel()

	_, err := (batch.Builder{}).Build(window.FrozenWindow{}, merkle.Hash{})
	require.ErrorIs(t, err, batch.ErrEmptyWindow)

	_, err = (batch.Builder{}).Build(window.FrozenWindow{
		WindowID: 1,
		Cuts:     []model.ShardCut{{ShardID: 0}},
		Intents:  []intent.PaymentIntent{payment(0, 1, 1, 1)},
	}, merkle.Hash{})
	require.ErrorIs(t, err, batch.ErrMissingShardCut)
}

func payment(source, destination, amount, nonce int64) intent.PaymentIntent {
	var sender, recipient account.Address
	sender[0] = byte(source + 1)
	recipient[0] = byte(destination + 11)

	return intent.PaymentIntent{
		Version: intent.CurrentVersion, ChainID: 1, Sender: sender, Recipient: recipient,
		SourceShard: source, DestinationShard: destination, AssetID: intent.NativeAssetID,
		Amount: big.NewInt(amount), Nonce: uint64(nonce), ExpiryEpoch: 100,
	}
}

func hash(value byte) merkle.Hash {
	var result merkle.Hash
	result[0] = value

	return result
}
