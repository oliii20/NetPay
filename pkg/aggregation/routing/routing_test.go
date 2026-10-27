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
		_, changed = c.Evaluate(now, 2*time.Second)
		require.False(t, changed, "high threshold must be exceeded")
		p, changed := c.Evaluate(now, 3*time.Second)
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
		p, changed := c.Evaluate(now, 3*time.Second)
		require.True(t, changed)
		require.Len(t, p.Destinations, 18)
	})
	t.Run("minimum fraction and healthy recovery", func(t *testing.T) {
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
		p, changed := c.Evaluate(now.Add(6*time.Second), 0)
		require.True(t, changed)
		require.Len(t, p.Destinations, 2)
		require.Equal(t, "frozen_queue_recovered", p.Reason)
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

func TestConfigurationAndTelemetry(t *testing.T) {
	require.NoError(t, (routing.Config{}).WithDefaults().Validate())
	require.Error(t, (routing.Config{FreezeHighMS: 10, FreezeLowMS: 20}).WithDefaults().Validate())
	c := routing.NewController(4, routing.Config{})
	require.Error(t, c.Observe(model.FinalizedBlockReceipt{ShardID: 0, Height: 1,
		DestinationStats: []model.DestinationStat{{ShardID: 1, PaymentCount: 1, RecipientCount: 2}}}, time.Now()))
}
