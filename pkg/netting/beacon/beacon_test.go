package beacon_test

import (
	"bytes"
	"encoding/gob"
	"errors"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/beacon"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/window"
)

func TestValidatorAndStoreCommitSequentialMatchRootBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "beacon.db")
	store, err := beacon.OpenStore(path)
	require.NoError(t, err)
	validator := beacon.NewValidator(store)

	first := proposal(t, 1, merkle.Hash{}, 0, beaconHash(0x11), beaconHash(0x21), 1)
	require.NoError(t, validator.Validate(first))
	committed, err := store.Commit(first, time.Unix(10, 0))
	require.NoError(t, err)
	require.Equal(t, uint64(1), committed.Number)
	require.Equal(t, first.Header, committed.Body)
	for _, result := range first.Sidecar.IntentResults {
		consumed, stateErr := store.HasIntent(result.IntentID)
		require.NoError(t, stateErr)
		require.True(t, consumed)
	}

	second := proposal(t, 2, first.Header.BatchID, 1, beaconHash(0x12), beaconHash(0x22), 2)
	require.NoError(t, validator.Validate(second))
	committed, err = store.Commit(second, time.Unix(20, 0))
	require.NoError(t, err)
	require.Equal(t, uint64(2), committed.Number)
	require.NotZero(t, committed.ParentHash)

	stored, err := store.GetProposal(second.Header.BatchID)
	require.NoError(t, err)
	require.Equal(t, second, stored)
	_, err = store.Commit(second, time.Now())
	require.ErrorIs(t, err, beacon.ErrBatchCommitted)
	require.NoError(t, store.Close())

	store, err = beacon.OpenStore(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	tip, found, err := store.Tip()
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, second.Header.BatchID, tip.Body.BatchID)
}

func TestValidatorRejectsTamperingCoverageAndExpiry(t *testing.T) {
	store, err := beacon.OpenStore(filepath.Join(t.TempDir(), "beacon.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	validator := beacon.NewValidator(store)
	valid := proposal(t, 1, merkle.Hash{}, 0, beaconHash(0x11), beaconHash(0x21), 1)

	tampered := valid.Clone()
	tampered.Sidecar.IntentResults[0].MatchedAmount.SetInt64(1)
	tampered.Sidecar.IntentResults[0].FallbackAmount.Sub(
		tampered.Sidecar.IntentResults[0].Intent.Amount,
		tampered.Sidecar.IntentResults[0].MatchedAmount,
	)
	require.ErrorIs(t, validator.Validate(tampered), beacon.ErrDerivedBatchMismatch)

	brokenCoverage := valid.Clone()
	brokenCoverage.Sidecar.FinalizedBlocks[0].Height = 2
	require.ErrorIs(t, validator.Validate(brokenCoverage), beacon.ErrReceiptCoverage)

	expired := proposal(t, 1, merkle.Hash{}, 0, beaconHash(0x31), beaconHash(0x41), 1)
	for idx := range expired.Sidecar.FinalizedBlocks {
		for intentIdx := range expired.Sidecar.FinalizedBlocks[idx].Intents {
			expired.Sidecar.FinalizedBlocks[idx].Intents[intentIdx].ExpiryEpoch = 1
		}
	}
	require.True(t, errors.Is(validator.Validate(expired), beacon.ErrExpiredBatchIntent))
}

func TestValidatorAcceptsGobRoundTripWithEmptyShardInstructions(t *testing.T) {
	store, err := beacon.OpenStore(filepath.Join(t.TempDir(), "beacon.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	first := beaconPayment(0, 1, 10, 1)
	second := beaconPayment(1, 0, 7, 2)
	frozen := window.FrozenWindow{
		WindowID: 1,
		Cuts: []model.ShardCut{
			{ShardID: 0, EndHeight: 1, EndBlockHash: beaconHash(0x11)},
			{ShardID: 1, EndHeight: 1, EndBlockHash: beaconHash(0x21)},
			{ShardID: 2},
			{ShardID: 3},
		},
		Receipts: []model.FinalizedBlockReceipt{
			{ShardID: 0, Height: 1, ParentHash: beaconHash(0x10), BlockHash: beaconHash(0x11), Epoch: 1, Intents: []intent.PaymentIntent{first}},
			{ShardID: 1, Height: 1, ParentHash: beaconHash(0x20), BlockHash: beaconHash(0x21), Epoch: 1, Intents: []intent.PaymentIntent{second}},
		},
		Intents: []intent.PaymentIntent{first, second},
	}
	proposal, err := (batch.Builder{}).Build(frozen, merkle.Hash{})
	require.NoError(t, err)
	var encoded bytes.Buffer
	require.NoError(t, gob.NewEncoder(&encoded).Encode(proposal))
	var decoded model.BatchProposal
	require.NoError(t, gob.NewDecoder(&encoded).Decode(&decoded))

	require.NoError(t, beacon.NewValidator(store).Validate(decoded))
}

func proposal(
	t *testing.T,
	windowID uint64,
	previous merkle.Hash,
	previousHeight uint64,
	firstHash, secondHash merkle.Hash,
	epoch int64,
) model.BatchProposal {
	t.Helper()
	first := beaconPayment(0, 1, 10, windowID*2)
	second := beaconPayment(1, 0, 7, windowID*2+1)
	firstParent := beaconHash(0x10)
	secondParent := beaconHash(0x20)
	if previousHeight > 0 {
		firstParent[0] = firstHash[0] - 1
		secondParent[0] = secondHash[0] - 1
	}
	frozen := window.FrozenWindow{
		WindowID: windowID,
		Cuts: []model.ShardCut{
			{ShardID: 0, PreviousHeight: previousHeight, EndHeight: previousHeight + 1, EndBlockHash: firstHash},
			{ShardID: 1, PreviousHeight: previousHeight, EndHeight: previousHeight + 1, EndBlockHash: secondHash},
		},
		Receipts: []model.FinalizedBlockReceipt{
			{ShardID: 0, Height: previousHeight + 1, ParentHash: firstParent, BlockHash: firstHash, Epoch: epoch, Intents: []intent.PaymentIntent{first}},
			{ShardID: 1, Height: previousHeight + 1, ParentHash: secondParent, BlockHash: secondHash, Epoch: epoch, Intents: []intent.PaymentIntent{second}},
		},
		Intents: []intent.PaymentIntent{first, second},
	}
	proposal, err := (batch.Builder{}).Build(frozen, previous)
	require.NoError(t, err)

	return proposal
}

func beaconPayment(source, destination, amount int64, nonce uint64) intent.PaymentIntent {
	var sender, recipient account.Address
	sender[0] = byte(source + 1)
	recipient[0] = byte(destination + 11)

	return intent.PaymentIntent{
		Version: intent.CurrentVersion, ChainID: 1, Sender: sender, Recipient: recipient,
		SourceShard: source, DestinationShard: destination, AssetID: intent.NativeAssetID,
		Amount: big.NewInt(amount), Nonce: nonce, ExpiryEpoch: 100,
	}
}

func beaconHash(value byte) merkle.Hash {
	var result merkle.Hash
	result[0] = value

	return result
}
