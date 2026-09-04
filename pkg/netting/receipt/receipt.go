// Package receipt builds and publishes source-shard finalized block receipts.
package receipt

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

var (
	ErrNotTransactionBlock = errors.New("cannot build receipt for non-transaction block")
	ErrInvalidBlockHash    = errors.New("invalid block hash length")
	ErrInvalidParentHash   = errors.New("invalid parent hash length")
	ErrInvalidStateRoot    = errors.New("invalid state root length")
	ErrIntentSource        = errors.New("receipt contains intent from another source shard")
)

type Publisher struct {
	enabled  bool
	nodeID   int64
	sender   network.P2PConn
	resolver nodetopo.NodeMapper
}

func NewPublisher(
	enabled bool,
	nodeID int64,
	sender network.P2PConn,
	resolver nodetopo.NodeMapper,
) *Publisher {
	return &Publisher{enabled: enabled, nodeID: nodeID, sender: sender, resolver: resolver}
}

func Build(shardID, epoch int64, b *block.Block, commitTime time.Time) (model.FinalizedBlockReceipt, error) {
	if b.Type != block.TxBlockType {
		return model.FinalizedBlockReceipt{}, fmt.Errorf("%w: %d", ErrNotTransactionBlock, b.Type)
	}

	blockHashBytes, err := b.Hash()
	if err != nil {
		return model.FinalizedBlockReceipt{}, fmt.Errorf("calculate block hash: %w", err)
	}
	blockHash, err := toHash(blockHashBytes, ErrInvalidBlockHash)
	if err != nil {
		return model.FinalizedBlockReceipt{}, err
	}
	parentHash, err := toHash(b.ParentBlockHash, ErrInvalidParentHash)
	if err != nil {
		return model.FinalizedBlockReceipt{}, err
	}
	stateRoot, err := toHash(b.StateRoot, ErrInvalidStateRoot)
	if err != nil {
		return model.FinalizedBlockReceipt{}, err
	}

	intents := make([]intent.PaymentIntent, 0)
	for idx := range b.TxList {
		tx := &b.TxList[idx]
		if tx.TxType() != transaction.IntentSubmitTxType {
			continue
		}
		if tx.Intent.SourceShard != shardID {
			return model.FinalizedBlockReceipt{}, fmt.Errorf(
				"%w: got %d, want %d",
				ErrIntentSource,
				tx.Intent.SourceShard,
				shardID,
			)
		}
		if _, err = tx.Intent.ID(); err != nil {
			return model.FinalizedBlockReceipt{}, fmt.Errorf("calculate intent ID: %w", err)
		}
		intents = append(intents, tx.Intent.Clone())
	}

	return model.FinalizedBlockReceipt{
		ShardID:    shardID,
		Height:     b.Number,
		BlockHash:  blockHash,
		ParentHash: parentHash,
		StateRoot:  stateRoot,
		Epoch:      epoch,
		Intents:    intents,
		CommitTime: commitTime,
	}, nil
}

func (p *Publisher) Publish(
	ctx context.Context,
	shardID, epoch int64,
	b *block.Block,
	commitTime time.Time,
) error {
	if !p.enabled || p.nodeID != 0 {
		return nil
	}

	finalized, err := Build(shardID, epoch, b, commitTime)
	if err != nil {
		return fmt.Errorf("build finalized block receipt: %w", err)
	}
	wrapped, err := message.WrapMsg(&message.FinalizedBlockReceiptMsg{
		NodeID:  p.nodeID,
		Receipt: finalized,
	})
	if err != nil {
		return fmt.Errorf("wrap finalized block receipt: %w", err)
	}
	solver, err := p.resolver.GetSolver()
	if err != nil {
		return fmt.Errorf("resolve solver: %w", err)
	}
	p.sender.SendMsg2Dest(ctx, solver, wrapped)

	return nil
}

func toHash(value []byte, kind error) (merkle.Hash, error) {
	if len(value) != len(merkle.Hash{}) {
		return merkle.Hash{}, fmt.Errorf("%w: got %d, want %d", kind, len(value), len(merkle.Hash{}))
	}
	var hash merkle.Hash
	copy(hash[:], value)

	return hash, nil
}
