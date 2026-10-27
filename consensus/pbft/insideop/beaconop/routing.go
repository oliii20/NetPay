package beaconop

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

func (o *Op) updateRouting(ctx context.Context) error {
	if o.routing == nil || o.manager == nil {
		return nil
	}

	now := time.Now()
	wait := o.manager.QueuedFrozenWait(now, o.activeWindow)

	policy, changed := o.routing.Evaluate(now, wait)
	if !changed {
		policy = o.routing.Current()
		if policy.Version == 0 || now.Sub(o.lastPolicyBroadcast) < time.Second {
			return nil
		}
	}

	wrapped, err := message.WrapMsg(&message.AggregationRoutingPolicyMsg{
		NodeID: o.nodeID, ShardID: nodetopo.BeaconShardID, Policy: policy,
	})
	if err != nil {
		return fmt.Errorf("wrap adaptive routing policy: %w", err)
	}

	if err = o.broadcastFinalized(ctx, wrapped); err != nil {
		return fmt.Errorf("broadcast adaptive routing policy: %w", err)
	}

	supervisor, err := o.resolver.GetSupervisor()
	if err != nil {
		return fmt.Errorf("resolve routing-policy supervisor: %w", err)
	}

	o.conn.SendMsg2Dest(ctx, supervisor, wrapped)
	o.lastPolicyBroadcast = now

	if changed {
		slog.InfoContext(ctx, "adaptive aggregation routing changed", "version", policy.Version,
			"reason", policy.Reason, "frozen_wait_ms", wait.Milliseconds(), "destinations", policy.Destinations)
	}

	return nil
}
