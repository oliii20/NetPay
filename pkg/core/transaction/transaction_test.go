package transaction

import (
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
)

func TestTxTypeKeepsLegacyInference(t *testing.T) {
	t.Parallel()

	var recipient account.Address
	recipient[0] = 1

	tests := []struct {
		name string
		tx   Transaction
		want byte
	}{
		{name: "normal", tx: Transaction{}, want: NormalTxType},
		{name: "relay", tx: Transaction{RelayTxOpt: RelayTxOpt{ROriginalHash: []byte{1}}}, want: RelayTxType},
		{name: "broker", tx: Transaction{BrokerTxOpt: BrokerTxOpt{BOriginalHash: []byte{1}}}, want: BrokerTxType},
		{name: "create contract", tx: Transaction{Data: []byte{1}}, want: CreateContractTxType},
		{name: "call contract", tx: Transaction{Recipient: recipient, Data: []byte{1}}, want: CallContractTxType},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.tx.TxType())
		})
	}
}

func TestTxTypeIntentTakesPrecedence(t *testing.T) {
	t.Parallel()

	tx := Transaction{
		Data:        []byte{1},
		RelayTxOpt:  RelayTxOpt{ROriginalHash: []byte{1}},
		BrokerTxOpt: BrokerTxOpt{BOriginalHash: []byte{1}},
		IntentTxOpt: IntentTxOpt{Intent: transactionTestIntent([]byte("signature"))},
	}

	require.Equal(t, IntentSubmitTxType, tx.TxType())
	require.Equal(t, byte(5), IntentSubmitTxType)
	require.Equal(t, byte(6), SettlementTxType)
}

func TestIntentTransactionEncodingCommitsSignature(t *testing.T) {
	t.Parallel()

	createdAt := time.Unix(123, 456)
	left := NewTransaction(account.Address{}, account.Address{}, big.NewInt(1), big.NewInt(0), 0, createdAt)
	left.Intent = transactionTestIntent([]byte("signature-a"))

	right := *left
	right.Intent = transactionTestIntent([]byte("signature-b"))

	leftHash, err := left.Hash()
	require.NoError(t, err)
	rightHash, err := right.Hash()
	require.NoError(t, err)

	require.NotEqual(t, leftHash, rightHash)
}

func transactionTestIntent(signature []byte) *intent.PaymentIntent {
	var recipient account.Address
	recipient[0] = 1

	return &intent.PaymentIntent{
		Version:          intent.CurrentVersion,
		ChainID:          11,
		Recipient:        recipient,
		SourceShard:      0,
		DestinationShard: 1,
		AssetID:          intent.NativeAssetID,
		Amount:           big.NewInt(5),
		ExpiryEpoch:      2,
		Signature:        signature,
	}
}
