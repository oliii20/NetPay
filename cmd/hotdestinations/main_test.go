package main

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRankDeterministicAndIncludesZeroDestinations(t *testing.T) {
	require.Equal(t, []destination{{1, 9}, {2, 9}, {0, 2}, {3, 0}}, rank([]int64{2, 9, 9, 0}))
}
