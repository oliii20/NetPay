package beaconop

import (
	"bytes"
	"context"
	"encoding/gob"
	"math/big"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/config"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/beacon"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/window"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
	"github.com/stretchr/testify/require"
)

type dispatchRecord struct {
	dest nodetopo.NodeInfo
	msg  *rpcserver.WrappedMsg
}
type recordingTransport struct {
	mux     sync.Mutex
	records []dispatchRecord
}

func (*recordingTransport) ListenStart() error                      { return nil }
func (*recordingTransport) DrainMsgBuffer() []*rpcserver.WrappedMsg { return nil }
func (*recordingTransport) Close()                                  {}
func (r *recordingTransport) SendMsg2Dest(_ context.Context, dest nodetopo.NodeInfo, msg *rpcserver.WrappedMsg) {
	r.mux.Lock()
	defer r.mux.Unlock()
	r.records = append(r.records, dispatchRecord{dest, msg})
}
func TestFinalizationSendsDetailsOnlyToSupervisor(t *testing.T) {
	for _, batchOnly := range []bool{false, true} {
		name := "full"
		if batchOnly {
			name = "batch_only"
		}
		t.Run(name, func(t *testing.T) {
			store, err := beacon.OpenStore(filepath.Join(t.TempDir(), "beacon.db"))
			require.NoError(t, err)
			defer store.Close()
			topo := nodetopo.NewTopoGetter(nil, nil)
			require.NoError(t, topo.SetTopoGetter(map[int64]map[int64]string{0: {0: "", 1: ""}, 1: {0: "", 1: ""}, nodetopo.BeaconShardID: {0: ""}}))
			transport := &recordingTransport{}
			op := New(network.NewConnHandler(transport), topo, store, 2, 11, 0, config.AggregationCfg{})
			payment := paymentmodel.CrossShardPayment{Amount: big.NewInt(7), SourceShard: 0, DestinationShard: 1}
			frozen := window.FrozenWindow{WindowID: 1, Cuts: []model.ShardCut{{ShardID: 0, EndHeight: 1}, {ShardID: 1}}, Payments: []paymentmodel.CrossShardPayment{payment}, Receipts: []model.FinalizedBlockReceipt{{ShardID: 0, Height: 1, Payments: []paymentmodel.CrossShardPayment{payment}}}}
			proposal, err := (batch.Builder{BatchOnly: batchOnly, CreditChunkSize: 512}).Build(frozen, merkle.Hash{})
			require.NoError(t, err)
			op.receivedAt[proposal.Header.BatchID] = time.Now()
			op.prepared[proposal.Header.BatchID] = proposal.Clone()
			require.NoError(t, op.ProposalCommitAndDeliver(context.Background(), true, message.WrapAggregationHeaderProposal(proposal.Header)))
			headers, details, packages := 0, 0, 0
			for _, record := range transport.records {
				switch record.msg.GetMsgType() {
				case message.AggregationFinalizedMessageType:
					var msg message.AggregationFinalizedMsg
					require.NoError(t, gob.NewDecoder(bytes.NewReader(record.msg.GetPayload())).Decode(&msg))
					require.Equal(t, proposal.Header.BatchID, msg.Header.BatchID)
					if record.dest.ShardID == nodetopo.SupervisorShardID {
						details++
						require.Len(t, msg.PaymentIDs, 1)
						require.Len(t, msg.Settlements, 1)
					} else {
						headers++
						require.Empty(t, msg.PaymentIDs)
						require.Empty(t, msg.Settlements)
					}
				case message.AggregationPackageMessageType:
					packages++
				}
			}
			require.Equal(t, 4, headers)
			require.Equal(t, 1, details)
			require.Equal(t, 1, packages)
		})
	}
}
