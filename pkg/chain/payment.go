package chain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/ethereum/go-ethereum/common"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/outbox"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/vm"
)

var ErrPaymentEnvelopeMismatch = errors.New("cross-shard payment transaction envelope mismatch")

func (c *Chain) GetOutgoingPayment(_ context.Context, payment paymentmodel.CrossShardPayment) (outbox.Record, error) {
	c.mux.Lock()
	defer c.mux.Unlock()

	vme, err := c.getVMExecutor()
	if err != nil {
		return outbox.Record{}, fmt.Errorf("get VM executor: %w", err)
	}

	record, err := outbox.New(vme.StateDB()).Get(payment)
	if err != nil {
		return outbox.Record{}, fmt.Errorf("get outgoing payment: %w", err)
	}

	return record, nil
}

func (c *Chain) GetNextPaymentNonce(_ context.Context, sender account.Address) (uint64, error) {
	c.mux.Lock()
	defer c.mux.Unlock()

	vme, err := c.getVMExecutor()
	if err != nil {
		return 0, fmt.Errorf("get VM executor: %w", err)
	}

	return outbox.New(vme.StateDB()).NextNonce(sender), nil
}

func (c *Chain) paymentTxExecute(v *vm.Executor, tx transaction.Transaction) error {
	if err := validatePaymentEnvelope(tx); err != nil {
		return err
	}

	stateDB := v.StateDB()
	snapshot := stateDB.Snapshot()
	setInitBalanceIfNotExist(stateDB, common.Address(tx.Payment.Sender))

	currentEpoch, err := nonNegativeEpoch(c.epochID)
	if err != nil {
		stateDB.RevertToSnapshot(snapshot)
		return err
	}

	_, err = outbox.New(stateDB).Debit(*tx.Payment, paymentmodel.ValidationContext{
		ChainID: uint64(c.cfg.ChainID), ShardID: c.shardID,
		ShardCount: c.cfg.ShardNum, CurrentEpoch: currentEpoch,
	})
	if err != nil {
		stateDB.RevertToSnapshot(snapshot)
		return fmt.Errorf("debit cross-shard payment: %w", err)
	}

	return nil
}

func (c *Chain) curateCrossShardPayments(
	ctx context.Context,
	header block.Header,
	body block.Body,
) (block.Body, error) {
	if !containsCrossShardPayment(body.TxList) {
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

			if tx.TxType() != transaction.CrossShardPaymentTxType {
				return block.Body{}, fmt.Errorf("pre-execute transaction: %w", err)
			}

			slog.DebugContext(ctx, "drop invalid cross-shard payment", "err", err)

			continue
		}

		accepted = append(accepted, tx)
	}

	body.TxList = accepted

	return body, nil
}

func validatePaymentEnvelope(tx transaction.Transaction) error {
	if tx.Payment == nil || tx.Value == nil || tx.Payment.Amount == nil {
		return ErrPaymentEnvelopeMismatch
	}

	if tx.Sender != tx.Payment.Sender || tx.Recipient != tx.Payment.Recipient ||
		tx.Nonce != tx.Payment.Nonce || tx.Value.Cmp(tx.Payment.Amount) != 0 {
		return ErrPaymentEnvelopeMismatch
	}

	return nil
}

func containsCrossShardPayment(txs []transaction.Transaction) bool {
	for idx := range txs {
		if txs[idx].TxType() == transaction.CrossShardPaymentTxType {
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
