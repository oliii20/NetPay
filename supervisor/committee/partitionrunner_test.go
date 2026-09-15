package committee

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPartitionRunnerRecordsCLPAMetrics(t *testing.T) {
	path := filepath.Join(t.TempDir(), clpaRepartitionMetricsPath)
	runner := partitionRunner{
		supervisorEpoch: 1,
		shardEpoch:      []int64{1, 1},
		clpaMetricsPath: path,
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	partitionEnd := start.Add(2 * time.Second)
	broadcastEnd := partitionEnd.Add(3 * time.Second)
	syncEnd := broadcastEnd.Add(5 * time.Second)

	require.NoError(t, runner.recordCLPARound(1, start, partitionEnd, broadcastEnd, 7))
	require.True(t, runner.checkEpochSyncAndMarkAt(syncEnd))

	fp, err := os.Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, fp.Close()) }()

	rows, err := csv.NewReader(fp).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, clpaRepartitionMetricsHeader, rows[0])
	require.Equal(t, []string{
		"1",
		"2026-01-01T00:00:00Z",
		"2026-01-01T00:00:02Z",
		"2026-01-01T00:00:05Z",
		"2026-01-01T00:00:10Z",
		"7",
		"2000000000",
		"3000000000",
		"5000000000",
		"10000000000",
	}, rows[1])
}
