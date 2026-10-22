package aggregationstats

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
)

func TestCollectorBuildsCrossShardLifecycle(t *testing.T) {
	dir := t.TempDir()
	collector := New(dir)
	var paymentID paymentmodel.ID
	paymentID[0] = 1
	var batchID merkle.Hash
	batchID[0] = 2
	createdAt := time.Unix(100, 0)
	committedAt := createdAt.Add(3 * time.Second)

	update(t, collector, &message.AggregationExecutionMetricMsg{Metrics: []model.AggregationExecutionMetric{
		{Phase: model.MetricPhaseSourceDebit, PaymentID: paymentID, CreatedAt: createdAt, CommittedAt: createdAt.Add(time.Second)},
	}})
	update(t, collector, &message.AggregationFinalizedMsg{
		Header: model.AggregationBatchHeader{BatchID: batchID}, PaymentIDs: []paymentmodel.ID{paymentID},
	})
	update(t, collector, &message.AggregationAppliedMsg{Chunks: []message.AggregationApplied{
		{BatchID: batchID, ShardID: 1, ChunkIndex: 0, ReceiptCount: 1, CommittedAt: committedAt},
	}})
	require.NoError(t, collector.OutputResultAndClose())

	file, err := os.Open(filepath.Join(dir, LifecycleMetricsFile))
	require.NoError(t, err)
	defer file.Close()
	records, err := csv.NewReader(file).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 2)
	require.Equal(t, "3000000000", records[1][4])
}

func update(t *testing.T, collector *Collector, payload any) {
	t.Helper()
	wrapper, err := message.WrapMsg(payload)
	require.NoError(t, err)
	require.NoError(t, collector.UpdateMeasureRecord(wrapper))
}

func TestCollectorUsesActualChunkCommitTimes(t *testing.T) {
	t.Run("out of order chunks and finalized notification", func(t *testing.T) {
		collector := New(t.TempDir())
		var batchID merkle.Hash
		batchID[0] = 1
		var a, b paymentmodel.ID
		a[0], b[0] = 2, 3
		early, late := time.Unix(100, 0), time.Unix(110, 0)
		update(t, collector, &message.AggregationAppliedMsg{Chunks: []message.AggregationApplied{
			{BatchID: batchID, ShardID: 1, ChunkIndex: 0, ReceiptCount: 1, CommittedAt: early},
		}})
		update(t, collector, &message.AggregationFinalizedMsg{
			Header: model.AggregationBatchHeader{BatchID: batchID}, PaymentIDs: []paymentmodel.ID{a, b},
			Settlements: []message.PaymentSettlement{{PaymentID: a, ShardID: 1}, {PaymentID: b, ShardID: 2}},
		})
		require.Equal(t, early, collector.completedAt[a])
		require.NotContains(t, collector.completedAt, b)
		update(t, collector, &message.AggregationAppliedMsg{Chunks: []message.AggregationApplied{
			{BatchID: batchID, ShardID: 2, ChunkIndex: 0, ReceiptCount: 1, CommittedAt: late},
		}})
		require.Equal(t, early, collector.completedAt[a])
		require.Equal(t, late, collector.completedAt[b])
	})
}

func TestCollectorExportsBeaconFinalizationTime(t *testing.T) {
	t.Run("batch timeline", func(t *testing.T) {
		collector := New(t.TempDir())
		var batchID merkle.Hash
		batchID[0] = 9
		finalized := time.Unix(100, 123)
		update(t, collector, &message.AggregationBeaconMetricMsg{Metric: model.AggregationBeaconMetric{
			BatchID: batchID, FinalizedAt: finalized,
		}})
		rows := collector.batchRows()
		require.Len(t, rows, 1)
		require.Len(t, rows[0], len(batchHeaders))
		columns := make(map[string]string)
		for i, name := range batchHeaders {
			columns[name] = rows[0][i]
		}
		require.Equal(t, "100000000123", columns["BeaconFinalizedAtUnixNano"])
		require.Equal(t, "0", columns["HeaderBroadcastTimeNs"])
	})
}
