package beacon_test

import (
	"errors"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/beacon"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/window"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
)

func TestValidatorStoreAndReplayProtection(t *testing.T) {
	t.Parallel()

	store, err := beacon.OpenStore(filepath.Join(t.TempDir(), "beacon.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	validator := beacon.NewValidator(store, 11, 2)

	payment := beaconPayment()
	first := buildBatch(t, 1, merkle.Hash{}, 0, 1, testHash(1), testHash(2), payment)
	require.NoError(t, validator.Validate(first))
	committed, err := store.Commit(first, time.Unix(10, 0))
	require.NoError(t, err)
	require.Equal(t, first.Header, committed.Body)
	id, err := payment.ID()
	require.NoError(t, err)
	consumed, err := store.HasPayment(id)
	require.NoError(t, err)
	require.True(t, consumed)

	loaded, err := store.GetBatch(first.Header.BatchID)
	require.NoError(t, err)
	require.Equal(t, first, loaded)

	second := buildBatch(t, 2, first.Header.BatchID, 1, 2, testHash(2), testHash(3), payment)
	err = validator.Validate(second)
	require.True(t, errors.Is(err, beacon.ErrPaymentConsumed), "unexpected error: %v", err)
}

func buildBatch(
	t *testing.T,
	windowID uint64,
	previous merkle.Hash,
	previousHeight, endHeight uint64,
	parentHash, blockHash merkle.Hash,
	payment paymentmodel.CrossShardPayment,
) model.AggregationBatch {
	t.Helper()
	receipt := model.FinalizedBlockReceipt{
		ShardID: 0, Height: endHeight, ParentHash: parentHash, BlockHash: blockHash,
		Epoch: 1, Payments: []paymentmodel.CrossShardPayment{payment},
	}
	proposal, err := (batch.Builder{}).Build(window.FrozenWindow{
		WindowID: windowID,
		Cuts: []model.ShardCut{
			{ShardID: 0, PreviousHeight: previousHeight, EndHeight: endHeight, EndBlockHash: blockHash},
			{ShardID: 1, PreviousHeight: 0, EndHeight: 0},
		},
		Receipts: []model.FinalizedBlockReceipt{receipt}, Payments: []paymentmodel.CrossShardPayment{payment},
	}, previous)
	require.NoError(t, err)
	return proposal
}

func beaconPayment() paymentmodel.CrossShardPayment {
	var sender, recipient account.Address
	sender[0] = 1
	recipient[0] = 2
	return paymentmodel.CrossShardPayment{
		Version: paymentmodel.CurrentVersion, ChainID: 11, Sender: sender, Recipient: recipient,
		SourceShard: 0, DestinationShard: 1, Amount: big.NewInt(7), ExpiryEpoch: 100,
	}
}

func testHash(value byte) merkle.Hash {
	var result merkle.Hash
	result[0] = value
	return result
}

func TestValidationCacheChecksFullPayloadAndCommittedTip(t *testing.T) {
	store, err := beacon.OpenStore(filepath.Join(t.TempDir(), "beacon.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	validator := beacon.NewValidator(store, 11, 2)
	proposal := buildBatch(t, 1, merkle.Hash{}, 0, 1, testHash(1), testHash(2), beaconPayment())
	require.NoError(t, validator.Validate(proposal))
	// A separate decoded/cloned object is eligible for reuse, not just the same pointer.
	require.NoError(t, validator.Validate(proposal.Clone()))
	for _, field := range []string{"credit amount", "source payment", "cut", "batch id"} {
		t.Run(field, func(t *testing.T) {
			changed := proposal.Clone()
			switch field {
			case "credit amount":
				changed.Sidecar.ShardAggregations[0].Credits[0].Amount.Add(changed.Sidecar.ShardAggregations[0].Credits[0].Amount, big.NewInt(1))
			case "source payment":
				changed.Sidecar.Receipts[0].Payments[0].Amount.Add(changed.Sidecar.Receipts[0].Payments[0].Amount, big.NewInt(1))
			case "cut":
				changed.Header.Cuts[0].EndHeight++
			case "batch id":
				changed.Header.BatchID[0] ^= 1
			}
			require.Error(t, validator.Validate(changed))
			require.NoError(t, validator.Validate(proposal))
		})
	}
	_, err = store.Commit(proposal, time.Now())
	require.NoError(t, err)
	// Previously successful content must be rechecked after the store advances.
	require.ErrorIs(t, validator.Validate(proposal), beacon.ErrBatchSequence)
	replayed := buildBatch(t, 2, proposal.Header.BatchID, 1, 2, testHash(2), testHash(3), beaconPayment())
	require.ErrorIs(t, validator.Validate(replayed), beacon.ErrPaymentConsumed)
}

func TestReceiptValidationCacheRequiresIdenticalContent(t *testing.T) {
	store, err := beacon.OpenStore(filepath.Join(t.TempDir(), "beacon.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	validator := beacon.NewValidator(store, 11, 2)
	receipt := model.FinalizedBlockReceipt{
		ShardID: 0, Height: 1, BlockHash: testHash(2), Epoch: 1,
		Payments: []paymentmodel.CrossShardPayment{beaconPayment()},
	}
	require.NoError(t, validator.ValidateReceipt(receipt))
	require.NoError(t, validator.ValidateReceipt(receipt.Clone()))

	changed := receipt.Clone()
	changed.Payments[0].Amount = nil
	require.ErrorIs(t, validator.ValidateReceipt(changed), beacon.ErrInvalidPayment)
}

func BenchmarkBatchValidation(b *testing.B) {
	store, err := beacon.OpenStore(filepath.Join(b.TempDir(), "beacon.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := store.Close(); err != nil {
			b.Error(err)
		}
	})
	payments := make([]paymentmodel.CrossShardPayment, 2000)
	for i := range payments {
		payments[i] = beaconPayment()
		payments[i].Nonce = uint64(i)
	}
	receipt := model.FinalizedBlockReceipt{ShardID: 0, Height: 1, BlockHash: testHash(2), Epoch: 1, Payments: payments}
	proposal, err := (batch.Builder{CreditChunkSize: 512}).Build(window.FrozenWindow{
		WindowID: 1, Cuts: []model.ShardCut{{ShardID: 0, EndHeight: 1, EndBlockHash: testHash(2)}, {ShardID: 1}},
		Receipts: []model.FinalizedBlockReceipt{receipt}, Payments: payments,
	}, merkle.Hash{})
	if err != nil {
		b.Fatal(err)
	}
	b.Run("full", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := beacon.NewValidator(store, 11, 2).Validate(proposal); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("reused", func(b *testing.B) {
		validator := beacon.NewValidator(store, 11, 2)
		if err := validator.Validate(proposal); err != nil {
			b.Fatal(err)
		}
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := validator.Validate(proposal); err != nil {
				b.Fatal(err)
			}
		}
	})
}
