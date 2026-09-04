// Package metrics publishes protocol measurements without putting them on the
// consensus-critical path.
package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
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

func (p *Publisher) PublishBatch(ctx context.Context, metric model.NettingBatchMetric) error {
	return p.publish(ctx, &message.NettingBatchMetricMsg{NodeID: p.nodeID, Metric: metric})
}

func (p *Publisher) PublishBeacon(ctx context.Context, metric model.NettingBeaconMetric) error {
	return p.publish(ctx, &message.NettingBeaconMetricMsg{NodeID: p.nodeID, Metric: metric})
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
	measurements := blockMeasurements(shardID, committed, committedAt)
	if len(measurements) == 0 {
		return nil
	}

	return p.publish(ctx, &message.NettingExecutionMetricMsg{NodeID: p.nodeID, Metrics: measurements})
}

func (p *Publisher) publish(ctx context.Context, payload any) error {
	if !p.enabled || p.nodeID != 0 {
		return nil
	}
	wrapper, err := message.WrapMsg(payload)
	if err != nil {
		return fmt.Errorf("wrap netting metric: %w", err)
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
) []model.NettingExecutionMetric {
	measurements := make([]model.NettingExecutionMetric, 0)
	for idx := range committed.TxList {
		tx := &committed.TxList[idx]
		switch tx.TxType() {
		case transaction.IntentSubmitTxType:
			id, err := tx.Intent.ID()
			if err != nil {
				continue
			}
			measurements = append(measurements, model.NettingExecutionMetric{
				Phase: model.MetricPhaseReservation, IntentID: id, ShardID: shardID,
				CreatedAt: tx.CreateTime, CommittedAt: committedAt,
				StateReadCount: 3, StateWriteCount: 7,
			})
		case transaction.SettlementTxType:
			measurements = append(measurements, settlementMeasurements(shardID, tx, committedAt)...)
		case transaction.ReservedFallbackTxType:
			if tx.ReservedFallback == nil {
				continue
			}
			started := time.Now()
			_ = batch.VerifySettlementPackage(tx.ReservedFallback.Proof)
			measurements = append(measurements, model.NettingExecutionMetric{
				Phase: model.MetricPhaseFallback, BatchID: tx.ReservedFallback.BatchID,
				WindowID: tx.ReservedFallback.Proof.Header.WindowID, IntentID: tx.ReservedFallback.IntentID,
				ShardID: shardID, CommittedAt: committedAt, Final: true,
				StateReadCount: 2, StateWriteCount: 2, ProofVerificationTime: time.Since(started),
			})
		}
	}

	return measurements
}

func settlementMeasurements(
	shardID int64,
	tx *transaction.Transaction,
	committedAt time.Time,
) []model.NettingExecutionMetric {
	if tx.Settlement == nil {
		return nil
	}
	started := time.Now()
	_ = batch.VerifySettlementPackage(*tx.Settlement)
	proofTime := time.Since(started)
	settlement := tx.Settlement.Settlement
	measurements := make([]model.NettingExecutionMetric, 0, len(settlement.Outgoing))
	fallbackCount := int64(0)
	for _, result := range settlement.Outgoing {
		if result.FallbackAmount.Sign() > 0 {
			fallbackCount++
		}
	}
	reads := int64(1 + 3*len(settlement.Outgoing) + len(settlement.Incoming))
	writes := int64(1+4*len(settlement.Outgoing)+2*len(settlement.Incoming)) + 4*fallbackCount
	for idx, result := range settlement.Outgoing {
		metric := model.NettingExecutionMetric{
			Phase: model.MetricPhaseSettlement, BatchID: settlement.BatchID,
			WindowID: settlement.WindowID, IntentID: result.IntentID, ShardID: shardID,
			CommittedAt: committedAt, Final: result.FallbackAmount.Sign() == 0,
		}
		if idx == 0 {
			metric.StateReadCount = reads
			metric.StateWriteCount = writes
			metric.ProofVerificationTime = proofTime
		}
		measurements = append(measurements, metric)
	}

	return measurements
}
