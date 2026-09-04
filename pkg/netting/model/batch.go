package model

import (
	"bytes"
	"math/big"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
)

// MatchRootBlockBody is the compact batch record committed by the Beacon chain.
type MatchRootBlockBody struct {
	BatchID             merkle.Hash
	PreviousBatchID     merkle.Hash
	WindowID            uint64
	CutRoot             merkle.Hash
	IntentResultRoot    merkle.Hash
	ShardSettlementRoot merkle.Hash
	MatchRoot           merkle.Hash
	Cuts                []ShardCut
}

// ShardSettlement is one atomic execution unit for a normal shard. BatchID is
// routing metadata and is deliberately excluded from the Merkle payload.
type ShardSettlement struct {
	BatchID    merkle.Hash
	WindowID   uint64
	ShardID    int64
	ChunkIndex uint32
	ChunkCount uint32
	Outgoing   []IntentResult
	Incoming   []IntentResult
}

type ChunkCommitment struct {
	ChunkIndex uint32
	ChunkRoot  merkle.Hash
}

type ShardCommitment struct {
	ShardID    int64
	ShardRoot  merkle.Hash
	ChunkCount uint32
}

type BatchSidecar struct {
	FinalizedBlocks  []FinalizedBlockReceipt
	IntentResults    []IntentResult
	ShardSettlements []ShardSettlement
}

type BatchProposal struct {
	Header  MatchRootBlockBody
	Sidecar BatchSidecar
}

type SettlementPackage struct {
	Header          MatchRootBlockBody
	Settlement      ShardSettlement
	ChunkProof      merkle.Proof
	ShardCommitment ShardCommitment
	ShardProof      merkle.Proof
}

func (p SettlementPackage) Clone() SettlementPackage {
	cloned := p
	cloned.Header.Cuts = append([]ShardCut(nil), p.Header.Cuts...)
	cloned.Settlement = p.Settlement.Clone()
	cloned.ChunkProof.Steps = append([]merkle.Step(nil), p.ChunkProof.Steps...)
	cloned.ShardProof.Steps = append([]merkle.Step(nil), p.ShardProof.Steps...)

	return cloned
}

func (p BatchProposal) Clone() BatchProposal {
	cloned := p
	cloned.Header.Cuts = append([]ShardCut(nil), p.Header.Cuts...)
	cloned.Sidecar.FinalizedBlocks = make([]FinalizedBlockReceipt, len(p.Sidecar.FinalizedBlocks))
	for idx := range p.Sidecar.FinalizedBlocks {
		cloned.Sidecar.FinalizedBlocks[idx] = p.Sidecar.FinalizedBlocks[idx].Clone()
	}
	cloned.Sidecar.IntentResults = cloneIntentResults(p.Sidecar.IntentResults)
	cloned.Sidecar.ShardSettlements = make([]ShardSettlement, len(p.Sidecar.ShardSettlements))
	for idx := range p.Sidecar.ShardSettlements {
		cloned.Sidecar.ShardSettlements[idx] = p.Sidecar.ShardSettlements[idx].Clone()
	}

	return cloned
}

func (s ShardSettlement) Clone() ShardSettlement {
	cloned := s
	cloned.Outgoing = cloneIntentResults(s.Outgoing)
	cloned.Incoming = cloneIntentResults(s.Incoming)

	return cloned
}

func cloneIntentResults(results []IntentResult) []IntentResult {
	cloned := make([]IntentResult, len(results))
	for idx, result := range results {
		cloned[idx] = result
		cloned[idx].Intent = CloneIntent(result.Intent)
		cloned[idx].MatchedAmount = cloneBigInt(result.MatchedAmount)
		cloned[idx].FallbackAmount = cloneBigInt(result.FallbackAmount)
	}

	return cloned
}

func cloneBigInt(value *big.Int) *big.Int {
	if value == nil {
		return nil
	}

	return new(big.Int).Set(value)
}

func EqualBatchID(left, right merkle.Hash) bool {
	return bytes.Equal(left[:], right[:])
}
