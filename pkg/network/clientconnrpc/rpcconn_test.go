package clientconnrpc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

func TestNewRPCConnStoresConfiguredLatency(t *testing.T) {
	t.Parallel()

	me := nodetopo.NodeInfo{ShardID: 0, NodeID: 0}
	conn := NewRPCConn(me, map[nodetopo.NodeInfo]string{me: "127.0.0.1:0"}, 25*time.Millisecond)

	require.Equal(t, 25*time.Millisecond, conn.latency)
}
