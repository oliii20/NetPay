package model_test

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
)

func TestNewIntentResultCopiesInputAndPreservesConservation(t *testing.T) {
	t.Parallel()

	payment := modelTestIntent(10)
	result, err := model.NewIntentResult(payment, big.NewInt(7))
	require.NoError(t, err)
	require.Equal(t, int64(7), result.MatchedAmount.Int64())
	require.Equal(t, int64(3), result.FallbackAmount.Int64())
	require.NoError(t, result.Validate())

	payment.Amount.SetInt64(999)
	payment.Signature[0] ^= 0xff
	require.Equal(t, int64(10), result.Intent.Amount.Int64())
	require.Equal(t, []byte("signature"), result.Intent.Signature)
}

func TestNewIntentResultRejectsInvalidMatchedAmount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		matched *big.Int
	}{
		{name: "nil", matched: nil},
		{name: "negative", matched: big.NewInt(-1)},
		{name: "larger than original", matched: big.NewInt(11)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := model.NewIntentResult(modelTestIntent(10), tc.matched)
			require.ErrorIs(t, err, model.ErrInvalidMatchedAmount)
		})
	}
}

func TestNewIntentResultRejectsInvalidOriginalAmount(t *testing.T) {
	t.Parallel()

	payment := modelTestIntent(10)
	payment.Amount = nil
	_, err := model.NewIntentResult(payment, big.NewInt(0))
	require.ErrorIs(t, err, model.ErrInvalidOriginalAmount)
}

func TestIntentResultValidateRejectsCorruption(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*model.IntentResult)
		want   error
	}{
		{
			name:   "invalid original",
			mutate: func(result *model.IntentResult) { result.Intent.Amount = nil },
			want:   model.ErrInvalidOriginalAmount,
		},
		{
			name:   "invalid matched",
			mutate: func(result *model.IntentResult) { result.MatchedAmount = big.NewInt(-1) },
			want:   model.ErrInvalidMatchedAmount,
		},
		{
			name:   "invalid fallback",
			mutate: func(result *model.IntentResult) { result.FallbackAmount = big.NewInt(11) },
			want:   model.ErrInvalidFallbackAmount,
		},
		{
			name:   "intent ID mismatch",
			mutate: func(result *model.IntentResult) { result.IntentID[0] ^= 0xff },
			want:   model.ErrIntentIDMismatch,
		},
		{
			name:   "amount conservation",
			mutate: func(result *model.IntentResult) { result.FallbackAmount.SetInt64(2) },
			want:   model.ErrAmountConservation,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result, err := model.NewIntentResult(modelTestIntent(10), big.NewInt(7))
			require.NoError(t, err)
			tc.mutate(&result)
			require.ErrorIs(t, result.Validate(), tc.want)
		})
	}
}

func modelTestIntent(amount int64) intent.PaymentIntent {
	var recipient account.Address
	recipient[0] = 1

	return intent.PaymentIntent{
		Version:          intent.CurrentVersion,
		ChainID:          11,
		Recipient:        recipient,
		SourceShard:      0,
		DestinationShard: 1,
		AssetID:          intent.NativeAssetID,
		Amount:           big.NewInt(amount),
		ExpiryEpoch:      2,
		Signature:        []byte("signature"),
	}
}
