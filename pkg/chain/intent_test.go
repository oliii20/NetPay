package chain

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/config"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/registry"
)

func TestIntentTransactionsReserveFundsInNonceOrder(t *testing.T) {
	ctx := context.Background()
	bc := newIntentTestChain(t)

	first := chainTestIntent(0, 10, 0x21)
	badNonce := chainTestIntent(7, 20, 0x22)
	second := chainTestIntent(1, 30, 0x23)
	txs := []transaction.Transaction{
		*transaction.NewIntentTransaction(first, time.Now()),
		*transaction.NewIntentTransaction(badNonce, time.Now()),
		*transaction.NewIntentTransaction(second, time.Now()),
	}

	b, err := bc.GenerateBlock(ctx, testMiner, block.TxBlockType, block.Body{TxList: txs}, block.MigrationOpt{})
	require.NoError(t, err)
	require.Len(t, b.TxList, 2)
	require.Equal(t, first.Nonce, b.TxList[0].Intent.Nonce)
	require.Equal(t, second.Nonce, b.TxList[1].Intent.Nonce)
	require.NoError(t, bc.ValidateBlock(ctx, b))
	require.NoError(t, bc.AddBlock(ctx, b))

	states, err := bc.GetAccountStates(ctx, []account.Address{first.Sender, registry.EscrowAccountAddress})
	require.NoError(t, err)
	initialBalance, ok := new(big.Int).SetString(account.NormalInitBalanceStr, 10)
	require.True(t, ok)
	require.Equal(t, new(big.Int).Sub(initialBalance, big.NewInt(40)), states[0].Balance)
	require.Equal(t, big.NewInt(40), states[1].Balance)
	nextNonce, err := bc.GetNextIntentNonce(ctx, first.Sender)
	require.NoError(t, err)
	require.Equal(t, uint64(2), nextNonce)

	reservation, err := bc.GetReservation(ctx, first)
	require.NoError(t, err)
	require.Equal(t, registry.ReservationReserved, reservation.Status)
	require.Equal(t, first.Amount, reservation.Amount)
}

func TestIntentProposalDropsMalformedEnvelope(t *testing.T) {
	ctx := context.Background()
	bc := newIntentTestChain(t)
	payment := chainTestIntent(0, 10, 0x31)
	tx := transaction.NewIntentTransaction(payment, time.Now())
	tx.Sender[0]++

	b, err := bc.GenerateBlock(
		ctx,
		testMiner,
		block.TxBlockType,
		block.Body{TxList: []transaction.Transaction{*tx}},
		block.MigrationOpt{},
	)
	require.NoError(t, err)
	require.Empty(t, b.TxList)
	require.NoError(t, bc.AddBlock(ctx, b))
	nextNonce, err := bc.GetNextIntentNonce(ctx, payment.Sender)
	require.NoError(t, err)
	require.Zero(t, nextNonce)
}

func TestValidateBlockRejectsTamperedStateRoot(t *testing.T) {
	ctx := context.Background()
	bc := newIntentTestChain(t)
	payment := chainTestIntent(0, 10, 0x41)

	b, err := bc.GenerateBlock(
		ctx,
		testMiner,
		block.TxBlockType,
		block.Body{TxList: []transaction.Transaction{*transaction.NewIntentTransaction(payment, time.Now())}},
		block.MigrationOpt{},
	)
	require.NoError(t, err)
	b.StateRoot = append([]byte(nil), b.StateRoot...)
	b.StateRoot[0] ^= 0xff
	require.ErrorIs(t, bc.ValidateBlock(ctx, b), ErrStateRootMismatch)
}

func newIntentTestChain(t *testing.T) *Chain {
	t.Helper()
	cfg := getTestConfig()
	cfg.ChainID = 11
	cfg.BoltCfg.FilePathDir = t.TempDir()
	bc, err := NewChain(cfg, config.LocalParams{ShardID: 0})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, bc.Close()) })

	return bc
}

func chainTestIntent(nonce uint64, amount int64, recipientTag byte) intent.PaymentIntent {
	var recipient account.Address
	recipient[0] = recipientTag

	return intent.PaymentIntent{
		Version:          intent.CurrentVersion,
		ChainID:          11,
		Sender:           testSender,
		Recipient:        recipient,
		SourceShard:      0,
		DestinationShard: 1,
		AssetID:          intent.NativeAssetID,
		Amount:           big.NewInt(amount),
		Nonce:            nonce,
		ExpiryEpoch:      10,
	}
}
