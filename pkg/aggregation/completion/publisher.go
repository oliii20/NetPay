// Package completion publishes destination-shard settlement progress.
package completion

import (
	"context"
	"fmt"
	"time"

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

func (p *Publisher) PublishCommittedBlock(
	ctx context.Context,
	shardID int64,
	committed *block.Block,
	committedAt time.Time,
) error {
	if !p.enabled || p.nodeID != 0 {
		return nil
	}

	chunks := appliedChunks(shardID, committed, committedAt)
	if len(chunks) == 0 {
		return nil
	}

	wrapper, err := message.WrapMsg(&message.AggregationAppliedMsg{NodeID: p.nodeID, Chunks: chunks})
	if err != nil {
		return fmt.Errorf("wrap aggregation completion: %w", err)
	}

	supervisor, err := p.resolver.GetSupervisor()
	if err != nil {
		return fmt.Errorf("resolve aggregation completion supervisor: %w", err)
	}

	p.sender.SendMsg2Dest(ctx, supervisor, wrapper)

	return nil
}

func appliedChunks(shardID int64, committed *block.Block, committedAt time.Time) []message.AggregationApplied {
	chunks := make([]message.AggregationApplied, 0)

	for idx := range committed.TxList {
		tx := &committed.TxList[idx]
		if tx.TxType() != transaction.AggregationSettlementTxType || tx.Aggregation == nil {
			continue
		}

		pack := tx.Aggregation.Aggregation

		receiptCount := 0
		for _, credit := range pack.Credits {
			receiptCount += int(credit.ReceiptCount)
		}

		chunks = append(chunks, message.AggregationApplied{
			BatchID: pack.BatchID, WindowID: pack.WindowID, ShardID: shardID,
			ChunkIndex: pack.ChunkIndex, ReceiptCount: receiptCount, CommittedAt: committedAt,
		})
	}

	return chunks
}
