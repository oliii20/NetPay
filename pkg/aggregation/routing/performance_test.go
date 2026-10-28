package routing_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/routing"
)

func populatedController(t testing.TB) (*routing.Controller, time.Time, []model.DestinationStat) {
	t.Helper()
	now := time.Unix(100, 0)
	c := routing.NewController(24, routing.Config{})
	stats := make([]model.DestinationStat, 23)
	for i := range stats {
		stats[i] = model.DestinationStat{ShardID: int64(i + 1), PaymentCount: 100, RecipientCount: 10}
	}
	for i := 0; i < 1000; i++ {
		require.NoError(t, c.Observe(model.FinalizedBlockReceipt{ShardID: 0, Height: uint64(i + 1), DestinationStats: stats}, now.Add(-9*time.Second+time.Duration(i)*time.Millisecond)))
	}
	return c, now, stats
}

func TestUnchangedRoutingEvaluationDoesNotAllocate(t *testing.T) {
	c, now, _ := populatedController(t)
	_, changed := c.Evaluate(now, 100*time.Millisecond)
	require.False(t, changed)
	allocations := testing.AllocsPerRun(100, func() {
		_, changed = c.Evaluate(now, 100*time.Millisecond)
	})
	require.False(t, changed)
	require.Zero(t, allocations, "stable traffic should not allocate or copy its entire lookback history")
}

func TestTelemetryExpiryBoundaryAndPolicySnapshotOwnership(t *testing.T) {
	start := time.Unix(100, 0)
	c := routing.NewController(4, routing.Config{CooldownMS: 1, ObservationWindowMS: 1000})
	stats := []model.DestinationStat{{ShardID: 1, PaymentCount: 100, RecipientCount: 10}}
	require.NoError(t, c.Observe(model.FinalizedBlockReceipt{ShardID: 0, Height: 1, DestinationStats: stats}, start))
	stats[0].PaymentCount = 9999
	require.NoError(t, c.Observe(model.FinalizedBlockReceipt{ShardID: 0, Height: 2, DestinationStats: []model.DestinationStat{{ShardID: 2, PaymentCount: 40, RecipientCount: 20}}}, start.Add(500*time.Millisecond)))
	p1, changed := c.Evaluate(start.Add(time.Second), time.Second)
	require.True(t, changed)
	require.Equal(t, []int64{0, 100, 40, 0}, p1.PaymentCount, "sample at the exact cutoff remains eligible")
	require.NoError(t, c.Observe(model.FinalizedBlockReceipt{ShardID: 0, Height: 3, DestinationStats: []model.DestinationStat{{ShardID: 3, PaymentCount: 10, RecipientCount: 1}}}, start.Add(1001*time.Millisecond)))
	require.Equal(t, []int64{0, 100, 40, 0}, c.Current().PaymentCount, "live telemetry must not mutate the published policy")
	p2, changed := c.Evaluate(start.Add(1500*time.Millisecond), time.Second)
	require.True(t, changed)
	require.Equal(t, []int64{0, 0, 40, 10}, p2.PaymentCount)
	p3, changed := c.Evaluate(start.Add(1501*time.Millisecond), time.Second)
	require.True(t, changed)
	require.Equal(t, []int64{0, 0, 0, 10}, p3.PaymentCount)
	require.Equal(t, []int64{0, 100, 40, 0}, p1.PaymentCount)
	_, changed = c.Evaluate(start.Add(2002*time.Millisecond), time.Second)
	require.False(t, changed, "all old telemetry has expired")
}

func TestIncrementalTelemetrySurvivesLongStreamsAndCompaction(t *testing.T) {
	start := time.Unix(100, 0)
	c := routing.NewController(24, routing.Config{CooldownMS: 1, ObservationWindowMS: 1000})
	observations := make([]int64, 0, 6000)
	for phase, limit := range []int{3000, 4000, 6000} {
		for index := len(observations); index < limit; index++ {
			count := int64(1 + index%5)
			observations = append(observations, count)
			require.NoError(t, c.Observe(model.FinalizedBlockReceipt{ShardID: 0, Height: uint64(index + 1), DestinationStats: []model.DestinationStat{{ShardID: 1, PaymentCount: count, RecipientCount: 1}}}, start.Add(time.Duration(index)*time.Millisecond)))
		}
		var expected int64
		for index, count := range observations {
			if index >= limit-1000 {
				expected += count
			}
		}
		policy, changed := c.Evaluate(start.Add(time.Duration(limit)*time.Millisecond), time.Second)
		require.True(t, changed)
		require.Equal(t, uint64(phase+1), policy.Version)
		require.Equal(t, expected, policy.PaymentCount[1], "incremental totals must match a direct lookback calculation")
	}
	_, changed := c.Evaluate(start.Add(20*time.Second), time.Second)
	require.False(t, changed)
	version, destinations := c.PolicyState()
	require.Equal(t, uint64(3), version)
	require.Equal(t, 6, destinations)
}

func TestRecoveryRollingComparisonRetainsItsThreeWindowMeaning(t *testing.T) {
	start := time.Unix(100, 0)
	c := routing.NewController(4, routing.Config{CooldownMS: 1000})
	observe(t, c, 1, start)
	_, changed := c.Evaluate(start, time.Second)
	require.True(t, changed)
	for second := 1; second <= 18; second++ {
		at := start.Add(time.Duration(second) * time.Second)
		if second%2 == 1 {
			count := int64(100)
			if second > 12 {
				count = 24
			}
			require.NoError(t, c.Observe(model.FinalizedBlockReceipt{ShardID: 0, Height: uint64(second + 1), DestinationStats: []model.DestinationStat{{ShardID: 3, PaymentCount: count, RecipientCount: 1}}}, at))
		}
		policy, changed := c.Evaluate(at, 0)
		if second < 18 {
			require.False(t, changed)
		} else {
			require.True(t, changed)
			require.Equal(t, int64(300), policy.RecoveryBaselinePayments)
			require.Equal(t, int64(72), policy.RecoveryObservedPayments)
		}
	}
}

func BenchmarkRoutingEvaluation24Shards(b *testing.B) {
	for _, mode := range []string{"steady", "cooldown", "receipt_and_check"} {
		b.Run(mode, func(b *testing.B) {
			c, now, stats := populatedController(b)
			if mode == "cooldown" {
				_, changed := c.Evaluate(now, 501*time.Millisecond)
				require.True(b, changed)
				now = now.Add(time.Second)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if mode == "receipt_and_check" {
					if err := c.Observe(model.FinalizedBlockReceipt{ShardID: 0, Height: uint64(1001 + i), DestinationStats: stats}, now); err != nil {
						b.Fatal(err)
					}
				}
				_, changed := c.Evaluate(now, 100*time.Millisecond)
				if changed {
					b.Fatal("unexpected policy change")
				}
			}
		})
	}
}
