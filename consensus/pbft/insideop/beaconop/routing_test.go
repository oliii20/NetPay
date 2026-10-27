package beaconop

import (
	"bytes"
	"context"
	"encoding/gob"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/config"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/beacon"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/routing"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/window"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

type historicalClock struct{ at time.Time }

func (c historicalClock) Now() time.Time { return c.at }

func TestNewPolicyCannotDiscardOldFrozenPayments(t *testing.T) {
	store, err := beacon.OpenStore(filepath.Join(t.TempDir(), "beacon.db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	topo := nodetopo.NewTopoGetter(nil, nil)
	require.NoError(t, topo.SetTopoGetter(map[int64]map[int64]string{0: {0: "", 1: ""}, 1: {0: "", 1: ""}, nodetopo.BeaconShardID: {0: ""}}))
	transport := &recordingTransport{}
	cfg := config.AggregationCfg{CreditChunkSize: 512, AdaptiveRouting: routing.Config{Enabled: true}}
	op := New(network.NewConnHandler(transport), topo, store, 2, 11, 0, cfg)
	clock := historicalClock{at: time.Now().Add(-3 * time.Second)}
	op.manager, err = window.New(window.Config{ShardCount: 2, BatchSize: 1, MaxWindowDuration: time.Minute}, clock,
		map[int64]model.ShardCheckpoint{0: {}, 1: {}})
	require.NoError(t, err)
	payment := paymentmodel.CrossShardPayment{Version: paymentmodel.CurrentVersion, ChainID: 11,
		SourceShard: 1, DestinationShard: 0, Recipient: account.Address{9}, Amount: big.NewInt(7),
		SourceAggregated: true, OriginalIDs: []paymentmodel.ID{{3}}}
	receipt := model.FinalizedBlockReceipt{ShardID: 1, Height: 1, Payments: []paymentmodel.CrossShardPayment{payment},
		DestinationStats: []model.DestinationStat{{ShardID: 0, PaymentCount: 1, RecipientCount: 1}}}
	_, err = op.manager.AddReceipt(receipt)
	require.NoError(t, err)
	require.NoError(t, op.routing.Observe(receipt, time.Now()))
	require.NoError(t, op.routing.Observe(model.FinalizedBlockReceipt{ShardID: 0, Height: 1,
		DestinationStats: []model.DestinationStat{{ShardID: 1, PaymentCount: 100, RecipientCount: 1}}}, time.Now()))
	require.NoError(t, op.updateRouting(context.Background()))
	require.Equal(t, []int64{1}, op.routing.Current().Destinations)
	policyMessages := 0
	for _, sent := range transport.records {
		if sent.msg.GetMsgType() == message.AggregationRoutingPolicyMessageType {
			policyMessages++
			var msg message.AggregationRoutingPolicyMsg
			require.NoError(t, gob.NewDecoder(bytes.NewReader(sent.msg.GetPayload())).Decode(&msg))
			require.Equal(t, []int64{1}, msg.Policy.Destinations)
		}
	}
	require.Equal(t, 5, policyMessages, "four ordinary replicas and Supervisor")
	proposal, err := op.BuildProposal(context.Background())
	require.NoError(t, err)
	require.NotNil(t, proposal)
	require.NoError(t, op.ProposalCommitAndDeliver(context.Background(), true, proposal))
	packages := 0
	for _, sent := range transport.records {
		if sent.msg.GetMsgType() == message.AggregationPackageMessageType {
			packages++
			require.Equal(t, int64(0), sent.dest.ShardID, "old destination must still be paid")
		}
	}
	require.Equal(t, 1, packages)
}
