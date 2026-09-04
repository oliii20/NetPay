package chain

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/commitment"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/settlement"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/vm"
)

var (
	ErrInvalidMatchRoot   = errors.New("invalid confirmed MatchRoot")
	ErrMatchRootConflict  = errors.New("conflicting confirmed MatchRoot")
	ErrSettlementEnvelope = errors.New("invalid settlement transaction envelope")
)

func (c *Chain) ConfirmMatchRoot(_ context.Context, header model.MatchRootBlockBody) error {
	c.mux.Lock()
	defer c.mux.Unlock()

	roots := commitment.Roots{
		CutRoot: header.CutRoot, IntentResultRoot: header.IntentResultRoot,
		ShardSettlementRoot: header.ShardSettlementRoot,
	}
	if commitment.MatchRoot(roots) != header.MatchRoot ||
		commitment.BatchID(header.PreviousBatchID, header.WindowID, header.MatchRoot) != header.BatchID {
		return ErrInvalidMatchRoot
	}
	if existing, ok := c.confirmedRoots[header.BatchID]; ok {
		if reflect.DeepEqual(existing, header) {
			return nil
		}
		return ErrMatchRootConflict
	}
	cloned := header
	cloned.Cuts = append([]model.ShardCut(nil), header.Cuts...)
	c.confirmedRoots[header.BatchID] = cloned

	return nil
}

func (c *Chain) IsMatchRootConfirmed(batchID merkle.Hash) bool {
	c.mux.Lock()
	defer c.mux.Unlock()
	_, exists := c.confirmedRoots[batchID]

	return exists
}

func (c *Chain) settlementTxExecute(v *vm.Executor, tx transaction.Transaction) error {
	if tx.Settlement == nil || tx.Value == nil || tx.Value.Sign() != 0 || tx.Sender != account.EmptyAccountAddr {
		return ErrSettlementEnvelope
	}
	confirmed, exists := c.confirmedRoots[tx.Settlement.Header.BatchID]
	if !exists {
		return settlement.ErrUnknownMatchRoot
	}
	if err := (settlement.Executor{}).Execute(v.StateDB(), c.shardID, *tx.Settlement, confirmed); err != nil {
		return fmt.Errorf("execute settlement package: %w", err)
	}

	return nil
}
