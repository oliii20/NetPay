package solver_test

import (
	"context"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/batchstore"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/solver"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

func TestSolverBootstrapsBuildsAndRestoresBatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "solver.db")
	store, err := batchstore.Open(path)
	require.NoError(t, err)
	conn := network.NewConnHandler(&testP2P{})
	resolver := testResolver{}
	cfg := solver.Config{
		ShardCount: 2, BatchSize: 2, MaxWindowDuration: time.Minute, TickInterval: time.Millisecond,
	}
	node, err := solver.New(cfg, conn, resolver, store)
	require.NoError(t, err)

	require.NoError(t, node.HandleReceipt(message.FinalizedBlockReceiptMsg{
		NodeID: 0, Receipt: solverReceipt(0, solverHash(0x10), solverHash(0x11), solverPayment(0, 1, 10)),
	}))
	require.Empty(t, node.PendingBatchIDs())
	require.NoError(t, node.HandleReceipt(message.FinalizedBlockReceiptMsg{
		NodeID: 0, Receipt: solverReceipt(1, solverHash(0x20), solverHash(0x21), solverPayment(1, 0, 7)),
	}))
	pending := node.PendingBatchIDs()
	require.Len(t, pending, 1)
	proposal, err := store.GetProposal(pending[0])
	require.NoError(t, err)
	require.Equal(t, uint64(1), proposal.Header.WindowID)
	require.NoError(t, store.Close())

	store, err = batchstore.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	restarted, err := solver.New(cfg, conn, resolver, store)
	require.NoError(t, err)
	require.Equal(t, pending, restarted.PendingBatchIDs())
}

func TestSolverRejectsNonLeaderAndInvalidShard(t *testing.T) {
	store, err := batchstore.Open(filepath.Join(t.TempDir(), "solver.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	node, err := solver.New(solver.Config{
		ShardCount: 2, BatchSize: 2, MaxWindowDuration: time.Minute, TickInterval: time.Millisecond,
	}, network.NewConnHandler(&testP2P{}), testResolver{}, store)
	require.NoError(t, err)

	err = node.HandleReceipt(message.FinalizedBlockReceiptMsg{NodeID: 1})
	require.ErrorIs(t, err, solver.ErrInvalidReceiptNode)
	err = node.HandleReceipt(message.FinalizedBlockReceiptMsg{
		NodeID: 0, Receipt: model.FinalizedBlockReceipt{ShardID: 2},
	})
	require.ErrorIs(t, err, solver.ErrInvalidReceiptShard)
}

type testP2P struct{}

func (*testP2P) ListenStart() error                                                     { return nil }
func (*testP2P) DrainMsgBuffer() []*rpcserver.WrappedMsg                                { return nil }
func (*testP2P) SendMsg2Dest(context.Context, nodetopo.NodeInfo, *rpcserver.WrappedMsg) {}
func (*testP2P) Close()                                                                 {}

type testResolver struct{}

func (testResolver) SetTopoGetter(map[int64]map[int64]string) error     { return nil }
func (testResolver) GetNodesInShard(int64) ([]nodetopo.NodeInfo, error) { return nil, nil }
func (testResolver) GetLeader(shardID int64) (nodetopo.NodeInfo, error) {
	return nodetopo.NodeInfo{ShardID: shardID, NodeID: 0}, nil
}
func (testResolver) ChangeLeader(int64, nodetopo.NodeInfo) error { return nil }
func (testResolver) GetAllLeaders() ([]nodetopo.NodeInfo, error) { return nil, nil }
func (testResolver) GetSupervisor() (nodetopo.NodeInfo, error)   { return nodetopo.NodeInfo{}, nil }
func (testResolver) GetSolver() (nodetopo.NodeInfo, error)       { return nodetopo.NodeInfo{}, nil }

func solverReceipt(
	shardID int64,
	parent, blockHash merkle.Hash,
	payments ...intent.PaymentIntent,
) model.FinalizedBlockReceipt {
	return model.FinalizedBlockReceipt{
		ShardID: shardID, Height: 1, ParentHash: parent, BlockHash: blockHash,
		StateRoot: solverHash(byte(shardID + 30)), Epoch: 1, Intents: payments,
	}
}

func solverPayment(source, destination, amount int64) intent.PaymentIntent {
	var sender, recipient account.Address
	sender[0] = byte(source + 1)
	recipient[0] = byte(destination + 11)

	return intent.PaymentIntent{
		Version: intent.CurrentVersion, ChainID: 1, Sender: sender, Recipient: recipient,
		SourceShard: source, DestinationShard: destination, AssetID: intent.NativeAssetID,
		Amount: big.NewInt(amount), ExpiryEpoch: 100,
	}
}

func solverHash(value byte) merkle.Hash {
	var result merkle.Hash
	result[0] = value

	return result
}
