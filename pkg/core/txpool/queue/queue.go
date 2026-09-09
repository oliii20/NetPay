package queue

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/HuangLab-SYSU/block-emulator-x/config"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
)

type packTxFunc func(q []transaction.Transaction, n int) ([]transaction.Transaction, []transaction.Transaction, error)

type TxPool struct {
	queue []transaction.Transaction
	pf    packTxFunc
	lock  sync.Mutex
	cfg   config.TxPoolCfg
}

func NewTxPool(cfg config.TxPoolCfg) (*TxPool, error) {
	var pf packTxFunc

	switch cfg.Type {
	case config.TxPoolByteType:
		pf = packTxsByGivenBytes
	case config.TxPoolNumType:
		pf = packTxsByGivenNum
	default:
		return nil, fmt.Errorf("unknown tx pool type: %s", cfg.Type)
	}

	return &TxPool{
		queue: make([]transaction.Transaction, 0),
		pf:    pf,
		cfg:   cfg,
	}, nil
}

func (t *TxPool) AddTxs(txs []transaction.Transaction) error {
	t.lock.Lock()
	defer t.lock.Unlock()

	t.queue = append(t.queue, txs...)

	return nil
}

func (t *TxPool) PackTxs(limit int) ([]transaction.Transaction, error) {
	t.lock.Lock()
	defer t.lock.Unlock()

	var (
		packed, q []transaction.Transaction
		err       error
	)

	packed, q, err = t.pf(t.queue, limit)
	if err != nil {
		return nil, fmt.Errorf("call pack tx func err: %w", err)
	}

	t.queue = q

	slog.Debug("txs are packed from the tx pool", "packed tx size", len(packed), "tx pool size", len(q))

	return packed, nil
}

func (t *TxPool) GetTxListSize(txs []transaction.Transaction) (int, error) {
	switch t.cfg.Type {
	case config.TxPoolByteType:
		return getCurSizeOfByte(txs)
	case config.TxPoolNumType:
		return getCurSizeOfNum(txs)
	}

	return 0, fmt.Errorf("unknown tx pool type: %s", t.cfg.Type)
}

func packTxsByGivenNum(
	q []transaction.Transaction,
	n int,
) ([]transaction.Transaction, []transaction.Transaction, error) {
	if n <= 0 || len(q) == 0 {
		return nil, q, nil
	}

	selected := make([]bool, len(q))
	packed := make([]transaction.Transaction, 0, min(n, len(q)))
	for idx := range q {
		if len(packed) == n {
			break
		}
		if !isPriorityTx(q[idx]) {
			continue
		}
		packed = append(packed, q[idx])
		selected[idx] = true
	}
	for idx := range q {
		if len(packed) == n {
			break
		}
		if selected[idx] {
			continue
		}
		packed = append(packed, q[idx])
		selected[idx] = true
	}

	return packed, remainingTxs(q, selected), nil
}

func packTxsByGivenBytes(
	q []transaction.Transaction,
	n int,
) ([]transaction.Transaction, []transaction.Transaction, error) {
	if n <= 0 || len(q) == 0 {
		return nil, q, nil
	}

	selected := make([]bool, len(q))
	packed := make([]transaction.Transaction, 0)
	remainingBytes := n
	for idx := range q {
		if !isPriorityTx(q[idx]) {
			continue
		}
		nextRemaining, ok, err := packIfFits(q[idx], remainingBytes)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}
		packed = append(packed, q[idx])
		selected[idx] = true
		remainingBytes = nextRemaining
	}
	for idx := range q {
		if selected[idx] {
			continue
		}
		nextRemaining, ok, err := packIfFits(q[idx], remainingBytes)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			continue
		}
		packed = append(packed, q[idx])
		selected[idx] = true
		remainingBytes = nextRemaining
	}

	return packed, remainingTxs(q, selected), nil
}

func packIfFits(tx transaction.Transaction, remainingBytes int) (int, bool, error) {
	b, err := tx.Encode()
	if err != nil {
		return remainingBytes, false, err
	}
	size := len(b)
	if size > remainingBytes {
		return remainingBytes, false, nil
	}

	return remainingBytes - size, true, nil
}

func remainingTxs(q []transaction.Transaction, selected []bool) []transaction.Transaction {
	remaining := make([]transaction.Transaction, 0, len(q))
	for idx := range q {
		if selected[idx] {
			continue
		}
		remaining = append(remaining, q[idx])
	}

	return remaining
}

func isPriorityTx(tx transaction.Transaction) bool {
	switch tx.TxType() {
	case transaction.SettlementTxType, transaction.ReservedFallbackTxType, transaction.FallbackCompletedTxType:
		return true
	default:
		return false
	}
}

func getCurSizeOfNum(txs []transaction.Transaction) (int, error) {
	return len(txs), nil
}

func getCurSizeOfByte(txs []transaction.Transaction) (int, error) {
	cnt := 0

	for _, tx := range txs {
		b, err := tx.Encode()
		if err != nil {
			return 0, err
		}

		cnt += len(b)
	}

	return cnt, nil
}
