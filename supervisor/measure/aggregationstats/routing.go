package aggregationstats

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/csvwrite"
)

func (c *Collector) writeRoutingPolicies() error {
	versions := make([]uint64, 0, len(c.routingPolicies))
	for version := range c.routingPolicies {
		versions = append(versions, version)
	}

	slices.Sort(versions)
	rows := make([][]string, 0, len(versions))
	join := func(ids []int64) string {
		parts := make([]string, len(ids))
		for i, id := range ids {
			parts[i] = strconv.FormatInt(id, 10)
		}

		return strings.Join(parts, ";")
	}

	for _, version := range versions {
		p := c.routingPolicies[version]
		rows = append(rows, []string{
			strconv.FormatUint(p.Version, 10), strconv.FormatInt(p.IssuedAt.UnixNano(), 10),
			p.Reason, strconv.FormatInt(p.FrozenWait.Nanoseconds(), 10), join(p.Destinations),
			join(p.PaymentCount), join(p.RecipientCount),
			strconv.FormatInt(p.RecoveryBaselinePayments, 10), strconv.FormatInt(p.RecoveryObservedPayments, 10),
			strconv.FormatInt(
				p.RecoveryWindowMS,
				10,
			), strconv.Itoa(p.RecoveryWindows), strconv.Itoa(p.RecoveryChangePercent),
		})
	}

	if err := csvwrite.WriteAllToCSVReplace(filepath.Join(c.outputDir, RoutingMetricsFile),
		[]string{
			"Version", "IssuedAtUnixNano", "Reason", "FrozenQueueWaitNs", "DestinationShards", "ObservedPaymentCounts", "ObservedRecipientCounts",
			"RecoveryBaselinePayments", "RecoveryObservedPayments", "RecoveryWindowMs", "RecoveryWindows", "RecoveryChangePercent",
		}, rows); err != nil {
		return fmt.Errorf("write routing policies: %w", err)
	}

	return nil
}
