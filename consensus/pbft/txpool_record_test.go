package pbft

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/config"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/txpool"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/csvwrite"
)

func TestRecordTxPool(t *testing.T) {
	t.Parallel()

	pool, err := txpool.NewTxPool(config.TxPoolCfg{Type: config.TxPoolNumType})
	require.NoError(t, err)
	require.NoError(t, pool.AddTxs(make([]transaction.Transaction, 3)))

	path := filepath.Join(t.TempDir(), "txpool_record.csv")
	writer, err := csvwrite.NewCSVSeqWriter(path, txPoolRecordTitle)
	require.NoError(t, err)
	start := time.Date(2026, time.October, 15, 1, 2, 3, 0, time.UTC)
	node := &Node{txPool: pool, txPoolCSW: writer, txPoolStart: start}
	require.NoError(t, node.recordTxPool(start.Add(500*time.Millisecond)))
	require.NoError(t, writer.Close())

	fp, err := os.Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, fp.Close()) }()
	records, err := csv.NewReader(fp).ReadAll()
	require.NoError(t, err)
	require.Equal(t, [][]string{
		txPoolRecordTitle,
		{"2026-10-15T01:02:03.5Z", "500000000", "3"},
	}, records)
}
