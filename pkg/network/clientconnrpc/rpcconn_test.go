package clientconnrpc

import (
	"context"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
	"google.golang.org/grpc"
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

type blockingClient struct {
	entered chan struct{}
	release chan struct{}
}

func (c *blockingClient) HandleMessage(ctx context.Context, _ *rpcserver.HandleMessageRequest, _ ...grpc.CallOption) (*rpcserver.HandleMessageResponse, error) {
	c.entered <- struct{}{}
	select {
	case <-c.release:
		return &rpcserver.HandleMessageResponse{Ack: true}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestSlowRPCDoesNotBlockOtherDestinations(t *testing.T) {
	first := nodetopo.NodeInfo{ShardID: 1}
	second := nodetopo.NodeInfo{ShardID: 2}
	conn := NewRPCConn(nodetopo.NodeInfo{}, map[nodetopo.NodeInfo]string{first: "unused", second: "unused"}, 0)
	slow := &blockingClient{entered: make(chan struct{}, 1), release: make(chan struct{})}
	fast := &blockingClient{entered: make(chan struct{}, 1), release: make(chan struct{})}
	close(fast.release)
	conn.clientPool[first] = &clientConnection{client: slow}
	conn.clientPool[second] = &clientConnection{client: fast}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	release := slow.release
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	done := make(chan error, 1)
	go func() { done <- conn.sendMessage(ctx, first, &rpcserver.WrappedMsg{}) }()
	select {
	case <-slow.entered:
	case <-ctx.Done():
		t.Fatal("first RPC did not start")
	}
	// This must complete while the first RPC is still blocked.
	require.NoError(t, conn.sendMessage(ctx, second, &rpcserver.WrappedMsg{}))
	close(slow.release)
	require.NoError(t, <-done)
}
