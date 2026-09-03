package model

import (
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
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

type FinalizedBlockReceipt struct {
	ShardID    int64
	Height     uint64
	BlockHash  merkle.Hash
	ParentHash merkle.Hash
	StateRoot  merkle.Hash
	Epoch      int64
	Intents    []intent.PaymentIntent
	CommitTime time.Time
}

func (r FinalizedBlockReceipt) Clone() FinalizedBlockReceipt {
	cloned := r
	cloned.Intents = make([]intent.PaymentIntent, len(r.Intents))
	for idx, payment := range r.Intents {
		cloned.Intents[idx] = CloneIntent(payment)
	}

	return cloned
}
