package txsource

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/config"
)

func TestPositiveValueWorkloadCountsOnlyAcceptedPayments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payments.csv")
	file, err := os.Create(path)
	require.NoError(t, err)
	writer := csv.NewWriter(file)
	require.NoError(t, writer.Write(make([]string, 22)))
	for _, value := range []string{"0", "1", "-2", "0", "2"} {
		row := make([]string, 22)
		row[3], row[4] = "0x32be343b94f860124dc4fee278fdcbd38c102d88", "0x104994f45d9d697ca104e5704a7b77d7fec3537c"
		row[5], row[6], row[7], row[8], row[12] = "None", "0", "0", value, "0x"
		require.NoError(t, writer.Write(row))
	}
	writer.Flush()
	require.NoError(t, writer.Error())
	require.NoError(t, file.Close())
	var cfg config.TxSourceCfg
	require.NoError(t, json.Unmarshal([]byte(`{"tx_source_type":"csv_source","exclude_contract_txs":true,"positive_value_only":true}`), &cfg))
	cfg.TxSourceFile = path
	source, err := NewTxSource(cfg)
	require.NoError(t, err)
	txs, err := source.ReadTxs(2)
	require.NoError(t, err)
	require.Len(t, txs, 2)
	require.Equal(t, "1", txs[0].Value.String())
	require.Equal(t, "2", txs[1].Value.String())
	require.Equal(t, uint64(1), txs[0].Nonce)
	require.Equal(t, uint64(2), txs[1].Nonce)
	txs, err = source.ReadTxs(1)
	require.NoError(t, err)
	require.Empty(t, txs)
	// Generic CSV users retain their previous semantics unless explicitly opting
	// into the payment-workload filter.
	cfg.PositiveValueOnly = false
	source, err = NewTxSource(cfg)
	require.NoError(t, err)
	txs, err = source.ReadTxs(10)
	require.NoError(t, err)
	require.Len(t, txs, 5)
	require.Zero(t, txs[0].Value.Sign())
}
