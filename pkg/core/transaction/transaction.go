// Definition of transaction

package transaction

import (
	"crypto/sha256"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/rlp"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
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

// Encode encodes transactions.
// Transaction encode should be prepare
func (tx *Transaction) Encode() ([]byte, error) {
	return rlp.EncodeToBytes(tx)
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
