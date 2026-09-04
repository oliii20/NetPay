package fallback

import (
	"context"
	"fmt"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

type PendingReader interface {
	GetPendingFallbacks(context.Context) ([]model.ReservedFallback, error)
}

type Publisher struct {
	enabled  bool
	nodeID   int64
	chain    PendingReader
	conn     *network.ConnHandler
	resolver nodetopo.NodeMapper
}

func NewPublisher(
	enabled bool,
	nodeID int64,
	chain PendingReader,
	conn *network.ConnHandler,
	resolver nodetopo.NodeMapper,
) *Publisher {
	return &Publisher{enabled: enabled, nodeID: nodeID, chain: chain, conn: conn, resolver: resolver}
}

func (p *Publisher) PublishAfterBlock(ctx context.Context, committed *block.Block) error {
	if !p.enabled || p.nodeID != 0 {
		return nil
	}
	if err := p.publishCompletions(ctx, committed); err != nil {
		return err
	}
	items, err := p.chain.GetPendingFallbacks(ctx)
	if err != nil {
		return err
	}
	for _, item := range items {
		destination, resolveErr := p.resolver.GetLeader(item.DestinationShard)
		if resolveErr != nil {
			return fmt.Errorf("resolve fallback destination shard %d: %w", item.DestinationShard, resolveErr)
		}
		wrapped, wrapErr := message.WrapMsg(&message.FallbackTxMsg{NodeID: p.nodeID, Fallback: item})
		if wrapErr != nil {
			return fmt.Errorf("wrap reserved fallback: %w", wrapErr)
		}
		p.conn.SendMsg2Dest(ctx, destination, wrapped)
	}

	return nil
}

func (p *Publisher) publishCompletions(ctx context.Context, committed *block.Block) error {
	for idx := range committed.TxList {
		tx := &committed.TxList[idx]
		if tx.ReservedFallback == nil {
			continue
		}
		item := tx.ReservedFallback
		wrapped, err := message.WrapMsg(&message.FallbackCompletedMsg{
			NodeID: p.nodeID, SourceShard: item.SourceShard, Key: item.Key(),
		})
		if err != nil {
			return fmt.Errorf("wrap fallback completion: %w", err)
		}
		source, err := p.resolver.GetLeader(item.SourceShard)
		if err != nil {
			return fmt.Errorf("resolve fallback source shard %d: %w", item.SourceShard, err)
		}
		p.conn.SendMsg2Dest(ctx, source, wrapped)
		if supervisor, resolveErr := p.resolver.GetSupervisor(); resolveErr == nil {
			p.conn.SendMsg2Dest(ctx, supervisor, wrapped)
		}
	}

	return nil
}
