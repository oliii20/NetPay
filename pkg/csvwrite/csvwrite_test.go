package csvwrite

import (
	"encoding/csv"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	testCSVAllPath = "./test_all_write.csv"
	testCSVSeqPath = "./test_seq_write.csv"
)

var (
	testHeader = []string{"h1", "h2", "h3"}
	testLines  = [][]string{
		{"1", "2", "3"},
		{"4", "5", "6"},
	}
)

func TestWriteAllToCSV(t *testing.T) {
	_ = os.RemoveAll(testCSVAllPath)

	t.Cleanup(func() { _ = os.RemoveAll(testCSVAllPath) })

	err := WriteAllToCSV(testCSVAllPath, testHeader, testLines)
	require.NoError(t, err)
}

func TestWriteAllToCSVReplaceOverwritesExistingFile(t *testing.T) {
	path := t.TempDir() + "/replace.csv"

	require.NoError(t, WriteAllToCSV(path, testHeader, testLines))
	require.NoError(t, WriteAllToCSVReplace(path, testHeader, [][]string{{"7", "8", "9"}}))

	file, err := os.Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()
	rows, err := csv.NewReader(file).ReadAll()
	require.NoError(t, err)
	require.Equal(t, [][]string{testHeader, []string{"7", "8", "9"}}, rows)
}

func TestCSVSeqWriter(t *testing.T) {
	_ = os.RemoveAll(testCSVSeqPath)

	t.Cleanup(func() { _ = os.RemoveAll(testCSVSeqPath) })

	cs, err := NewCSVSeqWriter(testCSVSeqPath, testHeader)
	require.NoError(t, err)

	err = cs.WriteLine2CSV(testLines[0])
	require.NoError(t, err)
	err = cs.WriteLine2CSV(testLines[1])
	require.NoError(t, err)

	require.NoError(t, cs.Close())
}
