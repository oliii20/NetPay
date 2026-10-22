package chain

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/commitment"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/settlement"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/vm"
)

var (
	ErrInvalidAggregationRoot  = errors.New("invalid confirmed aggregation root")
	ErrAggregationRootConflict = errors.New("conflicting confirmed aggregation root")
	ErrSettlementEnvelope      = errors.New("invalid aggregation settlement transaction envelope")
)

func (c *Chain) ConfirmAggregationRoot(_ context.Context, header model.AggregationBatchHeader) error {
	c.mux.Lock()
	defer c.mux.Unlock()

	roots := commitment.Roots{
		CutRoot: header.CutRoot, ReceiptRoot: header.ReceiptRoot,
		ShardAggregationRoot: header.ShardAggregationRoot,
	}
	if commitment.AggregationRoot(roots) != header.AggregationRoot ||
		commitment.BatchID(header.PreviousBatchID, header.WindowID, header.AggregationRoot) != header.BatchID {
		return ErrInvalidAggregationRoot
	}

	if existing, ok := c.confirmedRoots[header.BatchID]; ok {
		if reflect.DeepEqual(existing, header) {
			return nil
		}

		return ErrAggregationRootConflict
	}

	cloned := header
	cloned.Cuts = append([]model.ShardCut(nil), header.Cuts...)
	c.confirmedRoots[header.BatchID] = cloned

	return nil
}

func (c *Chain) IsAggregationRootConfirmed(batchID merkle.Hash) bool {
	c.mux.Lock()
	defer c.mux.Unlock()

	_, exists := c.confirmedRoots[batchID]

	return exists
}

func (c *Chain) aggregationSettlementTxExecute(v *vm.Executor, tx transaction.Transaction) error {
	if tx.Aggregation == nil || tx.Value == nil || tx.Value.Sign() != 0 || tx.Sender != account.EmptyAccountAddr {
		return ErrSettlementEnvelope
	}

	confirmed, exists := c.confirmedRoots[tx.Aggregation.Aggregation.BatchID]
	if !exists {
		return settlement.ErrUnknownAggregationRoot
	}

	if err := (settlement.Executor{}).Execute(v.StateDB(), c.shardID, *tx.Aggregation, confirmed); err != nil {
		return fmt.Errorf("execute aggregation settlement package: %w", err)
	}

	return nil
}
