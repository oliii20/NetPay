// Package outbox records source-finalized cross-shard payments.
package outbox

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/holiman/uint256"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
)

const (
	outboxAddressDomain = "BLOCKEMULATOR_AGGREGATION_OUTBOX_V1"
	amountSlotDomain    = "payment:amount"
	statusSlotDomain    = "payment:status"
	batchSlotDomain     = "payment:batch"
	nonceSlotDomain     = "payment:nonce"
	balanceChangeDebit  = byte(28)
)

var (
	SystemAccountAddress = systemAddress(outboxAddressDomain)

	ErrPaymentExists       = errors.New("cross-shard payment already exists")
	ErrPaymentNotFound     = errors.New("cross-shard payment not found")
	ErrNonceMismatch       = errors.New("cross-shard payment nonce mismatch")
	ErrNonceExhausted      = errors.New("cross-shard payment nonce exhausted")
	ErrInsufficientBalance = errors.New("insufficient balance for cross-shard payment")
)

type Status uint8

const (
	StatusUnknown Status = iota
	StatusDebited
)

type Record struct {
	PaymentID paymentmodel.ID
	Amount    *big.Int
	Nonce     uint64
	BatchID   merkle.Hash
	Status    Status
}

type Outbox struct {
	state *state.StateDB
}

func New(stateDB *state.StateDB) *Outbox {
	return &Outbox{state: stateDB}
}

// Debit atomically removes the source balance and records a replay-protected
// outbox entry. Destination crediting later recreates the same amount under a
// Beacon-finalized aggregation, preserving global supply across shards.
func (o *Outbox) Debit(payment paymentmodel.CrossShardPayment, ctx paymentmodel.ValidationContext) (Record, error) {
	if err := payment.Validate(ctx); err != nil {
		return Record{}, fmt.Errorf("validate payment: %w", err)
	}

	id, err := payment.ID()
	if err != nil {
		return Record{}, fmt.Errorf("calculate payment ID: %w", err)
	}

	if o.status(id) != StatusUnknown {
		return Record{}, fmt.Errorf("%w: %x", ErrPaymentExists, id)
	}

	nextNonce := o.NextNonce(payment.Sender)
	if payment.Nonce != nextNonce {
		return Record{}, fmt.Errorf("%w: got %d, want %d", ErrNonceMismatch, payment.Nonce, nextNonce)
	}

	if nextNonce == math.MaxUint64 {
		return Record{}, fmt.Errorf("%w: sender %x", ErrNonceExhausted, payment.Sender)
	}

	amount, overflow := uint256.FromBig(payment.Amount)
	if overflow || !core.CanTransfer(o.state, common.Address(payment.Sender), amount) {
		return Record{}, fmt.Errorf("%w: sender %x", ErrInsufficientBalance, payment.Sender)
	}

	o.state.SubBalance(common.Address(payment.Sender), amount, tracing.BalanceChangeReason(balanceChangeDebit))
	o.ensureAccount()
	o.set(paymentSlot(amountSlotDomain, id), common.BigToHash(payment.Amount))
	o.set(paymentSlot(statusSlotDomain, id), common.BigToHash(big.NewInt(int64(StatusDebited))))
	o.setNonce(payment.Sender, nextNonce+1)

	return o.Get(payment)
}

func (o *Outbox) Get(payment paymentmodel.CrossShardPayment) (Record, error) {
	id, err := payment.ID()
	if err != nil {
		return Record{}, err
	}

	status := o.status(id)
	if status == StatusUnknown {
		return Record{}, fmt.Errorf("%w: %x", ErrPaymentNotFound, id)
	}

	return Record{
		PaymentID: id, Amount: o.get(paymentSlot(amountSlotDomain, id)).Big(), Nonce: payment.Nonce,
		BatchID: merkle.Hash(o.get(paymentSlot(batchSlotDomain, id))), Status: status,
	}, nil
}

func (o *Outbox) NextNonce(sender account.Address) uint64 {
	return o.get(nonceSlot(sender)).Big().Uint64()
}

func (o *Outbox) status(id paymentmodel.ID) Status {
	value := o.get(paymentSlot(statusSlotDomain, id)).Big()
	if !value.IsUint64() || value.Uint64() > uint64(StatusDebited) {
		return StatusUnknown
	}

	return Status(value.Uint64())
}

func (o *Outbox) ensureAccount() {
	address := common.Address(SystemAccountAddress)
	if o.state.GetNonce(address) == 0 {
		o.state.SetNonce(address, 1, tracing.NonceChangeUnspecified)
	}
}

func (o *Outbox) setNonce(sender account.Address, nonce uint64) {
	o.set(nonceSlot(sender), common.BigToHash(new(big.Int).SetUint64(nonce)))
}

func (o *Outbox) get(slot common.Hash) common.Hash {
	return o.state.GetState(common.Address(SystemAccountAddress), slot)
}

func (o *Outbox) set(slot, value common.Hash) {
	o.state.SetState(common.Address(SystemAccountAddress), slot, value)
}

func systemAddress(domain string) account.Address {
	hash := sha256.Sum256([]byte(domain))

	var address account.Address
	copy(address[:], hash[len(hash)-len(address):])

	return address
}

func paymentSlot(domain string, id paymentmodel.ID) common.Hash {
	hasher := sha256.New()
	hasher.Write([]byte(domain))
	hasher.Write(id[:])

	return common.BytesToHash(hasher.Sum(nil))
}

func nonceSlot(sender account.Address) common.Hash {
	hasher := sha256.New()
	hasher.Write([]byte(nonceSlotDomain))
	hasher.Write(sender[:])

	return common.BytesToHash(hasher.Sum(nil))
}
