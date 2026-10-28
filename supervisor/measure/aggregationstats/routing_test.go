package aggregationstats

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/routing"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
)

func TestRecoveryVolumeEvidenceExport(t *testing.T) {
	dir := t.TempDir()
	collector := New(dir)
	update(t, collector, &message.AggregationRoutingPolicyMsg{Policy: routing.Policy{Version: 1,
		Destinations: []int64{1}, RecoveryBaselinePayments: 300, RecoveryObservedPayments: 72,
		RecoveryWindowMS: 2000, RecoveryWindows: 3, RecoveryChangePercent: 75}})
	require.NoError(t, collector.OutputResultAndClose())
	f, err := os.Open(filepath.Join(dir, RoutingMetricsFile))
	require.NoError(t, err)
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	values := map[string]string{}
	for i, key := range rows[0] {
		values[key] = rows[1][i]
	}
	require.Equal(t, "300", values["RecoveryBaselinePayments"])
	require.Equal(t, "72", values["RecoveryObservedPayments"])
	require.Equal(t, "2000", values["RecoveryWindowMs"])
	require.Equal(t, "3", values["RecoveryWindows"])
	require.Equal(t, "75", values["RecoveryChangePercent"])
}
