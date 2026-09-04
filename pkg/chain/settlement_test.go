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
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/fallback"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/registry"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/window"
)

func TestSettlementConsumesReservationsAndCreditsLocalRecipients(t *testing.T) {
	ctx := context.Background()
	shard0 := newSettlementTestChain(t, 0)
	shard1 := newSettlementTestChain(t, 1)
	forward := settlementIntent(0, 1, 10, 0x31, 0x41)
	reverse := settlementIntent(1, 0, 7, 0x32, 0x42)
	reserveIntent(t, shard0, forward)
	reserveIntent(t, shard1, reverse)
	proposal, err := (batch.Builder{}).Build(window.FrozenWindow{
		WindowID: 1,
		Cuts: []model.ShardCut{
			{ShardID: 0, EndHeight: 1, EndBlockHash: settlementHash(0x11)},
			{ShardID: 1, EndHeight: 1, EndBlockHash: settlementHash(0x21)},
		},
		Intents: []intent.PaymentIntent{forward, reverse},
	}, merkle.Hash{})
	require.NoError(t, err)
	packages, err := batch.BuildSettlementPackages(proposal)
	require.NoError(t, err)
	require.Len(t, packages, 2)

	require.NoError(t, shard0.ConfirmMatchRoot(ctx, proposal.Header))
	require.NoError(t, shard1.ConfirmMatchRoot(ctx, proposal.Header))
	for _, pack := range packages {
		chain := shard0
		if pack.Settlement.ShardID == 1 {
			chain = shard1
		}
		tx := transaction.NewSettlementTransaction(pack, time.Now())
		settlementBlock, blockErr := chain.GenerateBlock(
			ctx, testMiner, block.TxBlockType,
			block.Body{TxList: []transaction.Transaction{*tx}}, block.MigrationOpt{},
		)
		require.NoError(t, blockErr)
		require.NoError(t, chain.AddBlock(ctx, settlementBlock))
	}

	forwardReservation, err := shard0.GetReservation(ctx, forward)
	require.NoError(t, err)
	require.Equal(t, registry.ReservationConsumed, forwardReservation.Status)
	require.Equal(t, big.NewInt(7), forwardReservation.MatchedAmount)
	require.Equal(t, big.NewInt(3), forwardReservation.FallbackAmount)
	reverseReservation, err := shard1.GetReservation(ctx, reverse)
	require.NoError(t, err)
	require.Equal(t, registry.ReservationConsumed, reverseReservation.Status)

	initial, ok := new(big.Int).SetString(account.NormalInitBalanceStr, 10)
	require.True(t, ok)
	states0, err := shard0.GetAccountStates(ctx, []account.Address{reverse.Recipient, registry.EscrowAccountAddress})
	require.NoError(t, err)
	require.Equal(t, new(big.Int).Add(initial, big.NewInt(7)), states0[0].Balance)
	require.Zero(t, states0[1].Balance.Sign())
	states1, err := shard1.GetAccountStates(ctx, []account.Address{forward.Recipient, registry.EscrowAccountAddress})
	require.NoError(t, err)
	require.Equal(t, new(big.Int).Add(initial, big.NewInt(7)), states1[0].Balance)
	require.Zero(t, states1[1].Balance.Sign())

	duplicate := transaction.NewSettlementTransaction(packages[0], time.Now())
	duplicateBlock, err := shard0.GenerateBlock(
		ctx, testMiner, block.TxBlockType,
		block.Body{TxList: []transaction.Transaction{*duplicate}}, block.MigrationOpt{},
	)
	require.NoError(t, err)
	require.NoError(t, shard0.AddBlock(ctx, duplicateBlock))
	afterDuplicate, err := shard0.GetAccountStates(ctx, []account.Address{reverse.Recipient})
	require.NoError(t, err)
	require.Equal(t, states0[0].Balance, afterDuplicate[0].Balance)

	pendingFallbacks, err := shard0.GetPendingFallbacks(ctx)
	require.NoError(t, err)
	require.Len(t, pendingFallbacks, 1)
	require.Equal(t, big.NewInt(3), pendingFallbacks[0].Amount)
	fallbackTx := transaction.NewReservedFallbackTransaction(pendingFallbacks[0], time.Now())
	fallbackBlock, err := shard1.GenerateBlock(
		ctx, testMiner, block.TxBlockType,
		block.Body{TxList: []transaction.Transaction{*fallbackTx}}, block.MigrationOpt{},
	)
	require.NoError(t, err)
	require.NoError(t, shard1.AddBlock(ctx, fallbackBlock))
	credited, err := shard1.GetAccountStates(ctx, []account.Address{forward.Recipient})
	require.NoError(t, err)
	require.Equal(t, new(big.Int).Add(initial, big.NewInt(10)), credited[0].Balance)

	duplicateFallback, err := shard1.GenerateBlock(
		ctx, testMiner, block.TxBlockType,
		block.Body{TxList: []transaction.Transaction{*fallbackTx}}, block.MigrationOpt{},
	)
	require.NoError(t, err)
	require.NoError(t, shard1.AddBlock(ctx, duplicateFallback))
	creditedAgain, err := shard1.GetAccountStates(ctx, []account.Address{forward.Recipient})
	require.NoError(t, err)
	require.Equal(t, credited[0].Balance, creditedAgain[0].Balance)

	completion := transaction.NewFallbackCompletedTransaction(pendingFallbacks[0].Key(), time.Now())
	completionBlock, err := shard0.GenerateBlock(
		ctx, testMiner, block.TxBlockType,
		block.Body{TxList: []transaction.Transaction{*completion}}, block.MigrationOpt{},
	)
	require.NoError(t, err)
	require.NoError(t, shard0.AddBlock(ctx, completionBlock))
	status, err := shard0.GetFallbackStatus(ctx, pendingFallbacks[0].Key())
	require.NoError(t, err)
	require.Equal(t, fallback.StatusCompleted, status)
	pendingFallbacks, err = shard0.GetPendingFallbacks(ctx)
	require.NoError(t, err)
	require.Empty(t, pendingFallbacks)
}

