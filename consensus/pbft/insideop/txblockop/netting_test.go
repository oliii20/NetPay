package txblockop

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
)

func TestLegacyDeliveryClassificationExcludesPaymentIntents(t *testing.T) {
	t.Parallel()

	var recipient account.Address
	recipient[0] = 1
	tx := *transaction.NewIntentTransaction(intent.PaymentIntent{
		Version:          intent.CurrentVersion,
		ChainID:          11,
		Recipient:        recipient,
		SourceShard:      0,
		DestinationShard: 1,
		Amount:           big.NewInt(10),
		ExpiryEpoch:      10,
	}, time.Now())

	relayInner, relay1, relay2 := (&RelayTxBlockOp{}).splitTxs(context.Background(), []transaction.Transaction{tx})
	require.Empty(t, relayInner)
	require.Empty(t, relay1)
	require.Empty(t, relay2)

	brokerInner, broker1, broker2, fallback1, fallback2 := (&BrokerTxBlockOp{}).splitTxs(
		context.Background(),
		[]transaction.Transaction{tx},
	)
	require.Empty(t, brokerInner)
	require.Empty(t, broker1)
	require.Empty(t, broker2)
	require.Empty(t, fallback1)
	require.Empty(t, fallback2)
}
