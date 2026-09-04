package nodetopo

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSystemNodesAreAddressableButExcludedFromShardLeaders(t *testing.T) {
	t.Parallel()

	shard := NodeInfo{ShardID: 0, NodeID: 0}
	solver := NodeInfo{ShardID: SolverShardID, NodeID: 0}
	beacon := NodeInfo{ShardID: BeaconShardID, NodeID: 0}
	supervisor := NodeInfo{ShardID: SupervisorShardID, NodeID: 0}
	topo := NewTopoGetter(
		map[int64]NodeInfo{
			0:                 shard,
			SolverShardID:     solver,
			BeaconShardID:     beacon,
			SupervisorShardID: supervisor,
		},
		nil,
	)

	gotSolver, err := topo.GetSolver()
	require.NoError(t, err)
	require.Equal(t, solver, gotSolver)
	leaders, err := topo.GetAllLeaders()
	require.NoError(t, err)
	require.Equal(t, []NodeInfo{shard}, leaders)
	require.True(t, IsSystemShardID(SolverShardID))
	require.True(t, IsSystemShardID(BeaconShardID))
	require.True(t, IsSystemShardID(SupervisorShardID))
	require.False(t, IsSystemShardID(0))
}
