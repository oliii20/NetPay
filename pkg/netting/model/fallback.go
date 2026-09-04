package model

import (
	"math/big"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
)

type FallbackKey struct {
	IntentID intent.ID
	BatchID  merkle.Hash
}

type ReservedFallback struct {
	IntentID         intent.ID
	BatchID          merkle.Hash
	Sender           account.Address
	Recipient        account.Address
	SourceShard      int64
	DestinationShard int64
	Amount           *big.Int
	Proof            SettlementPackage
}

func (f ReservedFallback) Clone() ReservedFallback {
	cloned := f
	if f.Amount != nil {
		cloned.Amount = new(big.Int).Set(f.Amount)
	}
	cloned.Proof = f.Proof.Clone()

	return cloned
}

func (f ReservedFallback) Key() FallbackKey {
	return FallbackKey{IntentID: f.IntentID, BatchID: f.BatchID}
}
