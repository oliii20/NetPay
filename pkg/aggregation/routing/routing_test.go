package routing_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/routing"
)

func observe(t *testing.T, c *routing.Controller, height uint64, now time.Time) {
	t.Helper()
	require.NoError(t, c.Observe(model.FinalizedBlockReceipt{ShardID: 0, Height: height, DestinationStats: []model.DestinationStat{
		{ShardID: 1, PaymentCount: 100, RecipientCount: 10},
		{ShardID: 2, PaymentCount: 200, RecipientCount: 180},
		{ShardID: 3, PaymentCount: 100, RecipientCount: 40},
	}}, now))
}

func TestOnlineController(t *testing.T) {
	t.Run("starts full and uses only received benefit statistics", func(t *testing.T) {
		now := time.Unix(100, 0)
		c := routing.NewController(4, routing.Config{})
		require.Equal(t, []int64{0, 1, 2, 3}, c.Current().Destinations)
		_, changed := c.Evaluate(now, time.Hour)
		require.False(t, changed, "no offline workload knowledge")
		observe(t, c, 1, now)
		for _, wait := range []time.Duration{499 * time.Millisecond, 500 * time.Millisecond} {
			_, changed = c.Evaluate(now, wait)
			require.False(t, changed, "high threshold must be strictly exceeded")
		}
		p, changed := c.Evaluate(now, 501*time.Millisecond)
		require.True(t, changed)
		require.Equal(t, []int64{1, 2, 3}, p.Destinations)
		require.Equal(t, uint64(1), p.Version)
		_, changed = c.Evaluate(now.Add(time.Second), 3*time.Second)
		require.False(t, changed, "cooldown prevents rapid switching")
		observe(t, c, 2, now.Add(10*time.Second))
		p, changed = c.Evaluate(now.Add(10*time.Second), 3*time.Second)
		require.True(t, changed)
		require.Equal(t, []int64{1, 3}, p.Destinations, "prefer compression benefit over raw volume")
	})
	t.Run("24 shards selects 18 only after overload", func(t *testing.T) {
		now := time.Unix(100, 0)
		c := routing.NewController(24, routing.Config{})
		require.Len(t, c.Current().Destinations, 24)
		observe(t, c, 1, now)
		p, changed := c.Evaluate(now, 501*time.Millisecond)
		require.True(t, changed)
		require.Len(t, p.Destinations, 18)
	})
	t.Run("recovery uses the separate 100 millisecond low watermark", func(t *testing.T) {
		now := time.Unix(100, 0)
		c := routing.NewController(4, routing.Config{RecoveryWindowMS: 1000, RecoveryWindows: 1})
		observe(t, c, 1, now)
		_, changed := c.Evaluate(now, 501*time.Millisecond)
		require.True(t, changed)
		observe(t, c, 2, now.Add(500*time.Millisecond))
		require.NoError(t, c.Observe(model.FinalizedBlockReceipt{ShardID: 0, Height: 3,
			DestinationStats: []model.DestinationStat{{ShardID: 1, PaymentCount: 1, RecipientCount: 1}}}, now.Add(10*time.Second)))
		_, changed = c.Evaluate(now.Add(10*time.Second), 101*time.Millisecond)
		require.False(t, changed)
		for _, seconds := range []int{11, 12} {
			_, changed = c.Evaluate(now.Add(time.Duration(seconds)*time.Second), 100*time.Millisecond)
			require.False(t, changed)
		}
		_, changed = c.Evaluate(now.Add(12500*time.Millisecond), 100*time.Millisecond)
		require.False(t, changed, "health observations must remain a second apart")
		p, changed := c.Evaluate(now.Add(13*time.Second), 100*time.Millisecond)
		require.True(t, changed)
		require.Len(t, p.Destinations, 4)
	})
	t.Run("minimum fraction and no queue-only recovery", func(t *testing.T) {
		now := time.Unix(100, 0)
		c := routing.NewController(4, routing.Config{CooldownMS: 1000, ObservationWindowMS: 20000})
		observe(t, c, 1, now)
		for i := range 3 {
			_, changed := c.Evaluate(now.Add(time.Duration(i)*time.Second), 3*time.Second)
			require.True(t, changed)
		}
		require.Len(t, c.Current().Destinations, 1)
		_, changed := c.Evaluate(now.Add(3*time.Second), 3*time.Second)
		require.False(t, changed)
		for i := range 2 {
			_, changed = c.Evaluate(now.Add(time.Duration(4+i)*time.Second), 0)
			require.False(t, changed)
		}
		_, changed = c.Evaluate(now.Add(6*time.Second), 0)
		require.False(t, changed)
		require.Len(t, c.Current().Destinations, 1)
	})
	t.Run("repeated observations and expired statistics", func(t *testing.T) {
		now := time.Unix(100, 0)
		c := routing.NewController(4, routing.Config{})
		observe(t, c, 1, now)
		observe(t, c, 1, now)
		p, changed := c.Evaluate(now, 3*time.Second)
		require.True(t, changed)
		require.Equal(t, int64(100), p.PaymentCount[1])
		_, changed = c.Evaluate(now.Add(20*time.Second), 3*time.Second)
		require.False(t, changed)
	})
}

