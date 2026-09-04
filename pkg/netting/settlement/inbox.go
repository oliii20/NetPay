package settlement

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
)

var (
	ErrPackageWrongShard   = errors.New("settlement package belongs to another shard")
	ErrPackageRootMismatch = errors.New("settlement package does not match confirmed root")
)

type RootConfirmer interface {
	ConfirmMatchRoot(context.Context, model.MatchRootBlockBody) error
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
	confirmed map[merkle.Hash]model.MatchRootBlockBody
	pending   map[packageKey]model.SettlementPackage
	enqueued  map[packageKey]struct{}
}

func NewInbox(shardID int64, confirmer RootConfirmer, txPool TransactionAdder) *Inbox {
	return &Inbox{
		shardID: shardID, confirmer: confirmer, txPool: txPool,
		confirmed: make(map[merkle.Hash]model.MatchRootBlockBody),
		pending:   make(map[packageKey]model.SettlementPackage),
		enqueued:  make(map[packageKey]struct{}),
	}
}

func (i *Inbox) Confirm(ctx context.Context, header model.MatchRootBlockBody) error {
	if err := i.confirmer.ConfirmMatchRoot(ctx, header); err != nil {
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

func (i *Inbox) AddPackage(pack model.SettlementPackage) error {
	if pack.Settlement.ShardID != i.shardID {
		return ErrPackageWrongShard
	}
	key := packageKey{BatchID: pack.Header.BatchID, ChunkIndex: pack.Settlement.ChunkIndex}
	if _, exists := i.enqueued[key]; exists {
		return nil
	}
	if _, exists := i.pending[key]; exists {
		return nil
	}
	if confirmed, exists := i.confirmed[key.BatchID]; exists {
		if !reflect.DeepEqual(confirmed, pack.Header) {
			return ErrPackageRootMismatch
		}
		return i.enqueue(pack)
	}
	i.pending[key] = pack.Clone()

	return nil
}

func (i *Inbox) enqueue(pack model.SettlementPackage) error {
	confirmed := i.confirmed[pack.Header.BatchID]
	if !reflect.DeepEqual(confirmed, pack.Header) {
		return ErrPackageRootMismatch
	}
	tx := transaction.NewSettlementTransaction(pack, time.Now())
	if err := i.txPool.AddTxs([]transaction.Transaction{*tx}); err != nil {
		return fmt.Errorf("add settlement transaction: %w", err)
	}
	i.enqueued[packageKey{
		BatchID: pack.Header.BatchID, ChunkIndex: pack.Settlement.ChunkIndex,
	}] = struct{}{}

	return nil
}
