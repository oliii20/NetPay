package nettingstats_test

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/supervisor/measure/nettingstats"
)

func TestCollectorMergesOutOfOrderEventsAndWritesStableCSVs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	collector := nettingstats.New(dir)
	batchID := merkle.Hash{1}
	var intentID intent.ID
	intentID[0] = 2
	createdAt := time.Unix(10, 0)
	reservedAt := time.Unix(12, 0)
	settledAt := time.Unix(20, 0)
	fallbackAt := time.Unix(25, 0)

	updates := []any{
		&message.NettingExecutionMetricMsg{NodeID: 0, Metrics: []model.NettingExecutionMetric{{
			Phase: model.MetricPhaseFallback, BatchID: batchID, WindowID: 1, IntentID: intentID,
			CommittedAt: fallbackAt, Final: true, StateReadCount: 2, StateWriteCount: 2,
		}}},
		&message.NettingBatchMetricMsg{NodeID: 0, Metric: model.NettingBatchMetric{
			BatchID: batchID, WindowID: 1, CloseReason: "batch_size", IntentCount: 1,
			Shards:             []model.ShardWindowMetric{{ShardID: 0, BlockCount: 2, CutHeight: 4}},
			MatchedIntentCount: 1, FallbackIntentCount: 1,
			OriginalValue: "10", MatchedValue: "7", FallbackValue: "3",
		}},
		&message.NettingExecutionMetricMsg{NodeID: 0, Metrics: []model.NettingExecutionMetric{{
			Phase: model.MetricPhaseReservation, IntentID: intentID,
			CreatedAt: createdAt, CommittedAt: reservedAt,
		}}},
		&message.NettingExecutionMetricMsg{NodeID: 0, Metrics: []model.NettingExecutionMetric{{
			Phase: model.MetricPhaseSettlement, BatchID: batchID, WindowID: 1, IntentID: intentID,
			CommittedAt: settledAt, StateReadCount: 5, StateWriteCount: 6,
			ProofVerificationTime: 3 * time.Millisecond,
		}}},
		&message.NettingBeaconMetricMsg{NodeID: 0, Metric: model.NettingBeaconMetric{
			BatchID: batchID, WindowID: 1, ValidationTime: time.Millisecond,
			BeaconConsensusLatency: 2 * time.Millisecond, PreprepareBytes: 100,
		}},
	}
	for _, update := range updates {
		wrapped, err := message.WrapMsg(update)
		require.NoError(t, err)
		require.NoError(t, collector.UpdateMeasureRecord(wrapped))
	}
	duplicate, err := message.WrapMsg(updates[3])
	require.NoError(t, err)
	require.NoError(t, collector.UpdateMeasureRecord(duplicate))
	require.NoError(t, collector.OutputResultAndClose())

	batchRows := readCSV(t, filepath.Join(dir, nettingstats.BatchMetricsFile))
	require.Len(t, batchRows, 2)
	require.Equal(t, "0.700000", csvValue(batchRows, "MatchedValueRatio"))
	require.Equal(t, "7", csvValue(batchRows, "StateReadCount"))
	require.Equal(t, "8", csvValue(batchRows, "StateWriteCount"))
	require.Equal(t, "3000000", csvValue(batchRows, "ProofVerificationTimeNs"))
	require.Equal(t, "15000000000", csvValue(batchRows, "EndToEndLatencyNs"))

	intentRows := readCSV(t, filepath.Join(dir, nettingstats.IntentMetricsFile))
	require.Len(t, intentRows, 2)
	require.Equal(t, "2000000000", csvValue(intentRows, "ReservationLatencyNs"))
	require.Equal(t, "15000000000", csvValue(intentRows, "EndToEndLatencyNs"))
	require.Equal(t, "true", csvValue(intentRows, "UsedFallback"))
}

func readCSV(t *testing.T, path string) [][]string {
	t.Helper()
	file, err := os.Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()
	rows, err := csv.NewReader(file).ReadAll()
	require.NoError(t, err)

	return rows
}

func csvValue(rows [][]string, column string) string {
	for idx, header := range rows[0] {
		if header == column {
			return rows[1][idx]
		}
	}

	return ""
}
