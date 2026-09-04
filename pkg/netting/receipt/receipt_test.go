package receipt_test

import (
	"bytes"
	"context"
	"encoding/gob"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/receipt"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

func TestBuildFinalizedBlockReceipt(t *testing.T) {
	t.Parallel()

	payment := receiptTestIntent()
	b := receiptTestBlock([]transaction.Transaction{
		*transaction.NewIntentTransaction(payment, time.Unix(10, 0)),
		*transaction.NewTransaction(account.Address{}, account.Address{}, big.NewInt(1), big.NewInt(0), 0, time.Unix(11, 0)),
	})
	committedAt := time.Unix(20, 0)

	finalized, err := receipt.Build(0, 7, b, committedAt)
	require.NoError(t, err)
	require.Equal(t, int64(0), finalized.ShardID)
	require.Equal(t, b.Number, finalized.Height)
	require.Equal(t, int64(7), finalized.Epoch)
	require.Equal(t, committedAt, finalized.CommitTime)
	require.Equal(t, b.ParentBlockHash, finalized.ParentHash[:])
	require.Equal(t, b.StateRoot, finalized.StateRoot[:])
	require.Len(t, finalized.Intents, 1)
	require.Equal(t, payment, finalized.Intents[0])

	expectedHash, err := b.Hash()
	require.NoError(t, err)
	require.Equal(t, expectedHash, finalized.BlockHash[:])

	b.TxList[0].Intent.Amount.SetInt64(999)
	require.Equal(t, int64(10), finalized.Intents[0].Amount.Int64())
}

func TestBuildIncludesEmptyTransactionBlocks(t *testing.T) {
	t.Parallel()

	finalized, err := receipt.Build(1, 2, receiptTestBlock(nil), time.Unix(20, 0))
	require.NoError(t, err)
	require.Empty(t, finalized.Intents)
}

func TestBuildRejectsMalformedBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*block.Block)
		want   error
	}{
		{
			name: "migration block",
			mutate: func(b *block.Block) {
				b.Type = block.MigrationBlockType
			},
			want: receipt.ErrNotTransactionBlock,
		},
		{
			name: "parent hash",
			mutate: func(b *block.Block) {
				b.ParentBlockHash = []byte{1}
			},
			want: receipt.ErrInvalidParentHash,
		},
		{
			name: "state root",
			mutate: func(b *block.Block) {
				b.StateRoot = []byte{1}
			},
			want: receipt.ErrInvalidStateRoot,
		},
		{
			name: "intent source",
			mutate: func(b *block.Block) {
				payment := receiptTestIntent()
				payment.SourceShard = 1
				b.TxList = []transaction.Transaction{*transaction.NewIntentTransaction(payment, time.Unix(10, 0))}
			},
			want: receipt.ErrIntentSource,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := receiptTestBlock(nil)
			tc.mutate(b)
			_, err := receipt.Build(0, 1, b, time.Unix(20, 0))
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestPublisherSendsOnlyWhenEnabledOnNodeZero(t *testing.T) {
	t.Parallel()

	solver := nodetopo.NodeInfo{ShardID: nodetopo.SolverShardID, NodeID: 0}
	topo := nodetopo.NewTopoGetter(
		map[int64]nodetopo.NodeInfo{nodetopo.SolverShardID: solver},
		map[int64][]nodetopo.NodeInfo{nodetopo.SolverShardID: {solver}},
	)

	for _, tc := range []struct {
		name         string
		enabled      bool
		nodeID       int64
		wantMessages int
	}{
		{name: "enabled node zero", enabled: true, nodeID: 0, wantMessages: 1},
		{name: "disabled", enabled: false, nodeID: 0, wantMessages: 0},
		{name: "nonzero node", enabled: true, nodeID: 1, wantMessages: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			conn := newRecordingConn()
			publisher := receipt.NewPublisher(tc.enabled, tc.nodeID, conn, topo)
			err := publisher.Publish(context.Background(), 0, 3, receiptTestBlock(nil), time.Unix(20, 0))
			require.NoError(t, err)
			require.Len(t, conn.messages, tc.wantMessages)
			if tc.wantMessages == 0 {
				return
			}

			require.Equal(t, solver, conn.destinations[0])
			require.Equal(t, message.FinalizedBlockReceiptMessageType, conn.messages[0].MsgType)
			var payload message.FinalizedBlockReceiptMsg
			err = gob.NewDecoder(bytes.NewReader(conn.messages[0].Payload)).Decode(&payload)
			require.NoError(t, err)
			require.Equal(t, int64(0), payload.NodeID)
			require.Equal(t, uint64(2), payload.Receipt.Height)
		})
	}
}

func TestPublisherRequiresSolverWhenEnabled(t *testing.T) {
	t.Parallel()

	publisher := receipt.NewPublisher(true, 0, newRecordingConn(), nodetopo.NewTopoGetter(nil, nil))
	err := publisher.Publish(context.Background(), 0, 1, receiptTestBlock(nil), time.Unix(20, 0))
	require.ErrorContains(t, err, "resolve solver")
}

func receiptTestBlock(txs []transaction.Transaction) *block.Block {
	return block.NewBlock(block.Header{
		ParentBlockHash: bytes.Repeat([]byte{0x10}, 32),
		StateRoot:       bytes.Repeat([]byte{0x20}, 32),
		LocationRoot:    bytes.Repeat([]byte{0x30}, 32),
		Number:          2,
		Type:            block.TxBlockType,
		CreateTime:      time.Unix(15, 0),
	}, block.Body{TxList: txs}, block.MigrationOpt{})
}

func receiptTestIntent() intent.PaymentIntent {
	var sender, recipient account.Address
	sender[0] = 1
	recipient[0] = 2

	return intent.PaymentIntent{
		Version:          intent.CurrentVersion,
		ChainID:          11,
		Sender:           sender,
		Recipient:        recipient,
		SourceShard:      0,
		DestinationShard: 1,
		Amount:           big.NewInt(10),
		ExpiryEpoch:      10,
	}
}

type recordingConn struct {
	destinations []nodetopo.NodeInfo
	messages     []*rpcserver.WrappedMsg
}

func newRecordingConn() *recordingConn {
	return &recordingConn{}
}

func (r *recordingConn) ListenStart() error {
	return nil
}

func (r *recordingConn) DrainMsgBuffer() []*rpcserver.WrappedMsg {
	return nil
}

func (r *recordingConn) SendMsg2Dest(
	_ context.Context,
	destination nodetopo.NodeInfo,
	wrapped *rpcserver.WrappedMsg,
) {
	r.destinations = append(r.destinations, destination)
	r.messages = append(r.messages, wrapped)
}

func (r *recordingConn) Close() {}
