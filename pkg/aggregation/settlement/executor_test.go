package settlement_test

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/rawdb"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/triedb"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/settlement"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/window"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
)

func TestExecutorCreditsAggregatedAmountExactlyOnce(t *testing.T) {
	t.Parallel()

	recipient := testAddress(0x44)
	paymentA := testPayment(testAddress(1), recipient, 6, 0)
	paymentB := testPayment(testAddress(2), recipient, 9, 0)
	receipt := model.FinalizedBlockReceipt{
		ShardID: 0, Height: 1, ParentHash: testHash(1), BlockHash: testHash(2),
		Payments: []paymentmodel.CrossShardPayment{paymentA, paymentB},
	}
	proposal, err := (batch.Builder{}).Build(window.FrozenWindow{
		WindowID: 1,
		Cuts: []model.ShardCut{
			{ShardID: 0, EndHeight: 1, EndBlockHash: receipt.BlockHash},
			{ShardID: 1, EndBlockHash: testHash(3)},
		},
		Receipts: []model.FinalizedBlockReceipt{receipt},
		Payments: []paymentmodel.CrossShardPayment{paymentA, paymentB},
	}, merkle.Hash{})
	require.NoError(t, err)
	packages, err := batch.BuildAggregationPackages(proposal)
	require.NoError(t, err)
	require.Len(t, packages, 1)
	require.Len(t, packages[0].Aggregation.Credits, 1)

	stateDB := newStateDB(t)
	stateDB.SetBalance(common.Address(recipient), uint256.NewInt(100), tracing.BalanceChangeUnspecified)
	executor := settlement.Executor{}
	require.NoError(t, executor.Execute(stateDB, 1, packages[0], proposal.Header))
	require.Equal(t, uint64(115), stateDB.GetBalance(common.Address(recipient)).Uint64())
	require.True(t, settlement.IsExecuted(stateDB, proposal.Header.BatchID, 1, 0))

	// Re-delivery is expected in distributed systems and must be idempotent.
	require.NoError(t, executor.Execute(stateDB, 1, packages[0], proposal.Header))
	require.Equal(t, uint64(115), stateDB.GetBalance(common.Address(recipient)).Uint64())
}

func TestExecutorRejectsTamperedCredit(t *testing.T) {
	t.Parallel()

	recipient := testAddress(0x55)
	payment := testPayment(testAddress(1), recipient, 4, 0)
	receipt := model.FinalizedBlockReceipt{ShardID: 0, Height: 1, BlockHash: testHash(2), Payments: []paymentmodel.CrossShardPayment{payment}}
	proposal, err := (batch.Builder{}).Build(window.FrozenWindow{
		WindowID: 1,
		Cuts:     []model.ShardCut{{ShardID: 0, EndHeight: 1, EndBlockHash: receipt.BlockHash}, {ShardID: 1}},
		Receipts: []model.FinalizedBlockReceipt{receipt}, Payments: []paymentmodel.CrossShardPayment{payment},
	}, merkle.Hash{})
	require.NoError(t, err)
	packages, err := batch.BuildAggregationPackages(proposal)
	require.NoError(t, err)
	packages[0].Aggregation.Credits[0].Amount.Add(packages[0].Aggregation.Credits[0].Amount, big.NewInt(1))

	err = (settlement.Executor{}).Execute(newStateDB(t), 1, packages[0], proposal.Header)
	require.ErrorIs(t, err, settlement.ErrInvalidProof)
}

func newStateDB(t *testing.T) *state.StateDB {
	t.Helper()
	disk := rawdb.NewMemoryDatabase()
	trieDB := triedb.NewDatabase(rawdb.NewDatabase(disk), nil)
	database := state.NewDatabase(trieDB, nil)
	stateDB, err := state.New(types.EmptyRootHash, database)
	require.NoError(t, err)
	return stateDB
}

func testPayment(sender, recipient account.Address, amount int64, nonce uint64) paymentmodel.CrossShardPayment {
	return paymentmodel.CrossShardPayment{
		Version: paymentmodel.CurrentVersion, ChainID: 1, Sender: sender, Recipient: recipient,
		SourceShard: 0, DestinationShard: 1, Amount: big.NewInt(amount), Nonce: nonce,
		ExpiryEpoch: 100,
	}
}

func testAddress(value byte) account.Address {
	var result account.Address
	result[0] = value
	return result
}

func testHash(value byte) merkle.Hash {
	var result merkle.Hash
	result[0] = value
	return result
}

func TestBatchOnlyPreservesIndividualCredits(t *testing.T) {
	for _, batchOnly := range []bool{false, true} {
		name := "full_aggregation"
		if batchOnly {
			name = "batch_only"
		}
		t.Run(name, func(t *testing.T) {
			recipient := testAddress(0x44)
			payments := []paymentmodel.CrossShardPayment{
				testPayment(testAddress(1), recipient, 6, 0),
				testPayment(testAddress(2), recipient, 9, 0),
			}
			receipt := model.FinalizedBlockReceipt{ShardID: 0, Height: 1, BlockHash: testHash(2), Payments: payments}
			proposal, err := (batch.Builder{BatchOnly: batchOnly}).Build(window.FrozenWindow{
				WindowID: 1, Cuts: []model.ShardCut{{ShardID: 0, EndHeight: 1, EndBlockHash: receipt.BlockHash}, {ShardID: 1}},
				Receipts: []model.FinalizedBlockReceipt{receipt}, Payments: payments,
			}, merkle.Hash{})
			require.NoError(t, err)
			require.NoError(t, batch.VerifyBatch(proposal))
			packs, err := batch.BuildAggregationPackages(proposal)
			require.NoError(t, err)
			require.Len(t, packs, 1)
			expectedCredits := 1
			if batchOnly {
				expectedCredits = 2
			}
			require.Len(t, packs[0].Aggregation.Credits, expectedCredits)
			db := newStateDB(t)
			db.SetBalance(common.Address(recipient), uint256.NewInt(100), tracing.BalanceChangeUnspecified)
			executor := settlement.Executor{}
			require.NoError(t, executor.Execute(db, 1, packs[0], proposal.Header))
			require.Equal(t, uint64(115), db.GetBalance(common.Address(recipient)).Uint64())
			require.NoError(t, executor.Execute(db, 1, packs[0], proposal.Header))
			require.Equal(t, uint64(115), db.GetBalance(common.Address(recipient)).Uint64())
			packs[0].Aggregation.BatchOnly = !batchOnly
			// Fresh state ensures the replay guard cannot hide a tampered proof.
			require.Error(t, executor.Execute(newStateDB(t), 1, packs[0], proposal.Header))
		})
	}
}
