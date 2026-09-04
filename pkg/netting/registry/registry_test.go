package registry_test

import (
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/config"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/registry"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/storage/vmstate"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/vm"
)

func TestSystemAddressesAreStable(t *testing.T) {
	t.Parallel()

	require.Equal(t, "0f0af4217f70d69e5eb75bf8bf4951dcef965861", hex.EncodeToString(registry.EscrowAccountAddress[:]))
	require.Equal(t, "f8c3b9fdaf39f84a77a0888f502d39ddd326324c", hex.EncodeToString(registry.IntentRegistryAddress[:]))
}

func TestReserveMovesFundsAndPersistsMetadata(t *testing.T) {
	t.Parallel()

	stateDB := newStateDB(t)
	payment := testIntent(0, 40)
	stateDB.SetBalance(common.Address(payment.Sender), uint256.NewInt(100), tracing.BalanceChangeUnspecified)
	store := registry.New(stateDB)

	reservation, err := store.Reserve(payment, intent.ValidationContext{
		ChainID: 11, ShardID: 0, ShardCount: 2, CurrentEpoch: 1,
	})
	require.NoError(t, err)
	require.Equal(t, registry.ReservationReserved, reservation.Status)
	require.Equal(t, int64(40), reservation.Amount.Int64())
	require.Equal(t, uint64(1), store.NextNonce(payment.Sender))
	require.Equal(t, uint64(60), stateDB.GetBalance(common.Address(payment.Sender)).Uint64())
	require.Equal(t, uint64(40), store.EscrowBalance().Uint64())

	loaded, err := store.Get(payment)
	require.NoError(t, err)
	require.Equal(t, reservation, loaded)
}

func TestReserveRejectsWithoutMutatingState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*intent.PaymentIntent)
		want   error
	}{
		{
			name: "wrong nonce",
			mutate: func(payment *intent.PaymentIntent) {
				payment.Nonce = 1
			},
			want: registry.ErrNonceMismatch,
		},
		{name: "expired", mutate: func(payment *intent.PaymentIntent) { payment.ExpiryEpoch = 1 }, want: intent.ErrExpired},
		{
			name: "insufficient balance",
			mutate: func(payment *intent.PaymentIntent) {
				payment.Amount.SetInt64(101)
			},
			want: registry.ErrInsufficientBalance,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			stateDB := newStateDB(t)
			payment := testIntent(0, 40)
			tc.mutate(&payment)
			stateDB.SetBalance(common.Address(payment.Sender), uint256.NewInt(100), tracing.BalanceChangeUnspecified)
			store := registry.New(stateDB)

			_, err := store.Reserve(payment, intent.ValidationContext{
				ChainID: 11, ShardID: 0, ShardCount: 2, CurrentEpoch: 1,
			})
			require.ErrorIs(t, err, tc.want)
			require.Equal(t, uint64(100), stateDB.GetBalance(common.Address(payment.Sender)).Uint64())
			require.True(t, store.EscrowBalance().IsZero())
			require.Zero(t, store.NextNonce(payment.Sender))
		})
	}
}

func TestReserveRejectsDuplicateIntent(t *testing.T) {
	t.Parallel()

	stateDB := newStateDB(t)
	payment := testIntent(0, 10)
	stateDB.SetBalance(common.Address(payment.Sender), uint256.NewInt(100), tracing.BalanceChangeUnspecified)
	store := registry.New(stateDB)
	ctx := intent.ValidationContext{ChainID: 11, ShardID: 0, ShardCount: 2, CurrentEpoch: 1}

	_, err := store.Reserve(payment, ctx)
	require.NoError(t, err)
	_, err = store.Reserve(payment, ctx)
	require.ErrorIs(t, err, registry.ErrReservationExists)
	require.Equal(t, uint64(90), stateDB.GetBalance(common.Address(payment.Sender)).Uint64())
	require.Equal(t, uint64(10), store.EscrowBalance().Uint64())
}

func newStateDB(t *testing.T) *state.StateDB {
	t.Helper()
	store, err := vmstate.NewStateStore(
		config.StorageCfg{EthStorageCfg: config.EthStorageCfg{IsMemoryDB: true}},
		config.LocalParams{},
	)
	require.NoError(t, err)
	executor, err := vm.NewExecutor(store, types.EmptyRootHash, params.MainnetChainConfig)
	require.NoError(t, err)

	return executor.StateDB()
}

func testIntent(nonce uint64, amount int64) intent.PaymentIntent {
	var sender, recipient account.Address
	sender[0] = 0x11
	recipient[0] = 0x22

	return intent.PaymentIntent{
		Version:          intent.CurrentVersion,
		ChainID:          11,
		Sender:           sender,
		Recipient:        recipient,
		SourceShard:      0,
		DestinationShard: 1,
		AssetID:          intent.NativeAssetID,
		Amount:           big.NewInt(amount),
		Nonce:            nonce,
		ExpiryEpoch:      10,
	}
}