func TestPolicyTable(t *testing.T) {
	table := routing.NewTable(4)
	snapshot := table.Snapshot()
	p := routing.Policy{Version: 2, Destinations: []int64{1, 3}}
	require.NoError(t, table.Apply(p))
	require.NoError(t, table.Apply(p))
	require.NoError(t, table.Apply(routing.Policy{Version: 1, Destinations: []int64{0}}))
	require.Equal(t, []int64{1, 3}, table.Snapshot().Destinations)
	require.Equal(t, []int64{0, 1, 2, 3}, snapshot.Destinations)
	p.Destinations[0] = 0
	require.Equal(t, []int64{1, 3}, table.Snapshot().Destinations, "no borrowed slices")
	require.Error(t, table.Apply(p), "conflicting same-version policy")
	for _, dest := range [][]int64{nil, {1, 1}, {-1, 2}, {2, 4}, {3, 1}} {
		require.Error(t, table.Apply(routing.Policy{Version: 3, Destinations: dest}))
	}
}

func TestRecoveryRequiresCompletedPostReductionVolumeWindows(t *testing.T) {
	for _, tc := range []struct {
		name    string
		volume  int64
		wait    time.Duration
		recover bool
	}{
		{"stable traffic", 100, 0, false},
		{"exactly 75 percent lower", 25, 0, false},
		{"exactly 75 percent higher", 175, 0, false},
		{"more than 75 percent lower", 24, 0, true},
		{"more than 75 percent higher", 176, 0, true},
		{"volume changed but queue unhealthy", 24, 101 * time.Millisecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Unix(100, 0)
			c := routing.NewController(4, routing.Config{CooldownMS: 1000})
			var height uint64
			add := func(at time.Time, count int64) {
				height++
				r := model.FinalizedBlockReceipt{ShardID: 0, Height: height, DestinationStats: []model.DestinationStat{{ShardID: 3, PaymentCount: count, RecipientCount: 1}}}
				require.NoError(t, c.Observe(r, at))
			}
			add(start, 100)
			_, changed := c.Evaluate(start, 501*time.Millisecond)
			require.True(t, changed)
			for second := 1; second < 12; second++ {
				at := start.Add(time.Duration(second) * time.Second)
				if second%2 == 1 {
					count := int64(100)
					if second > 6 {
						count = tc.volume
					}
					add(at, count)
				}
				_, changed = c.Evaluate(at, tc.wait)
				require.False(t, changed, "must collect 3 baseline windows and 3 comparison windows before recovery")
			}
			policy, changed := c.Evaluate(start.Add(12*time.Second), tc.wait)
			require.Equal(t, tc.recover, changed)
			if changed {
				require.Equal(t, "frozen_queue_recovered", policy.Reason)
				require.Equal(t, int64(300), policy.RecoveryBaselinePayments)
				require.Equal(t, 3*tc.volume, policy.RecoveryObservedPayments)
			}
		})
	}
}

func TestRecoveryKeepsFirstBaselineAndCountsRelayOnlyDestinations(t *testing.T) {
	start := time.Unix(100, 0)
	c := routing.NewController(4, routing.Config{CooldownMS: 1000})
	var height uint64
	add := func(at time.Time, source, dest, count int64) {
		height++
		r := model.FinalizedBlockReceipt{ShardID: source, Height: height, DestinationStats: []model.DestinationStat{{ShardID: dest, PaymentCount: count, RecipientCount: 1}}}
		require.NoError(t, c.Observe(r, at))
		require.NoError(t, c.Observe(r, at), "replayed summaries cannot alter either volume")
	}
	add(start, 0, 3, 100)
	_, changed := c.Evaluate(start, 501*time.Millisecond)
	require.True(t, changed)
	var relayDestination int64
	for shard := int64(0); shard < 4; shard++ {
		if !c.Current().Aggregates(shard) {
			relayDestination = shard
		}
	}
	for second := 1; second <= 12; second++ {
		at := start.Add(time.Duration(second) * time.Second)
		if second%2 == 1 {
			count := int64(100)
			if second > 6 {
				count = 24
			}
			add(at, (relayDestination+1)%4, relayDestination, count)
		}
		wait := time.Duration(0)
		if second == 6 {
			wait = time.Second
		}
		policy, changed := c.Evaluate(at, wait)
		if second == 6 {
			require.True(t, changed)
			require.Len(t, policy.Destinations, 2)
		} else if second < 12 {
			require.False(t, changed)
		} else {
			require.True(t, changed)
			require.Len(t, policy.Destinations, 3)
			require.Equal(t, int64(300), policy.RecoveryBaselinePayments, "second reduction must not rebase")
			require.Equal(t, int64(72), policy.RecoveryObservedPayments)
		}
	}
}

func TestEmptyRecoveryBaselineCannotTriggerRecovery(t *testing.T) {
	start := time.Unix(100, 0)
	c := routing.NewController(4, routing.Config{CooldownMS: 1000})
	observe(t, c, 1, start)
	_, changed := c.Evaluate(start, time.Second)
	require.True(t, changed)
	// The first three post-reduction windows contain no received payments.
	for second := 1; second <= 12; second++ {
		at := start.Add(time.Duration(second) * time.Second)
		if second > 6 && second%2 == 1 {
			observe(t, c, uint64(second), at)
		}
		_, changed = c.Evaluate(at, 0)
		require.False(t, changed, "a zero baseline cannot define a relative volume change")
	}
}

func TestConfigurationAndTelemetry(t *testing.T) {
	require.NoError(t, (routing.Config{}).WithDefaults().Validate())
	require.Error(t, (routing.Config{FreezeHighMS: 10, FreezeLowMS: 20}).WithDefaults().Validate())
	c := routing.NewController(4, routing.Config{})
	require.Error(t, c.Observe(model.FinalizedBlockReceipt{ShardID: 0, Height: 1,
		DestinationStats: []model.DestinationStat{{ShardID: 1, PaymentCount: 1, RecipientCount: 2}}}, time.Now()))
}
