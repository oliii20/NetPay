package block

import (
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
)

func TestBlockRecordDistinguishesZeroValueRelayStages(t *testing.T) {
	var txs []transaction.Transaction
	for _, stage := range []uint{transaction.Relay1Tx, transaction.Relay2Tx} {
		for _, amount := range []int64{0, 7} {
			tx := *transaction.NewTransaction(account.Address{1}, account.Address{2}, big.NewInt(amount), big.NewInt(0), 1, time.Now())
			tx.RelayStage = stage
			tx.ROriginalHash = make([]byte, 32)
			txs = append(txs, tx)
		}
	}
	b := &Block{Body: Body{TxList: txs}}
	line, err := ConvertBlock2Line(b)
	require.NoError(t, err)
	require.Len(t, line, len(RecordTitle))
	values := make(map[string]string)
	for i, name := range RecordTitle {
		values[name] = line[i]
	}
	require.Equal(t, "2", values["Relay1TxCount"])
	require.Equal(t, "2", values["Relay2TxCount"])
	require.Equal(t, "1", values["ZeroValueRelay1TxCount"])
	require.Equal(t, "1", values["ZeroValueRelay2TxCount"])
}
