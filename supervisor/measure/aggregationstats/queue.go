package aggregationstats

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/csvwrite"
)

func (c *Collector) writeQueueSamples() error {
	times := make([]int64, 0, len(c.queueSamples))
	for stamp := range c.queueSamples {
		times = append(times, stamp)
	}

	slices.Sort(times)

	rows := make([][]string, 0, len(times))
	for _, stamp := range times {
		m := c.queueSamples[stamp]
		rows = append(rows, []string{
			strconv.FormatInt(stamp, 10), strconv.Itoa(m.FrozenWindows),
			strconv.Itoa(m.QueuedWindows), strconv.Itoa(m.PendingPayments), strconv.Itoa(m.QueuedOriginals),
			strconv.FormatInt(m.FrozenQueueWait.Nanoseconds(), 10), strconv.FormatUint(m.ActiveWindowID, 10),
			strconv.FormatUint(m.PolicyVersion, 10), strconv.Itoa(m.AggregationDestinations),
		})
	}

	if err := csvwrite.WriteAllToCSVReplace(filepath.Join(c.outputDir, QueueMetricsFile),
		[]string{
			"ObservedAtUnixNano", "FrozenWindows", "QueuedWindows", "PendingPayments", "QueuedOriginals",
			"FrozenQueueWaitNs", "ActiveWindowID", "PolicyVersion", "AggregationDestinations",
		}, rows); err != nil {
		return fmt.Errorf("write queue samples: %w", err)
	}

	return nil
}
