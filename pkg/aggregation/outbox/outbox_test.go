package outbox_test

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

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/outbox"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
)

func TestDebitIsAtomicAndReplayProtected(t *testing.T) {
	t.Parallel()

	stateDB := newStateDB(t)
	var sender, recipient account.Address
	sender[0], recipient[0] = 1, 2
	stateDB.SetBalance(common.Address(sender), uint256.NewInt(100), tracing.BalanceChangeUnspecified)
	payment := paymentmodel.CrossShardPayment{
		Version: paymentmodel.CurrentVersion, ChainID: 11, Sender: sender, Recipient: recipient,
		SourceShard: 0, DestinationShard: 1, Amount: big.NewInt(30), ExpiryEpoch: 10,
	}
	box := outbox.New(stateDB)
	record, err := box.Debit(payment, paymentmodel.ValidationContext{ChainID: 11, ShardID: 0, ShardCount: 2, CurrentEpoch: 1})
	require.NoError(t, err)
	require.Equal(t, outbox.StatusDebited, record.Status)
	require.Equal(t, int64(30), record.Amount.Int64())
	require.Equal(t, uint64(70), stateDB.GetBalance(common.Address(sender)).Uint64())
	require.Equal(t, uint64(1), box.NextNonce(sender))

	_, err = box.Debit(payment, paymentmodel.ValidationContext{ChainID: 11, ShardID: 0, ShardCount: 2, CurrentEpoch: 1})
	require.ErrorIs(t, err, outbox.ErrPaymentExists)
	require.Equal(t, uint64(70), stateDB.GetBalance(common.Address(sender)).Uint64())
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
