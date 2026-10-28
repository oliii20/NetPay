package txblockop

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/receipt"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
)

func (r *RelayTxBlockOp) deliverAdaptive(ctx context.Context, b *block.Block, committedAt time.Time) error {
	policy := r.routes.Snapshot()

	finalized, relay, err := receipt.BuildRouted(r.lp.ShardID, r.c.GetEpochID(), r.cfg.ChainID, r.cfg.ShardNum,
		b, committedAt, policy, r.cfg.AggregationCfg.SourceAggregateAbstracts)
	if err != nil {
		return fmt.Errorf("route finalized source block: %w", err)
	}

	if err = r.receipts.PublishReceipt(ctx, finalized); err != nil {
		return err
	}

	if err = r.completion.PublishCommittedBlock(ctx, r.lp.ShardID, b, committedAt); err != nil {
		return err
	}

	if err = r.metrics.PublishCommittedBlock(ctx, r.lp.ShardID, b, committedAt); err != nil {
		slog.WarnContext(ctx, "publish aggregation execution metrics failed", "err", err)
	}

	selected := make(map[paymentmodel.ID]bool)

	for _, p := range finalized.Payments {
		ids, idErr := p.LogicalIDs()
		if idErr != nil {
			return idErr
		}

		for _, id := range ids {
			selected[id] = true
		}
	}

	records := make([]model.AggregationExecutionMetric, 0, len(selected))
	for _, tx := range b.TxList {
		if len(tx.ROriginalHash) != len(paymentmodel.ID{}) {
			continue
		}

		var id paymentmodel.ID
		copy(id[:], tx.ROriginalHash)

		if !selected[id] {
			continue
		}

		records = append(records, model.AggregationExecutionMetric{
			Phase: model.MetricPhaseSourceDebit, PaymentID: id, ShardID: r.lp.ShardID,
			ItemCount: 1, UniqueAccountCount: 1, CreatedAt: tx.CreateTime, CommittedAt: committedAt,
			StateReadCount: 1, StateWriteCount: 1,
		})
	}

	if err = r.metrics.PublishSourceMetrics(ctx, records); err != nil {
		slog.WarnContext(ctx, "publish routed source metrics failed", "err", err)
	}

	inner, _, relay2 := r.splitTxs(ctx, b.TxList)
	if err = r.deliverBlockInfo2Supervisor(ctx, inner, relay, relay2, *b); err != nil {
		return err
	}

	if err = r.sendRelayedTxs(ctx, b, relay); err != nil {
		return err
	}

	if len(records)+len(relay) > 0 {
		zeroValueRelay := 0

		for _, tx := range relay {
			if tx.Value != nil && tx.Value.Sign() == 0 {
				zeroValueRelay++
			}
		}

		slog.InfoContext(ctx, "source block routed after commitment", "height", b.Number,
			"routing_version", policy.Version, "aggregation_count", len(records), "relay_count", len(relay),
			"zero_value_relay_count", zeroValueRelay)
	}

	return nil
}
