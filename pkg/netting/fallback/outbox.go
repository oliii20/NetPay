// Package fallback implements consensus-state-backed reserved fallback delivery.
package fallback

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/holiman/uint256"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/registry"
)

const (
	statusDomain   = "fallback:status"
	lengthDomain   = "fallback:length"
	dataDomain     = "fallback:data"
	indexDomain    = "fallback:index"
	countDomain    = "fallback:count"
	transferReason = byte(26)
)

var (
	ErrInvalidFallback  = errors.New("invalid reserved fallback")
	ErrFallbackNotFound = errors.New("reserved fallback not found")
	ErrFallbackPayload  = errors.New("corrupt reserved fallback payload")
)

type Status uint8

const (
	StatusUnknown Status = iota
	StatusPending
	StatusCompleted
)

type Outbox struct {
	state *state.StateDB
}

func NewOutbox(stateDB *state.StateDB) *Outbox {
	return &Outbox{state: stateDB}
}

func (o *Outbox) Create(item model.ReservedFallback) error {
	if item.Amount == nil || item.Amount.Sign() <= 0 || item.IntentID == [32]byte{} || item.BatchID == [32]byte{} {
		return ErrInvalidFallback
	}
	key := recordKey(item.Key())
	status := o.status(key)
	if status != StatusUnknown {
		return nil
	}
	encoded, err := encode(item)
	if err != nil {
		return fmt.Errorf("encode reserved fallback: %w", err)
	}
	amount, overflow := uint256.FromBig(item.Amount)
	if overflow || !core.CanTransfer(o.state, common.Address(registry.EscrowAccountAddress), amount) {
		return registry.ErrInsufficientBalance
	}

	o.ensureStorageAccount()
	o.state.SubBalance(
		common.Address(registry.EscrowAccountAddress),
		amount,
		tracing.BalanceChangeReason(transferReason),
	)
	count := o.count()
	o.state.SetState(storageAddress(), indexSlot(count), key)
	o.state.SetState(storageAddress(), countSlot(), common.BigToHash(new(big.Int).SetUint64(count+1)))
	o.state.SetState(storageAddress(), recordSlot(lengthDomain, key), common.BigToHash(new(big.Int).SetUint64(uint64(len(encoded)))))
	for offset := 0; offset < len(encoded); offset += common.HashLength {
		end := min(offset+common.HashLength, len(encoded))
		var word common.Hash
		copy(word[:], encoded[offset:end])
		o.state.SetState(
			storageAddress(), dataSlot(key, uint64(offset/common.HashLength)), word,
		)
	}
	o.setStatus(key, StatusPending)

	return nil
}

func (o *Outbox) ListPending() ([]model.ReservedFallback, error) {
	items := make([]model.ReservedFallback, 0)
	for idx := uint64(0); idx < o.count(); idx++ {
		key := o.state.GetState(storageAddress(), indexSlot(idx))
		if o.status(key) != StatusPending {
			continue
		}
		item, err := o.load(key)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}

	return items, nil
}

func (o *Outbox) Complete(key model.FallbackKey) error {
	record := recordKey(key)
	switch o.status(record) {
	case StatusPending:
		o.setStatus(record, StatusCompleted)
		return nil
	case StatusCompleted:
		return nil
	default:
		return ErrFallbackNotFound
	}
}

func (o *Outbox) Status(key model.FallbackKey) Status {
	return o.status(recordKey(key))
}

func (o *Outbox) load(key common.Hash) (model.ReservedFallback, error) {
	length := o.state.GetState(storageAddress(), recordSlot(lengthDomain, key)).Big().Uint64()
	if length == 0 {
		return model.ReservedFallback{}, ErrFallbackPayload
	}
	encoded := make([]byte, 0, length)
	chunks := (length + common.HashLength - 1) / common.HashLength
	for idx := uint64(0); idx < chunks; idx++ {
		chunk := o.state.GetState(storageAddress(), dataSlot(key, idx))
		encoded = append(encoded, chunk[:]...)
	}
	encoded = encoded[:length]
	var item model.ReservedFallback
	if err := gob.NewDecoder(bytes.NewReader(encoded)).Decode(&item); err != nil {
		return model.ReservedFallback{}, fmt.Errorf("%w: %v", ErrFallbackPayload, err)
	}

	return item.Clone(), nil
}

func (o *Outbox) count() uint64 {
	return o.state.GetState(storageAddress(), countSlot()).Big().Uint64()
}

func (o *Outbox) status(key common.Hash) Status {
	value := o.state.GetState(storageAddress(), recordSlot(statusDomain, key)).Big().Uint64()
	if value > uint64(StatusCompleted) {
		return StatusUnknown
	}

	return Status(value)
}

func (o *Outbox) setStatus(key common.Hash, status Status) {
	o.state.SetState(storageAddress(), recordSlot(statusDomain, key), common.BigToHash(new(big.Int).SetUint64(uint64(status))))
}

func (o *Outbox) ensureStorageAccount() {
	if o.state.GetNonce(storageAddress()) == 0 {
		o.state.SetNonce(storageAddress(), 1, tracing.NonceChangeUnspecified)
	}
}

func storageAddress() common.Address {
	return common.Address(registry.IntentRegistryAddress)
}

func recordKey(key model.FallbackKey) common.Hash {
	hasher := sha256.New()
	hasher.Write([]byte("BLOCKEMULATOR_RESERVED_FALLBACK_V1"))
	hasher.Write(key.IntentID[:])
	hasher.Write(key.BatchID[:])

	return common.BytesToHash(hasher.Sum(nil))
}

func recordSlot(domain string, key common.Hash) common.Hash {
	hasher := sha256.New()
	hasher.Write([]byte(domain))
	hasher.Write(key[:])

	return common.BytesToHash(hasher.Sum(nil))
}

func dataSlot(key common.Hash, index uint64) common.Hash {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], index)
	hasher := sha256.New()
	hasher.Write([]byte(dataDomain))
	hasher.Write(key[:])
	hasher.Write(encoded[:])

	return common.BytesToHash(hasher.Sum(nil))
}

func indexSlot(index uint64) common.Hash {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], index)
	hasher := sha256.New()
	hasher.Write([]byte(indexDomain))
	hasher.Write(encoded[:])

	return common.BytesToHash(hasher.Sum(nil))
}

func countSlot() common.Hash {
	return sha256.Sum256([]byte(countDomain))
}

func encode(value any) ([]byte, error) {
	var out bytes.Buffer
	if err := gob.NewEncoder(&out).Encode(value); err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}
