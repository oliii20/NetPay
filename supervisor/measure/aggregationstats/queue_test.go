package aggregationstats

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
)

func TestQueueSamplesSortedAndDeduplicated(t *testing.T) {
	dir := t.TempDir()
	collector := New(dir)
	for _, second := range []int64{2, 1, 2} {
		update(t, collector, &message.AggregationQueueMetricMsg{Metric: model.AggregationQueueMetric{
			ObservedAt: time.Unix(second, 0), QueuedOriginals: 123,
			FrozenQueueWait: 500 * time.Millisecond, PolicyVersion: 1,
		}})
	}
	require.NoError(t, collector.OutputResultAndClose())
	file, err := os.Open(filepath.Join(dir, QueueMetricsFile))
	require.NoError(t, err)
	defer file.Close()
	rows, err := csv.NewReader(file).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Equal(t, "1000000000", rows[1][0])
	require.Equal(t, "123", rows[1][4])
	require.Equal(t, "500000000", rows[1][5])
	require.Equal(t, "2000000000", rows[2][0])
}
