package fallback_test

import (
	"bytes"
	"context"
	"encoding/gob"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/config"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/fallback"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/registry"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/storage/vmstate"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/vm"
)

func TestOutboxPersistsPayloadAndCompletesIdempotently(t *testing.T) {
	t.Parallel()

	store, err := vmstate.NewStateStore(
		config.StorageCfg{EthStorageCfg: config.EthStorageCfg{IsMemoryDB: true}},
		config.LocalParams{},
	)
	require.NoError(t, err)
	executor, err := vm.NewExecutor(store, types.EmptyRootHash, params.MainnetChainConfig)
	require.NoError(t, err)
	stateDB := executor.StateDB()
	stateDB.SetBalance(
		common.Address(registry.EscrowAccountAddress), uint256.NewInt(10), tracing.BalanceChangeUnspecified,
	)
	item := fallbackItem()
	outbox := fallback.NewOutbox(stateDB)

	require.NoError(t, outbox.Create(item))
	require.Equal(t, uint64(7), stateDB.GetBalance(common.Address(registry.EscrowAccountAddress)).Uint64())
	pending, err := outbox.ListPending()
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, item.Key(), pending[0].Key())
	require.Equal(t, item.Amount, pending[0].Amount)
	require.Equal(t, item.SourceShard, pending[0].SourceShard)
	require.Equal(t, item.DestinationShard, pending[0].DestinationShard)
	require.Equal(t, item.Proof.Header.Cuts, pending[0].Proof.Header.Cuts)
	require.NoError(t, outbox.Create(item))
	require.Equal(t, uint64(7), stateDB.GetBalance(common.Address(registry.EscrowAccountAddress)).Uint64())
	require.NoError(t, outbox.Complete(item.Key()))
	require.NoError(t, outbox.Complete(item.Key()))
	require.Equal(t, fallback.StatusCompleted, outbox.Status(item.Key()))
	pending, err = outbox.ListPending()
	require.NoError(t, err)
	require.Empty(t, pending)
}

func TestPublisherSendsPendingAndCompletionMessages(t *testing.T) {
	t.Parallel()

	item := fallbackItem()
	p2p := &fallbackP2P{}
	publisher := fallback.NewPublisher(
		true, 0, fallbackReader{items: []model.ReservedFallback{item}},
		network.NewConnHandler(p2p), fallbackResolver{},
	)
	committed := &block.Block{Body: block.Body{TxList: []transaction.Transaction{
		*transaction.NewReservedFallbackTransaction(item, time.Unix(1, 0)),
	}}}
	require.NoError(t, publisher.PublishAfterBlock(context.Background(), committed))
	require.Len(t, p2p.messages, 4)
	require.Equal(t, message.FallbackCompletedMessageType, p2p.messages[0].GetMsgType())
	require.Equal(t, message.NettingProgressMessageType, p2p.messages[2].GetMsgType())
	var progress message.NettingProgressMsg
	require.NoError(t, gob.NewDecoder(bytes.NewReader(p2p.messages[2].GetPayload())).Decode(&progress))
	require.Equal(t, []intent.ID{item.IntentID}, progress.IntentIDs)
	require.Equal(t, message.FallbackTxMessageType, p2p.messages[3].GetMsgType())
}

func TestPublisherSuppressesImmediatePendingFallbackRepublish(t *testing.T) {
	t.Parallel()

	item := fallbackItem()
	p2p := &fallbackP2P{}
	publisher := fallback.NewPublisher(
		true, 0, fallbackReader{items: []model.ReservedFallback{item}},
		network.NewConnHandler(p2p), fallbackResolver{},
	)
	emptyBlock := &block.Block{}

	require.NoError(t, publisher.PublishAfterBlock(context.Background(), emptyBlock))
	require.Len(t, p2p.messages, 1)
	require.Equal(t, message.FallbackTxMessageType, p2p.messages[0].GetMsgType())

	require.NoError(t, publisher.PublishAfterBlock(context.Background(), emptyBlock))
	require.Len(t, p2p.messages, 1)
}

type fallbackReader struct {
	items []model.ReservedFallback
}

func (f fallbackReader) GetPendingFallbacks(context.Context) ([]model.ReservedFallback, error) {
	return f.items, nil
}

type fallbackP2P struct {
	messages []*rpcserver.WrappedMsg
}

func (*fallbackP2P) ListenStart() error                      { return nil }
func (*fallbackP2P) DrainMsgBuffer() []*rpcserver.WrappedMsg { return nil }
func (f *fallbackP2P) SendMsg2Dest(_ context.Context, _ nodetopo.NodeInfo, msg *rpcserver.WrappedMsg) {
	f.messages = append(f.messages, msg)
}
func (*fallbackP2P) Close() {}

type fallbackResolver struct{}

func (fallbackResolver) SetTopoGetter(map[int64]map[int64]string) error     { return nil }
func (fallbackResolver) GetNodesInShard(int64) ([]nodetopo.NodeInfo, error) { return nil, nil }
func (fallbackResolver) GetLeader(shardID int64) (nodetopo.NodeInfo, error) {
	return nodetopo.NodeInfo{ShardID: shardID}, nil
}
func (fallbackResolver) ChangeLeader(int64, nodetopo.NodeInfo) error { return nil }
func (fallbackResolver) GetAllLeaders() ([]nodetopo.NodeInfo, error) { return nil, nil }
func (fallbackResolver) GetSupervisor() (nodetopo.NodeInfo, error) {
	return nodetopo.NodeInfo{ShardID: nodetopo.SupervisorShardID}, nil
}
func (fallbackResolver) GetSolver() (nodetopo.NodeInfo, error) { return nodetopo.NodeInfo{}, nil }

func fallbackItem() model.ReservedFallback {
	return model.ReservedFallback{
		IntentID: modelFallbackIntentID(1), BatchID: merkle.Hash{2},
		SourceShard: 0, DestinationShard: 1, Amount: big.NewInt(3),
		Proof: model.SettlementPackage{Header: model.MatchRootBlockBody{
			Cuts: []model.ShardCut{{EndBlockHash: merkle.Hash{3}}},
		}},
	}
}

func modelFallbackIntentID(value byte) intent.ID {
	var id intent.ID
	id[0] = value

	return id
}
