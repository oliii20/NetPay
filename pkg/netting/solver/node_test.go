package solver_test

import (
	"bytes"
	"context"
	"encoding/gob"
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
	p2p := &testP2P{}
	conn := network.NewConnHandler(p2p)
	resolver := testResolver{}
	cfg := solver.Config{
		ShardCount: 2, BatchSize: 2, MaxWindowDuration: time.Minute, TickInterval: time.Millisecond,
		MetricsEnabled: true,
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
	require.Len(t, p2p.sent, 1)
	require.Equal(t, message.NettingBatchMetricMessageType, p2p.sent[0].GetMsgType())
	var metricMsg message.NettingBatchMetricMsg
	require.NoError(t, gob.NewDecoder(bytes.NewReader(p2p.sent[0].GetPayload())).Decode(&metricMsg))
	require.Equal(t, proposal.Header.BatchID, metricMsg.Metric.BatchID)
	require.Equal(t, 2, metricMsg.Metric.IntentCount)
	require.Positive(t, metricMsg.Metric.BatchProposalBytes)
	require.Positive(t, metricMsg.Metric.SidecarBytes)
	require.NoError(t, store.Close())

	store, err = batchstore.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	restarted, err := solver.New(cfg, conn, resolver, store)
	require.NoError(t, err)
	require.Equal(t, pending, restarted.PendingBatchIDs())
	require.NoError(t, restarted.HandleFinalized(context.Background(), message.MatchRootFinalizedMsg{
		NodeID: 0, Header: proposal.Header,
	}))
	require.Empty(t, restarted.PendingBatchIDs())
	require.Len(t, p2p.sent, 3)
	for _, sent := range p2p.sent[1:] {
		require.Equal(t, message.SettlementPackageMessageType, sent.GetMsgType())
	}
}

func TestSolverDispatchesPendingBatchOnlyOnce(t *testing.T) {
	store, err := batchstore.Open(filepath.Join(t.TempDir(), "solver.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	p2p := &testP2P{}
	node, err := solver.New(solver.Config{
		ShardCount: 2, BatchSize: 2, MaxWindowDuration: time.Minute, TickInterval: time.Millisecond,
	}, network.NewConnHandler(p2p), testResolver{}, store)
	require.NoError(t, err)

	require.NoError(t, node.HandleReceipt(message.FinalizedBlockReceiptMsg{
		NodeID: 0, Receipt: solverReceipt(0, solverHash(0x10), solverHash(0x11), solverPayment(0, 1, 10)),
	}))
	require.NoError(t, node.HandleReceipt(message.FinalizedBlockReceiptMsg{
		NodeID: 0, Receipt: solverReceipt(1, solverHash(0x20), solverHash(0x21), solverPayment(1, 0, 7)),
	}))
	require.NoError(t, node.Step(context.Background()))
	require.Equal(t, 1, countSent(p2p.sent, message.BatchProposalMessageType))

	require.NoError(t, node.Step(context.Background()))
	require.Equal(t, 1, countSent(p2p.sent, message.BatchProposalMessageType))
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

func TestSolverStopsAfterConsensusStopMessage(t *testing.T) {
	p2p := &testP2P{}
	store, err := batchstore.Open(filepath.Join(t.TempDir(), "solver.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	node, err := solver.New(solver.Config{
		ShardCount: 2, BatchSize: 2, MaxWindowDuration: time.Minute, TickInterval: time.Millisecond,
	}, network.NewConnHandler(p2p), testResolver{}, store)
	require.NoError(t, err)
	stop, err := message.WrapMsg(message.StopConsensusMsg{})
	require.NoError(t, err)
	p2p.received = []*rpcserver.WrappedMsg{stop}

	err = node.Step(context.Background())
	require.EqualError(t, err, "solver stopped")
}

type testP2P struct {
	sent     []*rpcserver.WrappedMsg
	received []*rpcserver.WrappedMsg
}

func (*testP2P) ListenStart() error { return nil }
func (p *testP2P) DrainMsgBuffer() []*rpcserver.WrappedMsg {
	result := p.received
	p.received = nil

	return result
}
func (p *testP2P) SendMsg2Dest(_ context.Context, _ nodetopo.NodeInfo, msg *rpcserver.WrappedMsg) {
	p.sent = append(p.sent, msg)
}
func (*testP2P) Close() {}

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

func countSent(messages []*rpcserver.WrappedMsg, msgType string) int {
	count := 0
	for _, msg := range messages {
		if msg.GetMsgType() == msgType {
			count++
		}
	}

	return count
}

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
