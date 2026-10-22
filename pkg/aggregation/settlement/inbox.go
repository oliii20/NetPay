package settlement

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
)

var (
	ErrPackageWrongShard   = errors.New("settlement package belongs to another shard")
	ErrPackageRootMismatch = errors.New("settlement package does not match confirmed root")
)

type RootConfirmer interface {
	ConfirmAggregationRoot(context.Context, model.AggregationBatchHeader) error
}

type TransactionAdder interface {
	AddTxs([]transaction.Transaction) error
}

type packageKey struct {
	BatchID    merkle.Hash
	ChunkIndex uint32
}

type Inbox struct {
	shardID   int64
	confirmer RootConfirmer
	txPool    TransactionAdder
	confirmed map[merkle.Hash]model.AggregationBatchHeader
	pending   map[packageKey]model.AggregationPackage
	enqueued  map[packageKey]struct{}
}

func NewInbox(shardID int64, confirmer RootConfirmer, txPool TransactionAdder) *Inbox {
	return &Inbox{
		shardID: shardID, confirmer: confirmer, txPool: txPool,
		confirmed: make(map[merkle.Hash]model.AggregationBatchHeader),
		pending:   make(map[packageKey]model.AggregationPackage),
		enqueued:  make(map[packageKey]struct{}),
	}
}

func (i *Inbox) Confirm(ctx context.Context, header model.AggregationBatchHeader) error {
	if err := i.confirmer.ConfirmAggregationRoot(ctx, header); err != nil {
		return err
	}

	i.confirmed[header.BatchID] = header
	keys := make([]packageKey, 0)

	for key := range i.pending {
		if key.BatchID == header.BatchID {
			keys = append(keys, key)
		}
	}

	sort.Slice(keys, func(left, right int) bool { return keys[left].ChunkIndex < keys[right].ChunkIndex })

	for _, key := range keys {
		if err := i.enqueue(i.pending[key]); err != nil {
			return err
		}

		delete(i.pending, key)
	}

	return nil
}

func (i *Inbox) AddPackage(pack model.AggregationPackage) error {
	if pack.Aggregation.ShardID != i.shardID {
		return ErrPackageWrongShard
	}

	key := packageKey{BatchID: pack.Aggregation.BatchID, ChunkIndex: pack.Aggregation.ChunkIndex}
	if _, exists := i.enqueued[key]; exists {
		return nil
	}

	if _, exists := i.pending[key]; exists {
		return nil
	}

	if _, exists := i.confirmed[key.BatchID]; exists {
		return i.enqueue(pack)
	}

	i.pending[key] = pack.Clone()

	return nil
}

func (i *Inbox) enqueue(pack model.AggregationPackage) error {
	if _, exists := i.confirmed[pack.Aggregation.BatchID]; !exists {
		return ErrPackageRootMismatch
	}

	tx := transaction.NewAggregationSettlementTransaction(pack, time.Now())
	if err := i.txPool.AddTxs([]transaction.Transaction{*tx}); err != nil {
		return fmt.Errorf("add settlement transaction: %w", err)
	}

	i.enqueued[packageKey{
		BatchID: pack.Aggregation.BatchID, ChunkIndex: pack.Aggregation.ChunkIndex,
	}] = struct{}{}

	return nil
}
