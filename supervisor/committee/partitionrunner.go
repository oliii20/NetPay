package committee

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/csvwrite"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/partition"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/utils"
)

const clpaRepartitionMetricsPath = "clpa_repartition_metrics.csv"

var clpaRepartitionMetricsHeader = []string{
	"Epoch",
	"Start time",
	"Partition end time",
	"Broadcast end time",
	"Sync end time",
	"Migrated account count",
	"Partition latency ns",
	"Broadcast latency ns",
	"Migration sync latency ns",
	"Total latency ns",
}

type clpaRepartitionMetric struct {
	epoch                int64
	startTime            time.Time
	partitionEndTime     time.Time
	broadcastEndTime     time.Time
	syncEndTime          time.Time
	migratedAccountCount int
}

// partitionRunner maintains the states of CLPAState and records the consensus states of the partition algorithm.
type partitionRunner struct {
	state           *partition.CLPAState // state is the module of CLPA, aka., an account-reallocation algorithm.
	lastRunTime     time.Time
	epochSynced     bool    // labels that the epochs of the supervisor and all shards are synced (i.e., the clpa round is done).
	supervisorEpoch int64   // supervisorEpoch is the epoch ID of this supervisor.
	shardEpoch      []int64 // shardEpoch tells the epoch ID for each shard.

	clpaMetricsPath string
	clpaMetrics     []clpaRepartitionMetric
}

// CheckEpochSyncAndMark tells whether all shards are in the correct epoch
func (p *partitionRunner) CheckEpochSyncAndMark() bool {
	return p.checkEpochSyncAndMarkAt(time.Now())
}

func (p *partitionRunner) checkEpochSyncAndMarkAt(now time.Time) bool {
	// if the epoch is synced, return
	if p.epochSynced {
		return true
	}

	// if the epoch is not synced, retry to compute it.
	for _, epochID := range p.shardEpoch {
		if epochID != p.supervisorEpoch {
			return false
		}
	}

	// the epoch is synced, record the last run time
	p.epochSynced = true
	p.lastRunTime = now
	if err := p.recordCLPASyncComplete(now); err != nil {
		slog.Error("failed to record CLPA sync metric", "err", err)
	}

	return true
}

func clpaMetricsFile(resultOutputDir string) string {
	return filepath.Join(resultOutputDir, clpaRepartitionMetricsPath)
}

func (p *partitionRunner) recordCLPARound(
	epoch int64,
	startTime time.Time,
	partitionEndTime time.Time,
	broadcastEndTime time.Time,
	migratedAccountCount int,
) error {
	p.clpaMetrics = append(p.clpaMetrics, clpaRepartitionMetric{
		epoch:                epoch,
		startTime:            startTime,
		partitionEndTime:     partitionEndTime,
		broadcastEndTime:     broadcastEndTime,
		migratedAccountCount: migratedAccountCount,
	})

	return p.writeCLPAMetrics()
}

func (p *partitionRunner) recordCLPASyncComplete(syncEndTime time.Time) error {
	for idx := len(p.clpaMetrics) - 1; idx >= 0; idx-- {
		metric := &p.clpaMetrics[idx]
		if metric.epoch == p.supervisorEpoch && metric.syncEndTime.IsZero() {
			metric.syncEndTime = syncEndTime
			return p.writeCLPAMetrics()
		}
	}

	return nil
}

func (p *partitionRunner) writeCLPAMetrics() error {
	if p.clpaMetricsPath == "" {
		return nil
	}

	rows := make([][]string, 0, len(p.clpaMetrics))
	for _, metric := range p.clpaMetrics {
		rows = append(rows, metric.csvLine())
	}

	if err := csvwrite.WriteAllToCSVReplace(p.clpaMetricsPath, clpaRepartitionMetricsHeader, rows); err != nil {
		return fmt.Errorf("write CLPA repartition metrics: %w", err)
	}

	return nil
}

func (m clpaRepartitionMetric) csvLine() []string {
	partitionLatency := durationBetween(m.startTime, m.partitionEndTime)
	broadcastLatency := durationBetween(m.partitionEndTime, m.broadcastEndTime)
	syncLatency := durationBetween(m.broadcastEndTime, m.syncEndTime)
	totalLatency := durationBetween(m.startTime, m.syncEndTime)

	return []string{
		strconv.FormatInt(m.epoch, 10),
		utils.ConvertTime2Str(m.startTime),
		utils.ConvertTime2Str(m.partitionEndTime),
		utils.ConvertTime2Str(m.broadcastEndTime),
		utils.ConvertTime2Str(m.syncEndTime),
		strconv.Itoa(m.migratedAccountCount),
		strconv.FormatInt(partitionLatency.Nanoseconds(), 10),
		strconv.FormatInt(broadcastLatency.Nanoseconds(), 10),
		strconv.FormatInt(syncLatency.Nanoseconds(), 10),
		strconv.FormatInt(totalLatency.Nanoseconds(), 10),
	}
}

func durationBetween(start time.Time, end time.Time) time.Duration {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}

	return end.Sub(start)
}
