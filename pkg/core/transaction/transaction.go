// Package transaction defines user and protocol transactions.
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

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
)

const (
	NormalTxType byte = iota
	RelayTxType
	BrokerTxType
	CreateContractTxType
	CallContractTxType
	CrossShardPaymentTxType
	AggregationSettlementTxType
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
	GasLimit    uint64

	RelayTxOpt
	BrokerTxOpt
	CrossShardPaymentTxOpt
	AggregationSettlementTxOpt
}

type RelayTxOpt struct {
	RelayStage    uint
	ROriginalHash []byte
}

type BrokerTxOpt struct {
	BrokerStage               uint
	Broker                    account.Address
	BOriginalHash             []byte
	OriginalTxCreateTime      time.Time
	NonceBroker               uint64
	HeightLock, HeightCurrent uint64
}

type CrossShardPaymentTxOpt struct {
	Payment *paymentmodel.CrossShardPayment `rlp:"nil"`
}

type AggregationSettlementTxOpt struct {
	Aggregation *model.AggregationPackage `rlp:"nil"`
}

func NewTransaction(
	sender, recipient account.Address,
	value, priorityFee *big.Int,
	nonce uint64,
	proposeTime time.Time,
) *Transaction {
	return &Transaction{
		Sender: sender, Recipient: recipient, Value: value, PriorityFee: priorityFee,
		Nonce: nonce, CreateTime: proposeTime, GasLimit: defaultGasLimit,
	}
}

func NewCrossShardPaymentTransaction(payment paymentmodel.CrossShardPayment, proposeTime time.Time) *Transaction {
	cloned := payment.Clone()

	var value *big.Int
	if cloned.Amount != nil {
		value = new(big.Int).Set(cloned.Amount)
	}

	return &Transaction{
		Sender: cloned.Sender, Recipient: cloned.Recipient, Value: value,
		PriorityFee: new(big.Int), Nonce: cloned.Nonce, CreateTime: proposeTime,
		GasLimit: defaultGasLimit, CrossShardPaymentTxOpt: CrossShardPaymentTxOpt{Payment: &cloned},
	}
}

func NewAggregationSettlementTransaction(pack model.AggregationPackage, proposeTime time.Time) *Transaction {
	cloned := pack.Clone()

	return &Transaction{
		Value: new(big.Int), PriorityFee: new(big.Int), CreateTime: proposeTime,
		GasLimit:                   defaultGasLimit,
		AggregationSettlementTxOpt: AggregationSettlementTxOpt{Aggregation: &cloned},
	}
}

func (tx *Transaction) Encode() ([]byte, error) {
	return rlp.EncodeToBytes(tx)
}

// EncodeRLP commits protocol payloads through a deterministic opaque encoding.
func (tx *Transaction) EncodeRLP(writer io.Writer) error {
	if tx.isCanonicalPaymentEnvelope() {
		payment := tx.Payment

		return rlp.Encode(writer, struct {
			Type             byte
			CreateTime       time.Time
			Version          uint8
			ChainID          uint64
			Sender           account.Address
			Recipient        account.Address
			SourceShard      uint64
			DestinationShard uint64
			Amount           *big.Int
			Nonce            uint64
			ExpiryEpoch      uint64
			Signature        []byte
		}{
			Type: CrossShardPaymentTxType, CreateTime: tx.CreateTime,
			Version: payment.Version, ChainID: payment.ChainID,
			Sender: payment.Sender, Recipient: payment.Recipient,
			SourceShard: uint64(payment.SourceShard), DestinationShard: uint64(payment.DestinationShard),
			Amount: payment.Amount, Nonce: payment.Nonce, ExpiryEpoch: payment.ExpiryEpoch,
			Signature: payment.Signature,
		})
	}

	var aggregationBytes []byte

	if tx.Aggregation != nil {
		var out bytes.Buffer
		if err := gob.NewEncoder(&out).Encode(tx.Aggregation); err != nil {
			return fmt.Errorf("encode aggregation settlement transaction: %w", err)
		}

		aggregationBytes = out.Bytes()
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
		Payment                   *paymentmodel.CrossShardPayment `rlp:"nil"`
		Aggregation               []byte
	}

	return rlp.Encode(writer, rlpTransaction{
		Sender: tx.Sender, Recipient: tx.Recipient, Value: tx.Value, PriorityFee: tx.PriorityFee,
		Nonce: tx.Nonce, Signature: tx.Signature, CreateTime: tx.CreateTime, Data: tx.Data,
		GasLimit: tx.GasLimit, RelayStage: tx.RelayStage, ROriginalHash: tx.ROriginalHash,
		BrokerStage: tx.BrokerStage, Broker: tx.Broker, BOriginalHash: tx.BOriginalHash,
		OriginalTxCreateTime: tx.OriginalTxCreateTime, NonceBroker: tx.NonceBroker,
		HeightLock: tx.HeightLock, HeightCurrent: tx.HeightCurrent,
		Payment: tx.Payment, Aggregation: aggregationBytes,
	})
}

func (tx *Transaction) isCanonicalPaymentEnvelope() bool {
	if tx.Payment == nil || tx.Payment.Amount == nil || tx.Value == nil ||
		tx.Payment.SourceShard < 0 || tx.Payment.DestinationShard < 0 ||
		tx.Payment.AssetID != paymentmodel.NativeAssetID || tx.Sender != tx.Payment.Sender ||
		tx.Recipient != tx.Payment.Recipient || tx.Value.Cmp(tx.Payment.Amount) != 0 || tx.Nonce != tx.Payment.Nonce ||
		(tx.PriorityFee != nil && tx.PriorityFee.Sign() != 0) || tx.GasLimit != defaultGasLimit ||
		len(tx.Signature) != 0 || len(tx.Data) != 0 || tx.RelayStage != 0 || len(tx.ROriginalHash) != 0 ||
		tx.BrokerStage != 0 || tx.Broker != account.EmptyAccountAddr || len(tx.BOriginalHash) != 0 ||
		!tx.OriginalTxCreateTime.IsZero() || tx.NonceBroker != 0 || tx.HeightLock != 0 || tx.HeightCurrent != 0 ||
		tx.Aggregation != nil {
		return false
	}

	return true
}

func (tx *Transaction) Hash() ([]byte, error) {
	encoded, err := tx.Encode()
	if err != nil {
		return nil, err
	}

	sum := sha256.Sum256(encoded)

	return sum[:], nil
}

func (tx *Transaction) TxType() byte {
	if tx.Aggregation != nil {
		return AggregationSettlementTxType
	}

	if tx.Payment != nil {
		return CrossShardPaymentTxType
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
