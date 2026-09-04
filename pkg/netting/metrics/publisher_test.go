package metrics_test

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
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/metrics"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/window"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

func TestPublisherReportsCommittedLifecyclePhases(t *testing.T) {
	t.Parallel()

	forward := metricPayment(0, 1, 10, 1)
	reverse := metricPayment(1, 0, 7, 2)
	proposal, err := (batch.Builder{}).Build(window.FrozenWindow{
		WindowID: 1, Cuts: []model.ShardCut{{ShardID: 0}, {ShardID: 1}},
		Intents: []intent.PaymentIntent{forward, reverse},
	}, merkle.Hash{})
	require.NoError(t, err)
	packages, err := batch.BuildSettlementPackages(proposal)
	require.NoError(t, err)
	pack := packages[0]
	fallbackItem := model.ReservedFallback{
		IntentID: pack.Settlement.Outgoing[0].IntentID, BatchID: proposal.Header.BatchID,
		SourceShard: 0, DestinationShard: 1, Amount: big.NewInt(3), Proof: pack,
	}
	createdAt := time.Unix(10, 0)
	committedAt := time.Unix(20, 0)
	committed := &block.Block{Body: block.Body{TxList: []transaction.Transaction{
		*transaction.NewIntentTransaction(forward, createdAt),
		*transaction.NewSettlementTransaction(pack, createdAt),
		*transaction.NewReservedFallbackTransaction(fallbackItem, createdAt),
	}}}
	conn := &metricP2P{}
	publisher := metrics.NewPublisher(true, 0, conn, metricResolver{})
	require.NoError(t, publisher.PublishCommittedBlock(context.Background(), 0, committed, committedAt))
	require.Len(t, conn.messages, 1)
	require.Equal(t, message.NettingExecutionMetricMessageType, conn.messages[0].GetMsgType())

	var payload message.NettingExecutionMetricMsg
	require.NoError(t, gob.NewDecoder(bytes.NewReader(conn.messages[0].GetPayload())).Decode(&payload))
	require.Len(t, payload.Metrics, 3)
	require.Equal(t, model.MetricPhaseReservation, payload.Metrics[0].Phase)
	require.Equal(t, createdAt, payload.Metrics[0].CreatedAt)
	require.Equal(t, model.MetricPhaseSettlement, payload.Metrics[1].Phase)
	require.False(t, payload.Metrics[1].Final)
	require.Positive(t, payload.Metrics[1].ProofVerificationTime)
	require.Equal(t, model.MetricPhaseFallback, payload.Metrics[2].Phase)
	require.True(t, payload.Metrics[2].Final)
	require.Positive(t, payload.Metrics[2].StateWriteCount)
}

func metricPayment(source, destination, amount, nonce int64) intent.PaymentIntent {
	var sender, recipient account.Address
	sender[0] = byte(source + 1)
	recipient[0] = byte(destination + 11)

	return intent.PaymentIntent{
		Version: intent.CurrentVersion, ChainID: 1, Sender: sender, Recipient: recipient,
		SourceShard: source, DestinationShard: destination, Amount: big.NewInt(amount),
		Nonce: uint64(nonce), ExpiryEpoch: 100,
	}
}

type metricP2P struct {
	messages []*rpcserver.WrappedMsg
}

func (*metricP2P) ListenStart() error                      { return nil }
func (*metricP2P) DrainMsgBuffer() []*rpcserver.WrappedMsg { return nil }
func (p *metricP2P) SendMsg2Dest(_ context.Context, _ nodetopo.NodeInfo, msg *rpcserver.WrappedMsg) {
	p.messages = append(p.messages, msg)
}
func (*metricP2P) Close() {}

type metricResolver struct{}

func (metricResolver) SetTopoGetter(map[int64]map[int64]string) error     { return nil }
func (metricResolver) GetNodesInShard(int64) ([]nodetopo.NodeInfo, error) { return nil, nil }
func (metricResolver) GetLeader(int64) (nodetopo.NodeInfo, error)         { return nodetopo.NodeInfo{}, nil }
func (metricResolver) ChangeLeader(int64, nodetopo.NodeInfo) error        { return nil }
func (metricResolver) GetAllLeaders() ([]nodetopo.NodeInfo, error)        { return nil, nil }
func (metricResolver) GetSupervisor() (nodetopo.NodeInfo, error) {
	return nodetopo.NodeInfo{ShardID: nodetopo.SupervisorShardID}, nil
}
func (metricResolver) GetSolver() (nodetopo.NodeInfo, error) { return nodetopo.NodeInfo{}, nil }
