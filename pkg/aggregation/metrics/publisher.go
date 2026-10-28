// Package metrics publishes aggregation protocol measurements without putting
// them on the consensus-critical path.
package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

type Publisher struct {
	enabled  bool
	nodeID   int64
	sender   network.P2PConn
	resolver nodetopo.NodeMapper
}

func NewPublisher(enabled bool, nodeID int64, sender network.P2PConn, resolver nodetopo.NodeMapper) *Publisher {
	return &Publisher{enabled: enabled, nodeID: nodeID, sender: sender, resolver: resolver}
}

func (p *Publisher) PublishBatch(ctx context.Context, metric model.AggregationBatchMetric) error {
	return p.publish(ctx, &message.AggregationBatchMetricMsg{NodeID: p.nodeID, Metric: metric})
}

func (p *Publisher) PublishQueue(ctx context.Context, metric model.AggregationQueueMetric) error {
	return p.publish(ctx, &message.AggregationQueueMetricMsg{NodeID: p.nodeID, Metric: metric})
}

func (p *Publisher) PublishBeacon(ctx context.Context, metric model.AggregationBeaconMetric) error {
	return p.publish(ctx, &message.AggregationBeaconMetricMsg{NodeID: p.nodeID, Metric: metric})
}

func (p *Publisher) PublishSourceMetrics(ctx context.Context, records []model.AggregationExecutionMetric) error {
	if len(records) == 0 {
		return nil
	}

	return p.publish(ctx, &message.AggregationExecutionMetricMsg{NodeID: p.nodeID, Metrics: records})
}

func (p *Publisher) PublishCommittedBlock(
	ctx context.Context,
	shardID int64,
	committed *block.Block,
	committedAt time.Time,
) error {
	if !p.enabled || p.nodeID != 0 {
		return nil
	}

	measurements, settlementTxs := blockMeasurements(shardID, committed, committedAt)
	if len(measurements) == 0 && len(settlementTxs) == 0 {
		return nil
	}

	return p.publish(ctx, &message.AggregationExecutionMetricMsg{
		NodeID: p.nodeID, Metrics: measurements, SettlementTxs: settlementTxs,
	})
}

func (p *Publisher) publish(ctx context.Context, payload any) error {
	if !p.enabled || p.nodeID != 0 {
		return nil
	}

	wrapper, err := message.WrapMsg(payload)
	if err != nil {
		return fmt.Errorf("wrap aggregation metric: %w", err)
	}

	supervisor, err := p.resolver.GetSupervisor()
	if err != nil {
		return fmt.Errorf("resolve metrics supervisor: %w", err)
	}

	p.sender.SendMsg2Dest(ctx, supervisor, wrapper)

	return nil
}

func blockMeasurements(
	shardID int64,
	committed *block.Block,
	committedAt time.Time,
) ([]model.AggregationExecutionMetric, []model.SettlementTxMetric) {
	measurements := make([]model.AggregationExecutionMetric, 0)
	settlementTxs := make([]model.SettlementTxMetric, 0)

	for idx := range committed.TxList {
		tx := &committed.TxList[idx]
		switch tx.TxType() {
		case transaction.CrossShardPaymentTxType:
			paymentID, err := tx.Payment.ID()
			if err != nil {
				continue
			}

			measurements = append(measurements, model.AggregationExecutionMetric{
				Phase: model.MetricPhaseSourceDebit, PaymentID: paymentID, ShardID: shardID, ItemCount: 1,
				UniqueAccountCount: 1, CreatedAt: tx.CreateTime, CommittedAt: committedAt,
				StateReadCount: 2, StateWriteCount: 2,
			})
		case transaction.AggregationSettlementTxType:
			if tx.Aggregation == nil {
				continue
			}

			aggregation := tx.Aggregation.Aggregation

			receiptCount := 0
			accounts := make(map[[20]byte]struct{})

			for _, credit := range aggregation.Credits {
				receiptCount += int(credit.ReceiptCount)
				accounts[[20]byte(credit.Recipient)] = struct{}{}
			}

			measurements = append(measurements, model.AggregationExecutionMetric{
				Phase: model.MetricPhaseSettlement, BatchID: aggregation.BatchID,
				WindowID: aggregation.WindowID, ShardID: shardID, ChunkIndex: aggregation.ChunkIndex,
				ItemCount: receiptCount,
				ReceiverUpdateCount: len(
					aggregation.Credits,
				), UniqueAccountCount: len(accounts), CreatedAt: tx.CreateTime,
				CommittedAt: committedAt, StateReadCount: 1,
				StateWriteCount: int64(len(aggregation.Credits) + 1),
			})
			settlementTxs = append(settlementTxs, model.SettlementTxMetric{
				BatchID: aggregation.BatchID, WindowID: aggregation.WindowID,
				ShardID: shardID, ChunkIndex: aggregation.ChunkIndex,
				CreditCount: len(aggregation.Credits),
			})
		}
	}

	return measurements, settlementTxs
}
