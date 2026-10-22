package receipt_test

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/receipt"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
)

func TestAggregatePaymentsGroupsByDestinationRecipientAndAsset(t *testing.T) {
	t.Parallel()

	recipient := testAddress(9)
	payments := []paymentmodel.CrossShardPayment{
		testPayment(testAddress(1), recipient, 1, 3),
		testPayment(testAddress(2), recipient, 1, 5),
		testPayment(testAddress(3), testAddress(8), 1, 7),
		testPayment(testAddress(4), recipient, 2, 11),
	}

	abstracts, err := receipt.AggregatePayments(payments)
	require.NoError(t, err)
	require.Len(t, abstracts, 3)

	var grouped paymentmodel.CrossShardPayment
	for _, abstract := range abstracts {
		if abstract.DestinationShard == 1 && abstract.Recipient == recipient {
			grouped = abstract
		}
	}
	require.True(t, grouped.SourceAggregated)
	require.Equal(t, int64(8), grouped.Amount.Int64())
	require.Len(t, grouped.OriginalIDs, 2)
	require.Equal(t, account.Address{}, grouped.Sender)
	require.Empty(t, grouped.Signature)

	logicalIDs, err := grouped.LogicalIDs()
	require.NoError(t, err)
	require.Equal(t, grouped.OriginalIDs, logicalIDs)
}

func testPayment(sender, recipient account.Address, destination, amount int64) paymentmodel.CrossShardPayment {
	return paymentmodel.CrossShardPayment{
		Version: paymentmodel.CurrentVersion, ChainID: 1, Sender: sender, Recipient: recipient,
		SourceShard: 0, DestinationShard: destination, AssetID: paymentmodel.NativeAssetID,
		Amount: big.NewInt(amount), Nonce: uint64(sender[0]), ExpiryEpoch: 100,
	}
}

func testAddress(value byte) account.Address {
	var result account.Address
	result[0] = value
	return result
}
