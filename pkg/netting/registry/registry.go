// Package registry stores payment-intent reservations in a shard's EVM state.
package registry

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

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
)

const (
	escrowAddressDomain   = "BLOCKEMULATOR_NETTING_ESCROW_V1"
	registryAddressDomain = "BLOCKEMULATOR_NETTING_REGISTRY_V1"

	amountSlotDomain   = "intent:amount"
	matchedSlotDomain  = "intent:matched"
	fallbackSlotDomain = "intent:fallback"
	batchSlotDomain    = "intent:batch"
	statusSlotDomain   = "intent:status"
	nonceSlotDomain    = "intent:nonce"

	balanceChangeEscrow = byte(28)
)

var (
	EscrowAccountAddress  = systemAddress(escrowAddressDomain)
	IntentRegistryAddress = systemAddress(registryAddressDomain)

	ErrReservationExists   = errors.New("intent reservation already exists")
	ErrReservationNotFound = errors.New("intent reservation not found")
	ErrNonceMismatch       = errors.New("intent nonce mismatch")
	ErrNonceExhausted      = errors.New("intent nonce exhausted")
	ErrInsufficientBalance = errors.New("insufficient balance for intent reservation")
	ErrInvalidStatus       = errors.New("invalid reservation status")
)

type ReservationStatus uint8

const (
	ReservationUnknown ReservationStatus = iota
	ReservationReserved
	ReservationConsumed
	ReservationExpired
)

type Reservation struct {
	IntentID       intent.ID
	Sender         account.Address
	Recipient      account.Address
	Amount         *big.Int
	MatchedAmount  *big.Int
	FallbackAmount *big.Int
	Nonce          uint64
	ExpiryEpoch    uint64
	BatchID        merkle.Hash
	Status         ReservationStatus
}

type Registry struct {
	state *state.StateDB
}

func New(stateDB *state.StateDB) *Registry {
	return &Registry{state: stateDB}
}

func (r *Registry) Reserve(payment intent.PaymentIntent, ctx intent.ValidationContext) (Reservation, error) {
	if err := payment.Validate(ctx); err != nil {
		return Reservation{}, fmt.Errorf("validate intent: %w", err)
	}

	id, err := payment.ID()
	if err != nil {
		return Reservation{}, fmt.Errorf("calculate intent ID: %w", err)
	}
	status, err := r.status(id)
	if err != nil {
		return Reservation{}, err
	}
	if status != ReservationUnknown {
		return Reservation{}, fmt.Errorf("%w: %x", ErrReservationExists, id)
	}

	nextNonce := r.NextNonce(payment.Sender)
	if payment.Nonce != nextNonce {
		return Reservation{}, fmt.Errorf("%w: got %d, want %d", ErrNonceMismatch, payment.Nonce, nextNonce)
	}
	if nextNonce == math.MaxUint64 {
		return Reservation{}, fmt.Errorf("%w: sender %x", ErrNonceExhausted, payment.Sender)
	}

	amount, overflow := uint256.FromBig(payment.Amount)
	if overflow {
		return Reservation{}, fmt.Errorf("convert intent amount: %w", intent.ErrInvalidAmount)
	}
	sender := common.Address(payment.Sender)
	if !core.CanTransfer(r.state, sender, amount) {
		return Reservation{}, fmt.Errorf("%w: sender %x", ErrInsufficientBalance, payment.Sender)
	}

	r.state.SubBalance(sender, amount, tracing.BalanceChangeReason(balanceChangeEscrow))
	r.state.AddBalance(
		common.Address(EscrowAccountAddress),
		amount,
		tracing.BalanceChangeReason(balanceChangeEscrow),
	)
	r.ensureRegistryAccount()
	r.setIntentValue(amountSlotDomain, id, common.BigToHash(payment.Amount))
	r.setIntentValue(statusSlotDomain, id, common.BigToHash(new(big.Int).SetUint64(uint64(ReservationReserved))))
	r.setNonce(payment.Sender, nextNonce+1)

	return r.Get(payment)
}

