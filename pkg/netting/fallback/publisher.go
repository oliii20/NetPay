package fallback

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

const republishInterval = 5 * time.Second

const maxFallbackPublishesPerBlock = 8192

type PendingReader interface {
	GetPendingFallbacks(context.Context) ([]model.ReservedFallback, error)
}

type Publisher struct {
	enabled  bool
	nodeID   int64
	chain    PendingReader
	conn     *network.ConnHandler
	resolver nodetopo.NodeMapper
	lastSent map[model.FallbackKey]time.Time
	lastScan time.Time
}

func NewPublisher(
	enabled bool,
	nodeID int64,
	chain PendingReader,
	conn *network.ConnHandler,
	resolver nodetopo.NodeMapper,
) *Publisher {
	return &Publisher{
		enabled: enabled, nodeID: nodeID, chain: chain, conn: conn, resolver: resolver,
		lastSent: make(map[model.FallbackKey]time.Time),
	}
}

func (p *Publisher) PublishAfterBlock(ctx context.Context, committed *block.Block) error {
	if !p.enabled || p.nodeID != 0 {
		return nil
	}
	if err := p.publishCompletions(ctx, committed); err != nil {
		return err
	}
	if err := p.publishProgress(ctx, committed); err != nil {
		return err
	}
	now := time.Now()
	published, err := p.publishNewFallbacks(ctx, committed, now)
	if err != nil {
		return err
	}
	if published > 0 && p.lastScan.IsZero() {
		p.lastScan = now
	}
	if !p.shouldScan(now) {
		return nil
	}
	items, err := p.chain.GetPendingFallbacks(ctx)
	if err != nil {
		return err
	}
	p.lastScan = now
	for _, item := range items {
		if published >= maxFallbackPublishesPerBlock {
			break
		}
		sent, sendErr := p.publishFallback(ctx, item, now)
		if sendErr != nil {
			return sendErr
		}
		if sent {
			published++
		}
	}

	return nil
}

func (p *Publisher) shouldScan(now time.Time) bool {
	return p.lastScan.IsZero() || now.Sub(p.lastScan) >= republishInterval
}

func (p *Publisher) publishNewFallbacks(ctx context.Context, committed *block.Block, now time.Time) (int, error) {
	published := 0
	for idx := range committed.TxList {
		tx := &committed.TxList[idx]
		if tx.Settlement == nil {
			continue
		}
		for _, result := range tx.Settlement.Settlement.Outgoing {
			if result.FallbackAmount == nil || result.FallbackAmount.Sign() <= 0 {
				continue
			}
			if published >= maxFallbackPublishesPerBlock {
				return published, nil
			}
			sent, err := p.publishFallback(ctx, reservedFallbackFromSettlement(*tx.Settlement, result), now)
			if err != nil {
				return published, err
			}
			if sent {
				published++
			}
		}
	}

	return published, nil
}

func reservedFallbackFromSettlement(
	pack model.SettlementPackage,
	result model.IntentResult,
) model.ReservedFallback {
	return model.ReservedFallback{
		IntentID:         result.IntentID,
		BatchID:          pack.Settlement.BatchID,
		Sender:           result.Intent.Sender,
		Recipient:        result.Intent.Recipient,
		SourceShard:      result.Intent.SourceShard,
		DestinationShard: result.Intent.DestinationShard,
		Amount:           new(big.Int).Set(result.FallbackAmount),
		Proof:            pack.Clone(),
	}
}

func (p *Publisher) publishFallback(ctx context.Context, item model.ReservedFallback, now time.Time) (bool, error) {
	key := item.Key()
	if sentAt, ok := p.lastSent[key]; ok && now.Sub(sentAt) < republishInterval {
		return false, nil
	}
	destination, err := p.resolver.GetLeader(item.DestinationShard)
	if err != nil {
		return false, fmt.Errorf("resolve fallback destination shard %d: %w", item.DestinationShard, err)
	}
	wrapped, err := message.WrapMsg(&message.FallbackTxMsg{NodeID: p.nodeID, Fallback: item.Clone()})
	if err != nil {
		return false, fmt.Errorf("wrap reserved fallback: %w", err)
	}
	p.conn.SendMsg2Dest(ctx, destination, wrapped)
	p.lastSent[key] = now

	return true, nil
}

func (p *Publisher) publishProgress(ctx context.Context, committed *block.Block) error {
	completed := make([]intent.ID, 0)
	for idx := range committed.TxList {
		tx := &committed.TxList[idx]
		if tx.ReservedFallback != nil {
			completed = append(completed, tx.ReservedFallback.IntentID)
			continue
		}
		if tx.Settlement == nil {
			continue
		}
		for _, result := range tx.Settlement.Settlement.Outgoing {
			if result.FallbackAmount.Sign() == 0 {
				completed = append(completed, result.IntentID)
			}
		}
		for _, result := range tx.Settlement.Settlement.FallbackIncoming {
			completed = append(completed, result.IntentID)
		}
	}
	if len(completed) == 0 {
		return nil
	}
	wrapper, err := message.WrapMsg(&message.NettingProgressMsg{NodeID: p.nodeID, IntentIDs: completed})
	if err != nil {
		return fmt.Errorf("wrap netting progress: %w", err)
	}
	supervisor, err := p.resolver.GetSupervisor()
	if err != nil {
		return fmt.Errorf("resolve netting progress supervisor: %w", err)
	}
	p.conn.SendMsg2Dest(ctx, supervisor, wrapper)

	return nil
}

func (p *Publisher) publishCompletions(ctx context.Context, committed *block.Block) error {
	for idx := range committed.TxList {
		tx := &committed.TxList[idx]
		if tx.FallbackCompleted != nil {
			delete(p.lastSent, *tx.FallbackCompleted)
			continue
		}
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
