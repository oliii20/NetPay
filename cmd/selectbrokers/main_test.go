package main

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSelection(t *testing.T) {
	counts := map[string]int64{"a": 20, "b": 20, "c": 15, "d": 10, "e": 3, "f": 1}
	a, err := choose(counts, 5, 2, 7)
	require.NoError(t, err)
	require.Equal(t, "a", a[0].Address)
	require.Equal(t, "b", a[1].Address)
	b, err := choose(counts, 5, 2, 7)
	require.NoError(t, err)
	require.Equal(t, a, b)
	seen := map[string]bool{}
	for i, r := range a {
		require.False(t, seen[r.Address])
		seen[r.Address] = true
		if i < 2 {
			require.Equal(t, "hot", r.Kind)
		} else {
			require.Equal(t, "random", r.Kind)
		}
	}
	_, err = choose(counts, 7, 2, 1)
	require.Error(t, err)
}
