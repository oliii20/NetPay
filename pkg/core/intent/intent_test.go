package intent

import (
	"encoding/hex"
	"errors"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
)

func TestPaymentIntentIDGolden(t *testing.T) {
	t.Parallel()

	p := testPaymentIntent()

	got, err := p.ID()
	require.NoError(t, err)
	require.Equal(t, "7ddc66975962086482327bec28147bca90b95b3e5dfc2022ecd99da1ccfd0e7b", hex.EncodeToString(got[:]))
}

func TestPaymentIntentIDIgnoresSignature(t *testing.T) {
	t.Parallel()

	left := testPaymentIntent()
	right := testPaymentIntent()
	right.Signature = []byte("a different signature")

	leftID, err := left.ID()
	require.NoError(t, err)
	rightID, err := right.ID()
	require.NoError(t, err)
	require.Equal(t, leftID, rightID)
}

func TestPaymentIntentIDChangesWithSignedFields(t *testing.T) {
	t.Parallel()

	original := testPaymentIntent()
	originalID, err := original.ID()
	require.NoError(t, err)

	changed := testPaymentIntent()
	changed.Amount = new(big.Int).Add(changed.Amount, big.NewInt(1))
	changedID, err := changed.ID()
	require.NoError(t, err)

	require.NotEqual(t, originalID, changedID)
}

func TestPaymentIntentIDRejectsNonPositiveAmount(t *testing.T) {
	t.Parallel()

	p := testPaymentIntent()
	p.Amount.SetInt64(0)
	_, err := p.ID()
	require.ErrorIs(t, err, ErrInvalidAmount)
}

func TestPaymentIntentValidate(t *testing.T) {
	t.Parallel()

	validCtx := ValidationContext{
		ChainID:      11,
		ShardID:      1,
		ShardCount:   4,
		CurrentEpoch: 6,
	}

	tests := []struct {
		name   string
		mutate func(*PaymentIntent)
		ctx    ValidationContext
		want   error
	}{
		{name: "valid", ctx: validCtx},
		{name: "unsupported version", ctx: validCtx, mutate: func(p *PaymentIntent) { p.Version++ }, want: ErrUnsupportedVersion},
		{name: "wrong chain", ctx: validCtx, mutate: func(p *PaymentIntent) { p.ChainID++ }, want: ErrWrongChain},
		{name: "nil amount", ctx: validCtx, mutate: func(p *PaymentIntent) { p.Amount = nil }, want: ErrInvalidAmount},
		{name: "zero amount", ctx: validCtx, mutate: func(p *PaymentIntent) { p.Amount.SetInt64(0) }, want: ErrInvalidAmount},
		{name: "same participant", ctx: validCtx, mutate: func(p *PaymentIntent) { p.Recipient = p.Sender }, want: ErrSameParticipant},
		{name: "negative source shard", ctx: validCtx, mutate: func(p *PaymentIntent) { p.SourceShard = -1 }, want: ErrInvalidSourceShard},
		{name: "source shard out of range", ctx: validCtx, mutate: func(p *PaymentIntent) { p.SourceShard = 4 }, want: ErrInvalidSourceShard},
		{name: "destination shard out of range", ctx: validCtx, mutate: func(p *PaymentIntent) { p.DestinationShard = 4 }, want: ErrInvalidDestinationShard},
		{name: "wrong execution shard", ctx: validCtx, mutate: func(p *PaymentIntent) { p.SourceShard = 0 }, want: ErrWrongSourceShard},
		{name: "same shard", ctx: validCtx, mutate: func(p *PaymentIntent) { p.DestinationShard = p.SourceShard }, want: ErrSameShard},
		{name: "unsupported asset", ctx: validCtx, mutate: func(p *PaymentIntent) { p.AssetID[31] = 1 }, want: ErrUnsupportedAsset},
		{name: "expired", ctx: validCtx, mutate: func(p *PaymentIntent) { p.ExpiryEpoch = validCtx.CurrentEpoch }, want: ErrExpired},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := testPaymentIntent()
			if tc.mutate != nil {
				tc.mutate(&p)
			}

			err := p.Validate(tc.ctx)
			if tc.want == nil {
				require.NoError(t, err)
				return
			}
			require.True(t, errors.Is(err, tc.want), "got error %v", err)
		})
	}
}

func testPaymentIntent() PaymentIntent {
	var sender, recipient account.Address
	for i := range sender {
		sender[i] = byte(i + 1)
		recipient[i] = byte(0x80 + i)
	}

	return PaymentIntent{
		Version:          CurrentVersion,
		ChainID:          11,
		Sender:           sender,
		Recipient:        recipient,
		SourceShard:      1,
		DestinationShard: 3,
		AssetID:          NativeAssetID,
		Amount:           new(big.Int).SetBytes([]byte{0x01, 0x00, 0x01}),
		Nonce:            9,
		ExpiryEpoch:      17,
		Signature:        []byte("signature is deliberately outside the ID"),
	}
}
