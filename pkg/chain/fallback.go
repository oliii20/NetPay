package chain

import (
	"context"
	"fmt"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/fallback"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/vm"
)

func (c *Chain) GetPendingFallbacks(context.Context) ([]model.ReservedFallback, error) {
	c.mux.Lock()
	defer c.mux.Unlock()
	vme, err := c.getVMExecutor()
	if err != nil {
		return nil, fmt.Errorf("get VM executor: %w", err)
	}
	items, err := fallback.NewOutbox(vme.StateDB()).ListPending()
	if err != nil {
		return nil, fmt.Errorf("list pending fallback: %w", err)
	}

	return items, nil
}

func (c *Chain) GetFallbackStatus(_ context.Context, key model.FallbackKey) (fallback.Status, error) {
	c.mux.Lock()
	defer c.mux.Unlock()
	vme, err := c.getVMExecutor()
	if err != nil {
		return fallback.StatusUnknown, fmt.Errorf("get VM executor: %w", err)
	}

	return fallback.NewOutbox(vme.StateDB()).Status(key), nil
}

func (c *Chain) fallbackTxExecute(v *vm.Executor, tx transaction.Transaction) error {
	switch tx.TxType() {
	case transaction.ReservedFallbackTxType:
		item := tx.ReservedFallback
		if item == nil || tx.FallbackCompleted != nil || tx.Value == nil || item.Amount == nil ||
			tx.Value.Cmp(item.Amount) != 0 || tx.Sender != item.Sender || tx.Recipient != item.Recipient {
			return fallback.ErrInvalidFallback
		}
		confirmed, exists := c.confirmedRoots[item.BatchID]
		if !exists {
			return fallback.ErrFallbackProof
		}
		return fallback.ExecuteCredit(v.StateDB(), c.shardID, *item, confirmed)
	case transaction.FallbackCompletedTxType:
		if tx.FallbackCompleted == nil || tx.ReservedFallback != nil || tx.Value == nil || tx.Value.Sign() != 0 {
			return fallback.ErrInvalidFallback
		}
		return fallback.NewOutbox(v.StateDB()).Complete(*tx.FallbackCompleted)
	default:
		return fallback.ErrInvalidFallback
	}
}