func (r *Registry) Get(payment intent.PaymentIntent) (Reservation, error) {
	id, err := payment.ID()
	if err != nil {
		return Reservation{}, fmt.Errorf("calculate intent ID: %w", err)
	}
	status, err := r.status(id)
	if err != nil {
		return Reservation{}, err
	}
	if status == ReservationUnknown {
		return Reservation{}, fmt.Errorf("%w: %x", ErrReservationNotFound, id)
	}

	return Reservation{
		IntentID:       id,
		Sender:         payment.Sender,
		Recipient:      payment.Recipient,
		Amount:         r.intentValue(amountSlotDomain, id).Big(),
		MatchedAmount:  r.intentValue(matchedSlotDomain, id).Big(),
		FallbackAmount: r.intentValue(fallbackSlotDomain, id).Big(),
		Nonce:          payment.Nonce,
		ExpiryEpoch:    payment.ExpiryEpoch,
		BatchID:        merkle.Hash(r.intentValue(batchSlotDomain, id)),
		Status:         status,
	}, nil
}

func (r *Registry) NextNonce(sender account.Address) uint64 {
	return r.state.GetState(common.Address(IntentRegistryAddress), nonceSlot(sender)).Big().Uint64()
}

func (r *Registry) EscrowBalance() *uint256.Int {
	return new(uint256.Int).Set(r.state.GetBalance(common.Address(EscrowAccountAddress)))
}

func (r *Registry) Consume(result model.IntentResult, batchID merkle.Hash) error {
	if err := result.Validate(); err != nil {
		return fmt.Errorf("validate consumed result: %w", err)
	}
	reservation, err := r.Get(result.Intent)
	if err != nil {
		return err
	}
	if reservation.Status != ReservationReserved {
		return fmt.Errorf("%w: got %d, want %d", ErrInvalidStatus, reservation.Status, ReservationReserved)
	}
	if reservation.Amount.Cmp(result.Intent.Amount) != 0 || reservation.Nonce != result.Intent.Nonce {
		return fmt.Errorf("reservation differs from intent %x", result.IntentID)
	}
	r.ensureRegistryAccount()
	r.setIntentValue(matchedSlotDomain, result.IntentID, common.BigToHash(result.MatchedAmount))
	r.setIntentValue(fallbackSlotDomain, result.IntentID, common.BigToHash(result.FallbackAmount))
	r.setIntentValue(batchSlotDomain, result.IntentID, common.Hash(batchID))
	r.setIntentValue(
		statusSlotDomain,
		result.IntentID,
		common.BigToHash(new(big.Int).SetUint64(uint64(ReservationConsumed))),
	)

	return nil
}

func (r *Registry) status(id intent.ID) (ReservationStatus, error) {
	value := r.intentValue(statusSlotDomain, id).Big()
	if !value.IsUint64() || value.Uint64() > uint64(ReservationExpired) {
		return ReservationUnknown, fmt.Errorf("%w: %s", ErrInvalidStatus, value)
	}

	return ReservationStatus(value.Uint64()), nil
}

func (r *Registry) setNonce(sender account.Address, nonce uint64) {
	r.state.SetState(
		common.Address(IntentRegistryAddress),
		nonceSlot(sender),
		common.BigToHash(new(big.Int).SetUint64(nonce)),
	)
}

func (r *Registry) ensureRegistryAccount() {
	address := common.Address(IntentRegistryAddress)
	if r.state.GetNonce(address) == 0 {
		r.state.SetNonce(address, 1, tracing.NonceChangeUnspecified)
	}
}

func (r *Registry) intentValue(domain string, id intent.ID) common.Hash {
	return r.state.GetState(common.Address(IntentRegistryAddress), intentSlot(domain, id))
}

func (r *Registry) setIntentValue(domain string, id intent.ID, value common.Hash) {
	r.state.SetState(common.Address(IntentRegistryAddress), intentSlot(domain, id), value)
}

func systemAddress(domain string) account.Address {
	hash := sha256.Sum256([]byte(domain))
	var address account.Address
	copy(address[:], hash[len(hash)-len(address):])

	return address
}

func intentSlot(domain string, id intent.ID) common.Hash {
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
