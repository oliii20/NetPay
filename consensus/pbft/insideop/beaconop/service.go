package beaconop

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
)

// serviceGate simulates processing capacity without sleeping in the serial node
// loop. Receipts, PBFT messages and the routing controller continue to run.
type serviceGate struct {
	window    uint64
	startedAt time.Time
	delay     time.Duration
}

func (g *serviceGate) ready(window uint64, count int, micros int64, now time.Time) (bool, error) {
	if count <= 0 || micros < 0 || micros > math.MaxInt64/int64(time.Microsecond)/int64(count) {
		return false, fmt.Errorf("invalid or overflowing simulated Beacon service cost")
	}

	if g.window != window {
		g.window, g.startedAt = window, now
		g.delay = time.Duration(micros) * time.Microsecond * time.Duration(count)
	}

	return !now.Before(g.startedAt.Add(g.delay)), nil
}

// ObserveLoop runs once per node tick, including while a proposal is in flight.
func (o *Op) ObserveLoop(ctx context.Context) error {
	if o.nodeID != 0 || o.manager == nil {
		return nil
	}

	o.manager.TryClose()

	if err := o.updateRouting(ctx); err != nil {
		return err
	}

	now := time.Now()
	windows, originals := o.manager.QueuedWork(o.activeWindow)

	version, destinations := uint64(0), int(o.shardCount)
	if o.routing != nil {
		version, destinations = o.routing.PolicyState()
	}

	return o.metrics.PublishQueue(ctx, model.AggregationQueueMetric{
		ObservedAt: now, FrozenWindows: o.manager.FrozenWindowCount(), QueuedWindows: windows,
		PendingPayments: o.manager.PendingPaymentCount(), QueuedOriginals: originals,
		FrozenQueueWait: o.manager.QueuedFrozenWait(now, o.activeWindow), ActiveWindowID: o.activeWindow,
		PolicyVersion: version, AggregationDestinations: destinations,
	})
}
