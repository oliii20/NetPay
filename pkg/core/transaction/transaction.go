// Definition of transaction

package transaction

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"fmt"
	"io"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/rlp"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
)

const (
	NormalTxType byte = iota
	RelayTxType
	BrokerTxType
	CreateContractTxType
	CallContractTxType
	IntentSubmitTxType
	SettlementTxType
)

const (
	UndeterminedRelayTx = 0
	Relay1Tx            = 1
	Relay2Tx            = 2

	RawTxBrokerStage  = 0
	Sigma1BrokerStage = 1
	Sigma2BrokerStage = 2
)

const defaultGasLimit = 1000000

type Signature []byte

type Transaction struct {
	Sender      account.Address
	Recipient   account.Address
	Value       *big.Int
	PriorityFee *big.Int
	Nonce       uint64
	Signature   Signature
	CreateTime  time.Time
	Data        []byte

	GasLimit uint64

	RelayTxOpt  // the optional setting only for relay transactions.
	BrokerTxOpt // the optional setting only for broker transactions.
	IntentTxOpt // the optional setting only for payment-intent transactions.
	SettlementTxOpt
}

type RelayTxOpt struct {
	RelayStage    uint
	ROriginalHash []byte
}

type BrokerTxOpt struct {
	BrokerStage               uint // label that this is a sigma_1 tx or a sigma_2 tx.
	Broker                    account.Address
	BOriginalHash             []byte // the hash of raw message
	OriginalTxCreateTime      time.Time
	NonceBroker               uint64
	HeightLock, HeightCurrent uint64
}

type IntentTxOpt struct {
	Intent *intent.PaymentIntent `rlp:"nil"`
}

type SettlementTxOpt struct {
	Settlement *model.SettlementPackage `rlp:"nil"`
}

func NewTransaction(
	sender, recipient account.Address,
	value, priorityFee *big.Int,
	nonce uint64, proposeTime time.Time,
) *Transaction {
	tx := &Transaction{
		Sender:      sender,
		Recipient:   recipient,
		Value:       value,
		PriorityFee: priorityFee,
		Nonce:       nonce,
		CreateTime:  proposeTime,
		GasLimit:    defaultGasLimit,
	}

	return tx
}

func NewIntentTransaction(payment intent.PaymentIntent, proposeTime time.Time) *Transaction {
	cloned := payment.Clone()
	var value *big.Int
	if cloned.Amount != nil {
		value = new(big.Int).Set(cloned.Amount)
	}

	return &Transaction{
		Sender:      cloned.Sender,
		Recipient:   cloned.Recipient,
		Value:       value,
		PriorityFee: new(big.Int),
		Nonce:       cloned.Nonce,
		CreateTime:  proposeTime,
		GasLimit:    defaultGasLimit,
		IntentTxOpt: IntentTxOpt{Intent: &cloned},
	}
}

func NewSettlementTransaction(settlement model.SettlementPackage, proposeTime time.Time) *Transaction {
	cloned := settlement.Clone()

	return &Transaction{
		Value:           new(big.Int),
		PriorityFee:     new(big.Int),
		CreateTime:      proposeTime,
		GasLimit:        defaultGasLimit,
		SettlementTxOpt: SettlementTxOpt{Settlement: &cloned},
	}
}

// Encode encodes transactions.
// Transaction encode should be prepare
func (tx *Transaction) Encode() ([]byte, error) {
	return rlp.EncodeToBytes(tx)
}

// EncodeRLP keeps the legacy transaction fields explicit and commits the
// settlement package as a deterministic opaque payload. This avoids exposing
// signed shard identifiers to go-ethereum's unsigned-only RLP reflection.
func (tx *Transaction) EncodeRLP(writer io.Writer) error {
	var settlementBytes []byte
	if tx.Settlement != nil {
		var out bytes.Buffer
		if err := gob.NewEncoder(&out).Encode(tx.Settlement); err != nil {
			return fmt.Errorf("encode settlement transaction: %w", err)
		}
		settlementBytes = out.Bytes()
	}
	type rlpTransaction struct {
		Sender                    account.Address
		Recipient                 account.Address
		Value                     *big.Int
		PriorityFee               *big.Int
		Nonce                     uint64
		Signature                 Signature
		CreateTime                time.Time
		Data                      []byte
		GasLimit                  uint64
		RelayStage                uint
		ROriginalHash             []byte
		BrokerStage               uint
		Broker                    account.Address
		BOriginalHash             []byte
		OriginalTxCreateTime      time.Time
		NonceBroker               uint64
		HeightLock, HeightCurrent uint64
		Intent                    *intent.PaymentIntent `rlp:"nil"`
		Settlement                []byte
	}

	return rlp.Encode(writer, rlpTransaction{
		Sender: tx.Sender, Recipient: tx.Recipient, Value: tx.Value, PriorityFee: tx.PriorityFee,
		Nonce: tx.Nonce, Signature: tx.Signature, CreateTime: tx.CreateTime, Data: tx.Data,
		GasLimit: tx.GasLimit, RelayStage: tx.RelayStage, ROriginalHash: tx.ROriginalHash,
		BrokerStage: tx.BrokerStage, Broker: tx.Broker, BOriginalHash: tx.BOriginalHash,
		OriginalTxCreateTime: tx.OriginalTxCreateTime, NonceBroker: tx.NonceBroker,
		HeightLock: tx.HeightLock, HeightCurrent: tx.HeightCurrent, Intent: tx.Intent,
		Settlement: settlementBytes,
	})
}

func (tx *Transaction) Hash() ([]byte, error) {
	b, err := tx.Encode()
	if err != nil {
		return []byte{}, err
	}

	sum := sha256.Sum256(b)

	return sum[:], nil
}

// TxType returns the type of a transaction by its variables.
func (tx *Transaction) TxType() byte {
	if tx.Settlement != nil {
		return SettlementTxType
	}
	if tx.Intent != nil {
		return IntentSubmitTxType
	}

	if len(tx.BOriginalHash) != 0 {
		return BrokerTxType
	}

	if len(tx.ROriginalHash) != 0 {
		return RelayTxType
	}

	if len(tx.Data) == 0 {
		return NormalTxType
	}

	if tx.Recipient == account.EmptyAccountAddr {
		return CreateContractTxType
	}

	return CallContractTxType
}
