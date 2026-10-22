package csvsource

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const testdataPath = "testdata.csv"
const excludeContractTxs = true

func TestCSVSource_ReadTxs(t *testing.T) {
	cs, err := NewCSVSource(testdataPath, excludeContractTxs)
	require.NoError(t, err)

	txs, err := cs.ReadTxs(10)
	require.NoError(t, err)
	// only 5 data in this file
	require.Len(t, txs, 5)

	txs, err = cs.ReadTxs(10)
	require.Len(t, txs, 0)
}

func TestLine2TxFiltersCalldataWhenContractsAreExcluded(t *testing.T) {
	line := make([]string, 21)
	line[3] = "0x32be343b94f860124dc4fee278fdcbd38c102d88"
	line[4] = "0x104994f45d9d697ca104e5704a7b77d7fec3537c"
	line[5] = "None"
	line[6], line[7] = "0", "0"
	line[8] = "1"
	line[12] = "0x12345678"

	_, err := line2Tx(line, 1, true)
	require.ErrorContains(t, err, "contract transactions have been filtered")

	tx, err := line2Tx(line, 1, false)
	require.NoError(t, err)
	require.NotEmpty(t, tx.Data)
}
