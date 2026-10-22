package model

import (
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
)

type ShardCheckpoint struct {
	Height    uint64
	BlockHash merkle.Hash
}

type ShardCut struct {
	ShardID        int64
	PreviousHeight uint64
	EndHeight      uint64
	EndBlockHash   merkle.Hash
}

// FinalizedBlockReceipt is emitted only after a source block commits. The
// emulator authenticates it through the configured shard-leader channel;
// FinalityProof is reserved for a PBFT quorum certificate.
type FinalizedBlockReceipt struct {
	ShardID       int64
	Height        uint64
	BlockHash     merkle.Hash
	ParentHash    merkle.Hash
	StateRoot     merkle.Hash
	Epoch         int64
	Payments      []paymentmodel.CrossShardPayment
	FinalityProof []byte
	CommitTime    time.Time
}

func (r FinalizedBlockReceipt) Clone() FinalizedBlockReceipt {
	cloned := r

	cloned.Payments = make([]paymentmodel.CrossShardPayment, len(r.Payments))
	for idx, payment := range r.Payments {
		cloned.Payments[idx] = ClonePayment(payment)
	}

	cloned.FinalityProof = append([]byte(nil), r.FinalityProof...)

	return cloned
}

func ClonePayment(payment paymentmodel.CrossShardPayment) paymentmodel.CrossShardPayment {
	return payment.Clone()
}
