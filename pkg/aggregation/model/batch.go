package model

import (
	"bytes"
	"math/big"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
)

// AggregationBatchHeader is the compact record finalized by the Beacon chain.
// ReceiptRoot commits finalized source payments; ShardAggregationRoot commits
// the target-side, per-account balance deltas derived from them.
type AggregationBatchHeader struct {
	BatchID              merkle.Hash
	PreviousBatchID      merkle.Hash
	WindowID             uint64
	BatchOnly            bool
	CreditChunkSize      uint32
	CutRoot              merkle.Hash
	ReceiptRoot          merkle.Hash
	ShardAggregationRoot merkle.Hash
	AggregationRoot      merkle.Hash
	Cuts                 []ShardCut
}

// AggregatedCredit is the canonical target-side effect for one account and
// asset. ReceiptRoot commits the source payment IDs represented by Amount.
type AggregatedCredit struct {
	Recipient    account.Address
	AssetID      paymentmodel.AssetID
	Amount       *big.Int
	ReceiptCount uint32
	ReceiptRoot  merkle.Hash
}

func (c AggregatedCredit) Clone() AggregatedCredit {
	cloned := c
	if c.Amount != nil {
		cloned.Amount = new(big.Int).Set(c.Amount)
	}

	return cloned
}

// ShardAggregation is one atomic target-shard execution unit. Credits are
// globally aggregated before chunking, so an account occurs at most once in a
// batch even when a shard needs several settlement transactions.
type ShardAggregation struct {
	BatchOnly  bool
	BatchID    merkle.Hash
	WindowID   uint64
	ShardID    int64
	ChunkIndex uint32
	ChunkCount uint32
	Credits    []AggregatedCredit
}

func (s ShardAggregation) Clone() ShardAggregation {
	cloned := s

	cloned.Credits = make([]AggregatedCredit, len(s.Credits))
	for idx := range s.Credits {
		cloned.Credits[idx] = s.Credits[idx].Clone()
	}

	return cloned
}

type ChunkCommitment struct {
	ChunkIndex uint32
	ChunkRoot  merkle.Hash
}

type ShardAggregationCommitment struct {
	ShardID    int64
	ShardRoot  merkle.Hash
	ChunkCount uint32
}

// AggregationSidecar is data-available to Beacon validators. Normal shards
// receive only their compact AggregationPackage.
type AggregationSidecar struct {
	Receipts          []FinalizedBlockReceipt
	ShardAggregations []ShardAggregation
}

type AggregationBatch struct {
	Header  AggregationBatchHeader
	Sidecar AggregationSidecar
}

type AggregationPackage struct {
	Header          AggregationBatchHeader
	Aggregation     ShardAggregation
	ChunkProof      merkle.Proof
	ShardCommitment ShardAggregationCommitment
	ShardProof      merkle.Proof
}

func (p AggregationPackage) Clone() AggregationPackage {
	cloned := p
	cloned.Header.Cuts = append([]ShardCut(nil), p.Header.Cuts...)
	cloned.Aggregation = p.Aggregation.Clone()
	cloned.ChunkProof.Steps = append([]merkle.Step(nil), p.ChunkProof.Steps...)
	cloned.ShardProof.Steps = append([]merkle.Step(nil), p.ShardProof.Steps...)

	return cloned
}

func (p AggregationBatch) Clone() AggregationBatch {
	cloned := p
	cloned.Header.Cuts = append([]ShardCut(nil), p.Header.Cuts...)

	cloned.Sidecar.Receipts = make([]FinalizedBlockReceipt, len(p.Sidecar.Receipts))
	for idx := range p.Sidecar.Receipts {
		cloned.Sidecar.Receipts[idx] = p.Sidecar.Receipts[idx].Clone()
	}

	cloned.Sidecar.ShardAggregations = make([]ShardAggregation, len(p.Sidecar.ShardAggregations))
	for idx := range p.Sidecar.ShardAggregations {
		cloned.Sidecar.ShardAggregations[idx] = p.Sidecar.ShardAggregations[idx].Clone()
	}

	return cloned
}

func EqualBatchID(left, right merkle.Hash) bool {
	return bytes.Equal(left[:], right[:])
}
