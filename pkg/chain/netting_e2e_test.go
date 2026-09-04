package chain

import (
	"context"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/config"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/beacon"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/fallback"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/receipt"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/registry"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/window"
)

func TestNettingPipelineAcrossFourShards(t *testing.T) {
	ctx := context.Background()
	chains := makeNettingE2EChains(t, 4)
	payments := []intent.PaymentIntent{
		nettingE2EIntent(0, 1, 10, 0x11, 0x21),
		nettingE2EIntent(0, 1, 4, 0x12, 0x22),
		nettingE2EIntent(1, 0, 7, 0x13, 0x23),
		nettingE2EIntent(2, 3, 5, 0x14, 0x24),
		nettingE2EIntent(3, 2, 5, 0x15, 0x25),
	}

	checkpoints := make(map[int64]model.ShardCheckpoint, len(chains))
	for shardID, chain := range chains {
		header := chain.GetCurHeader()
		hash, err := header.Hash()
		require.NoError(t, err)
		checkpoints[int64(shardID)] = model.ShardCheckpoint{
			Height: header.Number, BlockHash: nettingE2EHash(hash),
		}
	}
	manager, err := window.New(window.Config{
		ShardCount: 4, BatchSize: len(payments), MaxWindowDuration: time.Minute,
	}, nil, checkpoints)
	require.NoError(t, err)

	receipts := make([]model.FinalizedBlockReceipt, len(chains))
	for shardID, chain := range chains {
		var txs []transaction.Transaction
		for _, payment := range payments {
			if payment.SourceShard == int64(shardID) {
				txs = append(txs, *transaction.NewIntentTransaction(payment, time.Unix(1, 0)))
			}
		}
		reservationBlock := nettingE2EAddBlock(t, chain, txs)
		receipts[shardID], err = receipt.Build(int64(shardID), 1, reservationBlock, time.Unix(2, 0))
		require.NoError(t, err)
	}

	var frozen *window.FrozenWindow
	for _, shardID := range []int{3, 0, 2, 1} {
		candidate, addErr := manager.AddReceipt(receipts[shardID])
		require.NoError(t, addErr)
		if candidate != nil {
			frozen = candidate
		}
	}
	require.NotNil(t, frozen)
	require.Equal(t, "batch_size", frozen.CloseReason)
	require.Len(t, frozen.Intents, len(payments))

	proposal, err := (batch.Builder{}).Build(*frozen, merkle.Hash{})
	require.NoError(t, err)
	beaconStore, err := beacon.OpenStore(filepath.Join(t.TempDir(), "beacon.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, beaconStore.Close()) })
	require.NoError(t, beacon.NewValidator(beaconStore).Validate(proposal))
	_, err = beaconStore.Commit(proposal, time.Unix(3, 0))
	require.NoError(t, err)
	_, err = beaconStore.Commit(proposal, time.Unix(4, 0))
	require.ErrorIs(t, err, beacon.ErrBatchCommitted)

	packages, err := batch.BuildSettlementPackages(proposal)
	require.NoError(t, err)
	require.Len(t, packages, 4)
	for _, chain := range chains {
		require.NoError(t, chain.ConfirmMatchRoot(ctx, proposal.Header))
	}
	for _, pack := range packages {
		nettingE2EAddBlock(t, chains[pack.Settlement.ShardID], []transaction.Transaction{
			*transaction.NewSettlementTransaction(pack, time.Unix(5, 0)),
		})
	}

	var fallbackTotal big.Int
	for _, chain := range chains {
		pending, pendingErr := chain.GetPendingFallbacks(ctx)
		require.NoError(t, pendingErr)
		for _, item := range pending {
			fallbackTotal.Add(&fallbackTotal, item.Amount)
			destination := chains[item.DestinationShard]
			nettingE2EAddBlock(t, destination, []transaction.Transaction{
				*transaction.NewReservedFallbackTransaction(item, time.Unix(6, 0)),
			})
			nettingE2EAddBlock(t, chain, []transaction.Transaction{
				*transaction.NewFallbackCompletedTransaction(item.Key(), time.Unix(7, 0)),
			})
			status, statusErr := chain.GetFallbackStatus(ctx, item.Key())
			require.NoError(t, statusErr)
			require.Equal(t, fallback.StatusCompleted, status)
		}
	}
	require.Equal(t, big.NewInt(7), &fallbackTotal)

	initial, ok := new(big.Int).SetString(account.NormalInitBalanceStr, 10)
	require.True(t, ok)
	expectedTotal := new(big.Int).Mul(initial, big.NewInt(int64(len(payments)*2)))
	actualTotal := new(big.Int)
	for _, payment := range payments {
		senderState, stateErr := chains[payment.SourceShard].GetAccountStates(ctx, []account.Address{payment.Sender})
		require.NoError(t, stateErr)
		recipientState, stateErr := chains[payment.DestinationShard].GetAccountStates(
			ctx, []account.Address{payment.Recipient},
		)
		require.NoError(t, stateErr)
		actualTotal.Add(actualTotal, senderState[0].Balance)
		actualTotal.Add(actualTotal, recipientState[0].Balance)
		reservation, reservationErr := chains[payment.SourceShard].GetReservation(ctx, payment)
		require.NoError(t, reservationErr)
		require.Equal(t, registry.ReservationConsumed, reservation.Status)
	}
	require.Equal(t, expectedTotal, actualTotal)
}

func makeNettingE2EChains(t *testing.T, count int) []*Chain {
	t.Helper()
	chains := make([]*Chain, count)
	storageRoot := t.TempDir()
	for shardID := range count {
		cfg := getTestConfig()
		cfg.ChainID = 11
		cfg.BoltCfg.FilePathDir = filepath.Join(storageRoot, "blocks")
		chain, err := NewChain(cfg, config.LocalParams{ShardID: int64(shardID)})
		require.NoError(t, err)
		chains[shardID] = chain
	}
	t.Cleanup(func() {
		for _, chain := range chains {
			require.NoError(t, chain.Close())
		}
	})

	return chains
}

func nettingE2EAddBlock(t *testing.T, chain *Chain, txs []transaction.Transaction) *block.Block {
	t.Helper()
	generated, err := chain.GenerateBlock(
		context.Background(), testMiner, block.TxBlockType, block.Body{TxList: txs}, block.MigrationOpt{},
	)
	require.NoError(t, err)
	require.NoError(t, chain.ValidateBlock(context.Background(), generated))
	require.NoError(t, chain.AddBlock(context.Background(), generated))

	return generated
}

func nettingE2EIntent(source, destination, amount int64, senderTag, recipientTag byte) intent.PaymentIntent {
	var sender, recipient account.Address
	sender[0] = senderTag
	recipient[0] = recipientTag

	return intent.PaymentIntent{
		Version: intent.CurrentVersion, ChainID: 11, Sender: sender, Recipient: recipient,
		SourceShard: source, DestinationShard: destination, AssetID: intent.NativeAssetID,
		Amount: big.NewInt(amount), ExpiryEpoch: 100,
	}
}

func nettingE2EHash(value []byte) merkle.Hash {
	var hash merkle.Hash
	copy(hash[:], value)

	return hash
}
