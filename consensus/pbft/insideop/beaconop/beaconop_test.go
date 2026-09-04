package beaconop_test

import (
	"context"
	"math/big"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/consensus/pbft/insideop/beaconop"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/beacon"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/window"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

func TestOpQueuesValidatesCommitsAndBroadcasts(t *testing.T) {
	store, err := beacon.OpenStore(filepath.Join(t.TempDir(), "beacon.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	p2p := &capturingP2P{}
	op := beaconop.New(network.NewConnHandler(p2p), opResolver{}, store, 2, 0)
	proposal := opProposal(t)
	wrapped, err := message.WrapMsg(&message.BatchProposalMsg{NodeID: 0, Proposal: proposal})
	require.NoError(t, err)

	require.NoError(t, op.HandleMsgOutsideShard(context.Background(), wrapped))
	pbftProposal, err := op.BuildProposal(context.Background())
	require.NoError(t, err)
	require.NotNil(t, pbftProposal)
	require.NoError(t, op.ValidateProposal(context.Background(), pbftProposal))
	require.NoError(t, op.ProposalCommitAndDeliver(context.Background(), true, pbftProposal))
	tip, found, err := store.Tip()
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, proposal.Header.BatchID, tip.Body.BatchID)
	sentMessages := p2p.messages()
	require.NotEmpty(t, sentMessages)
	for _, sent := range sentMessages {
		require.Equal(t, message.MatchRootFinalizedMessageType, sent.GetMsgType())
	}
}

type capturingP2P struct {
	mux  sync.Mutex
	sent []*rpcserver.WrappedMsg
}

func (*capturingP2P) ListenStart() error                      { return nil }
func (*capturingP2P) DrainMsgBuffer() []*rpcserver.WrappedMsg { return nil }
func (p *capturingP2P) SendMsg2Dest(_ context.Context, _ nodetopo.NodeInfo, msg *rpcserver.WrappedMsg) {
	p.mux.Lock()
	defer p.mux.Unlock()
	p.sent = append(p.sent, msg)
}
func (*capturingP2P) Close() {}

func (p *capturingP2P) messages() []*rpcserver.WrappedMsg {
	p.mux.Lock()
	defer p.mux.Unlock()

	return append([]*rpcserver.WrappedMsg(nil), p.sent...)
}

type opResolver struct{}

func (opResolver) SetTopoGetter(map[int64]map[int64]string) error { return nil }
func (opResolver) GetNodesInShard(shardID int64) ([]nodetopo.NodeInfo, error) {
	return []nodetopo.NodeInfo{{ShardID: shardID, NodeID: 0}}, nil
}
func (opResolver) GetLeader(shardID int64) (nodetopo.NodeInfo, error) {
	return nodetopo.NodeInfo{ShardID: shardID, NodeID: 0}, nil
}
func (opResolver) ChangeLeader(int64, nodetopo.NodeInfo) error { return nil }
func (opResolver) GetAllLeaders() ([]nodetopo.NodeInfo, error) { return nil, nil }
func (opResolver) GetSupervisor() (nodetopo.NodeInfo, error) {
	return nodetopo.NodeInfo{ShardID: nodetopo.SupervisorShardID}, nil
}
func (opResolver) GetSolver() (nodetopo.NodeInfo, error) {
	return nodetopo.NodeInfo{ShardID: nodetopo.SolverShardID}, nil
}

func opProposal(t *testing.T) model.BatchProposal {
	t.Helper()
	forward := opPayment(0, 1, 10)
	reverse := opPayment(1, 0, 7)
	proposal, err := (batch.Builder{}).Build(window.FrozenWindow{
		WindowID: 1,
		Cuts: []model.ShardCut{
			{ShardID: 0, EndHeight: 1, EndBlockHash: opHash(0x11)},
			{ShardID: 1, EndHeight: 1, EndBlockHash: opHash(0x21)},
		},
		Receipts: []model.FinalizedBlockReceipt{
			{ShardID: 0, Height: 1, ParentHash: opHash(0x10), BlockHash: opHash(0x11), Epoch: 1, Intents: []intent.PaymentIntent{forward}},
			{ShardID: 1, Height: 1, ParentHash: opHash(0x20), BlockHash: opHash(0x21), Epoch: 1, Intents: []intent.PaymentIntent{reverse}},
		},
		Intents: []intent.PaymentIntent{forward, reverse},
	}, merkle.Hash{})
	require.NoError(t, err)

	return proposal
}

func opPayment(source, destination, amount int64) intent.PaymentIntent {
	var sender, recipient account.Address
	sender[0] = byte(source + 1)
	recipient[0] = byte(destination + 11)

	return intent.PaymentIntent{
		Version: intent.CurrentVersion, ChainID: 1, Sender: sender, Recipient: recipient,
		SourceShard: source, DestinationShard: destination, AssetID: intent.NativeAssetID,
		Amount: big.NewInt(amount), ExpiryEpoch: 100,
	}
}

func opHash(value byte) merkle.Hash {
	var result merkle.Hash
	result[0] = value

	return result
}
