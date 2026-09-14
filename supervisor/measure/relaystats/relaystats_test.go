package relaystats

import (
	"encoding/csv"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/utils"
	"github.com/HuangLab-SYSU/block-emulator-x/supervisor/txsource/randomsource"
)

const (
	txSize = 10
)

func TestRelayStats_UpdateMeasureRecord(t *testing.T) {
	_ = os.RemoveAll("test_dir")
	r, err := NewRelayStats("test_dir/")

	t.Cleanup(func() {
		err = os.RemoveAll("test_dir")
		require.NoError(t, err)
	})

	require.NoError(t, err)
	err = r.UpdateMeasureRecord(initInputMsg(t))
	require.NoError(t, err)
	err = r.OutputResultAndClose()
	require.NoError(t, err)
}

func TestRelayStatsPreservesOriginalCreateTimeWhenRelay2CreateTimeChanges(t *testing.T) {
	dir := t.TempDir()
	r, err := NewRelayStats(dir)
	require.NoError(t, err)

	originalCreate := time.Date(2026, 9, 14, 10, 0, 0, 0, time.UTC)
	relay1Propose := originalCreate.Add(time.Second)
	relay1Commit := originalCreate.Add(2 * time.Second)
	relay2Create := originalCreate.Add(5 * time.Second)
	relay2Propose := originalCreate.Add(6 * time.Second)
	relay2Commit := originalCreate.Add(8 * time.Second)

	rawTx := transaction.NewTransaction(
		account.Address{1}, account.Address{2}, big.NewInt(1), big.NewInt(0), 1, originalCreate,
	)
	rawHash, err := rawTx.Hash()
	require.NoError(t, err)

	relay1Tx := *rawTx
	relay1Tx.RelayStage = transaction.Relay1Tx
	relay1Tx.ROriginalHash = rawHash

	relay2Tx := *rawTx
	relay2Tx.RelayStage = transaction.Relay2Tx
	relay2Tx.ROriginalHash = rawHash
	relay2Tx.CreateTime = relay2Create

	require.NoError(t, r.UpdateMeasureRecord(relayMsg(t, []transaction.Transaction{relay1Tx}, nil, relay1Propose, relay1Commit)))
	require.NoError(t, r.UpdateMeasureRecord(relayMsg(t, nil, []transaction.Transaction{relay2Tx}, relay2Propose, relay2Commit)))
	require.NoError(t, r.OutputResultAndClose())

	detail := readCSV(t, filepath.Join(dir, detailTxInfoPath))
	require.Len(t, detail, 1)
	require.Equal(t, utils.ConvertTime2Str(originalCreate), csvValue(detail, "Tx create time"))
	require.Equal(t, utils.ConvertTime2Str(relay2Create), csvValue(detail, "Relay2 tx create time"))
	require.Equal(t, utils.ConvertTime2Str(relay2Commit), csvValue(detail, "Tx finally commit time"))

	brief := readCSV(t, filepath.Join(dir, briefTxInfoPath))
	require.Len(t, brief, 1)
	require.Equal(t, "8000000000.00", csvValue(brief, "Avg. TCL of this epoch (nanosecond)"))
}

func initInputMsg(t *testing.T) *rpcserver.WrappedMsg {
	txSource := randomsource.NewRandomSource()
	innerShardTxs, _ := txSource.ReadTxs(txSize)
	cTxs, _ := txSource.ReadTxs(txSize)
	r1Txs, r2Txs := make([]transaction.Transaction, txSize), make([]transaction.Transaction, txSize)
	for i, cTx := range cTxs {
		txHash, err := cTx.Hash()
		require.NoError(t, err)
		r1 := transaction.NewTransaction(cTx.Sender, cTx.Recipient, cTx.Value, big.NewInt(0), cTx.Nonce, cTx.CreateTime)
		r1.ROriginalHash = txHash
		r1.RelayStage = 1

		r2 := transaction.NewTransaction(cTx.Sender, cTx.Recipient, cTx.Value, big.NewInt(0), cTx.Nonce, cTx.CreateTime)
		r2.ROriginalHash = txHash
		r2.RelayStage = 2

		r1Txs[i] = *r1
		r2Txs[i] = *r2
	}

	m := &message.RelayBlockInfoMsg{
		InnerShardTxs:    innerShardTxs,
		Relay1Txs:        r1Txs,
		Relay2Txs:        r2Txs,
		Epoch:            0,
		BlockProposeTime: time.Now(),
		BlockCommitTime:  time.Now().Add(time.Second),
	}

	msg, err := message.WrapMsg(m)
	require.NoError(t, err)
	return msg
}

func relayMsg(
	t *testing.T,
	relay1Txs []transaction.Transaction,
	relay2Txs []transaction.Transaction,
	proposeTime time.Time,
	commitTime time.Time,
) *rpcserver.WrappedMsg {
	t.Helper()

	m := &message.RelayBlockInfoMsg{
		Relay1Txs:        relay1Txs,
		Relay2Txs:        relay2Txs,
		Epoch:            0,
		BlockProposeTime: proposeTime,
		BlockCommitTime:  commitTime,
	}

	msg, err := message.WrapMsg(m)
	require.NoError(t, err)
	return msg
}

func readCSV(t *testing.T, path string) []map[string]string {
	t.Helper()

	fp, err := os.Open(path)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, fp.Close())
	}()

	rows, err := csv.NewReader(fp).ReadAll()
	require.NoError(t, err)
	require.NotEmpty(t, rows)

	result := make([]map[string]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		entry := make(map[string]string, len(rows[0]))
		for idx, key := range rows[0] {
			entry[key] = row[idx]
		}
		result = append(result, entry)
	}
	return result
}

func csvValue(rows []map[string]string, key string) string {
	if len(rows) == 0 {
		return ""
	}
	return rows[0][key]
}