func TestSettlementRejectsUnconfirmedOrTamperedPackage(t *testing.T) {
	ctx := context.Background()
	chain := newSettlementTestChain(t, 0)
	forward := settlementIntent(0, 1, 10, 0x51, 0x61)
	reverse := settlementIntent(1, 0, 7, 0x52, 0x62)
	reserveIntent(t, chain, forward)
	proposal, err := (batch.Builder{}).Build(window.FrozenWindow{
		WindowID: 1,
		Cuts:     []model.ShardCut{{ShardID: 0}, {ShardID: 1}},
		Intents:  []intent.PaymentIntent{forward, reverse},
	}, merkle.Hash{})
	require.NoError(t, err)
	packages, err := batch.BuildSettlementPackages(proposal)
	require.NoError(t, err)
	pack := packages[0]
	tx := transaction.NewSettlementTransaction(pack, time.Now())
	_, err = chain.GenerateBlock(ctx, testMiner, block.TxBlockType, block.Body{TxList: []transaction.Transaction{*tx}}, block.MigrationOpt{})
	require.ErrorContains(t, err, "not confirmed")

	require.NoError(t, chain.ConfirmMatchRoot(ctx, proposal.Header))
	pack.ShardProof.Steps = append(pack.ShardProof.Steps, merkle.Step{Sibling: settlementHash(0xff)})
	tx = transaction.NewSettlementTransaction(pack, time.Now())
	_, err = chain.GenerateBlock(ctx, testMiner, block.TxBlockType, block.Body{TxList: []transaction.Transaction{*tx}}, block.MigrationOpt{})
	require.ErrorContains(t, err, "invalid settlement proof")
}

func TestSettlementAcceptsFullyUnmatchedDirection(t *testing.T) {
	ctx := context.Background()
	shard0 := newSettlementTestChain(t, 0)
	shard1 := newSettlementTestChain(t, 1)
	payment := settlementIntent(0, 1, 10, 0x71, 0x72)
	reserveIntent(t, shard0, payment)
	proposal, err := (batch.Builder{}).Build(window.FrozenWindow{
		WindowID: 1,
		Cuts:     []model.ShardCut{{ShardID: 0}, {ShardID: 1}},
		Intents:  []intent.PaymentIntent{payment},
	}, merkle.Hash{})
	require.NoError(t, err)
	packages, err := batch.BuildSettlementPackages(proposal)
	require.NoError(t, err)
	require.NoError(t, shard0.ConfirmMatchRoot(ctx, proposal.Header))
	require.NoError(t, shard1.ConfirmMatchRoot(ctx, proposal.Header))
	for _, pack := range packages {
		chain := shard0
		if pack.Settlement.ShardID == 1 {
			chain = shard1
		}
		tx := transaction.NewSettlementTransaction(pack, time.Now())
		settlementBlock, blockErr := chain.GenerateBlock(
			ctx, testMiner, block.TxBlockType,
			block.Body{TxList: []transaction.Transaction{*tx}}, block.MigrationOpt{},
		)
		require.NoError(t, blockErr)
		require.NoError(t, chain.AddBlock(ctx, settlementBlock))
	}

	pending, err := shard0.GetPendingFallbacks(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, payment.Amount, pending[0].Amount)
}

func newSettlementTestChain(t *testing.T, shardID int64) *Chain {
	t.Helper()
	cfg := getTestConfig()
	cfg.ChainID = 11
	cfg.BoltCfg.FilePathDir = t.TempDir()
	chain, err := NewChain(cfg, config.LocalParams{ShardID: shardID})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, chain.Close()) })

	return chain
}

func reserveIntent(t *testing.T, chain *Chain, payment intent.PaymentIntent) {
	t.Helper()
	block, err := chain.GenerateBlock(
		context.Background(), testMiner, block.TxBlockType,
		block.Body{TxList: []transaction.Transaction{*transaction.NewIntentTransaction(payment, time.Now())}},
		block.MigrationOpt{},
	)
	require.NoError(t, err)
	require.NoError(t, chain.AddBlock(context.Background(), block))
}

func settlementIntent(source, destination, amount int64, senderTag, recipientTag byte) intent.PaymentIntent {
	var sender, recipient account.Address
	sender[0] = senderTag
	recipient[0] = recipientTag

	return intent.PaymentIntent{
		Version: intent.CurrentVersion, ChainID: 11, Sender: sender, Recipient: recipient,
		SourceShard: source, DestinationShard: destination, AssetID: intent.NativeAssetID,
		Amount: big.NewInt(amount), ExpiryEpoch: 100,
	}
}

func settlementHash(value byte) merkle.Hash {
	var hash merkle.Hash
	hash[0] = value

	return hash
}
