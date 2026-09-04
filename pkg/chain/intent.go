package chain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ethereum/go-ethereum/common"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/registry"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/vm"
)

var ErrIntentEnvelopeMismatch = errors.New("intent transaction envelope mismatch")

func (c *Chain) GetReservation(
	_ context.Context,
	payment intent.PaymentIntent,
) (registry.Reservation, error) {
	c.mux.Lock()
	defer c.mux.Unlock()

	vme, err := c.getVMExecutor()
	if err != nil {
		return registry.Reservation{}, fmt.Errorf("get VM executor: %w", err)
	}

	reservation, err := registry.New(vme.StateDB()).Get(payment)
	if err != nil {
		return registry.Reservation{}, fmt.Errorf("get reservation: %w", err)
	}

	return reservation, nil
}

func (c *Chain) GetNextIntentNonce(_ context.Context, sender account.Address) (uint64, error) {
	c.mux.Lock()
	defer c.mux.Unlock()

	vme, err := c.getVMExecutor()
	if err != nil {
		return 0, fmt.Errorf("get VM executor: %w", err)
	}

	return registry.New(vme.StateDB()).NextNonce(sender), nil
}

func (c *Chain) intentTxExecute(v *vm.Executor, tx transaction.Transaction) error {
	if err := validateIntentEnvelope(tx); err != nil {
		return err
	}

	stateDB := v.StateDB()
	snapshot := stateDB.Snapshot()
	setInitBalanceIfNotExist(stateDB, common.Address(tx.Intent.Sender))

	currentEpoch, err := nonNegativeEpoch(c.epochID)
	if err != nil {
		stateDB.RevertToSnapshot(snapshot)
		return err
	}
	_, err = registry.New(stateDB).Reserve(*tx.Intent, intent.ValidationContext{
		ChainID:      uint64(c.cfg.ChainID),
		ShardID:      c.shardID,
		ShardCount:   c.cfg.ShardNum,
		CurrentEpoch: currentEpoch,
	})
	if err != nil {
		stateDB.RevertToSnapshot(snapshot)
		return fmt.Errorf("reserve intent: %w", err)
	}

	return nil
}

func (c *Chain) curateIntentTransactions(
	ctx context.Context,
	header block.Header,
	body block.Body,
) (block.Body, error) {
	if !containsIntent(body.TxList) {
		return body, nil
	}

	vme, err := c.getVMExecutor()
	if err != nil {
		return block.Body{}, fmt.Errorf("get VM executor: %w", err)
	}
	accountLocations, err := c.getAccountLocationsInTxs(ctx, body.TxList)
	if err != nil {
		return block.Body{}, fmt.Errorf("get account locations: %w", err)
	}

	bCtx := getBlockCtxByBlock(&block.Block{Header: header})
	accepted := make([]transaction.Transaction, 0, len(body.TxList))
	for _, tx := range body.TxList {
		snapshot := vme.StateDB().Snapshot()
		if err = c.txExecute(vme, bCtx, accountLocations, tx); err != nil {
			vme.StateDB().RevertToSnapshot(snapshot)
			if tx.TxType() != transaction.IntentSubmitTxType {
				return block.Body{}, fmt.Errorf("pre-execute transaction: %w", err)
			}

			slog.DebugContext(ctx, "drop invalid payment intent", "err", err)
			continue
		}
		accepted = append(accepted, tx)
	}

	body.TxList = accepted

	return body, nil
}

func validateIntentEnvelope(tx transaction.Transaction) error {
	if tx.Intent == nil || tx.Value == nil || tx.Intent.Amount == nil {
		return ErrIntentEnvelopeMismatch
	}
	if tx.Sender != tx.Intent.Sender || tx.Recipient != tx.Intent.Recipient {
		return ErrIntentEnvelopeMismatch
	}
	if tx.Nonce != tx.Intent.Nonce || tx.Value.Cmp(tx.Intent.Amount) != 0 {
		return ErrIntentEnvelopeMismatch
	}

	return nil
}

func containsIntent(txs []transaction.Transaction) bool {
	for idx := range txs {
		if txs[idx].TxType() == transaction.IntentSubmitTxType {
			return true
		}
	}

	return false
}

func nonNegativeEpoch(epoch int64) (uint64, error) {
	if epoch < 0 {
		return 0, fmt.Errorf("invalid negative epoch: %d", epoch)
	}

	return uint64(epoch), nil
}
