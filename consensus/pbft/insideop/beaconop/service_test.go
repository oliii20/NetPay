package beaconop

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServiceGate(t *testing.T) {
	t.Parallel()
	t.Run("proportional nonblocking service", func(t *testing.T) {
		var gate serviceGate
		now := time.Unix(100, 0)
		ready, err := gate.ready(1, 1000, 100, now)
		require.NoError(t, err)
		require.False(t, ready)
		ready, err = gate.ready(1, 1000, 100, now.Add(100*time.Millisecond))
		require.NoError(t, err)
		require.True(t, ready)
		ready, err = gate.ready(2, 100, 100, now.Add(time.Second))
		require.NoError(t, err)
		require.False(t, ready)
		require.Equal(t, 10*time.Millisecond, gate.delay)
	})
	t.Run("zero preserves normal service", func(t *testing.T) {
		var gate serviceGate
		ready, err := gate.ready(1, 100, 0, time.Now())
		require.NoError(t, err)
		require.True(t, ready)
	})
	t.Run("invalid costs", func(t *testing.T) {
		for _, cost := range []int64{-1, math.MaxInt64} {
			var gate serviceGate
			_, err := gate.ready(1, 1000, cost, time.Now())
			require.Error(t, err)
		}
	})
}
