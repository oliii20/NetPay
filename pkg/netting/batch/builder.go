// Package batch deterministically turns a frozen Vector-Cut window into a
// Beacon batch proposal and its two-level shard settlement commitment.
package batch

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"sort"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/commitment"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/matcher"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/window"
)

const ChunkLeafDomain = "BLOCKEMULATOR_SETTLEMENT_CHUNK_V1"

var (
	ErrEmptyWindow       = errors.New("cannot build an empty netting window")
	ErrDuplicateShardCut = errors.New("duplicate shard cut")
	ErrInvalidShardCut   = errors.New("invalid shard cut")
	ErrMissingShardCut   = errors.New("intent references a shard outside the vector cut")
)

type Builder struct{}

func (Builder) Build(frozen window.FrozenWindow, previousBatchID merkle.Hash) (model.BatchProposal, error) {
	if len(frozen.Intents) == 0 {
		return model.BatchProposal{}, ErrEmptyWindow
	}

	cuts, shardIDs, err := canonicalCuts(frozen.Cuts)
	if err != nil {
		return model.BatchProposal{}, err
	}
	matchOutput, err := matcher.Match(frozen.Intents)
	if err != nil {
		return model.BatchProposal{}, fmt.Errorf("match frozen window %d: %w", frozen.WindowID, err)
	}
	results := sortedResults(matchOutput.Results)

	settlements, shardLeaves, err := buildSettlements(frozen.WindowID, shardIDs, results)
	if err != nil {
		return model.BatchProposal{}, err
	}
	roots, err := commitment.BuildRoots(cutLeaves(cuts), resultLeaves(results), shardLeaves)
	if err != nil {
		return model.BatchProposal{}, fmt.Errorf("build batch roots: %w", err)
	}
	matchRoot := commitment.MatchRoot(roots)
	batchID := commitment.BatchID(previousBatchID, frozen.WindowID, matchRoot)
	for idx := range settlements {
		settlements[idx].BatchID = batchID
	}

	return model.BatchProposal{
		Header: model.MatchRootBlockBody{
			BatchID:             batchID,
			PreviousBatchID:     previousBatchID,
			WindowID:            frozen.WindowID,
			CutRoot:             roots.CutRoot,
			IntentResultRoot:    roots.IntentResultRoot,
			ShardSettlementRoot: roots.ShardSettlementRoot,
			MatchRoot:           matchRoot,
			Cuts:                cuts,
		},
		Sidecar: model.BatchSidecar{
			FinalizedBlocks:  cloneReceipts(frozen.Receipts),
			IntentResults:    results,
			ShardSettlements: settlements,
		},
	}, nil
}

func canonicalCuts(input []model.ShardCut) ([]model.ShardCut, []int64, error) {
	cuts := append([]model.ShardCut(nil), input...)
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].ShardID < cuts[j].ShardID })
	shardIDs := make([]int64, len(cuts))
	for idx, cut := range cuts {
		if cut.ShardID < 0 || cut.EndHeight < cut.PreviousHeight {
			return nil, nil, fmt.Errorf("%w: shard %d", ErrInvalidShardCut, cut.ShardID)
		}
		if idx > 0 && cuts[idx-1].ShardID == cut.ShardID {
			return nil, nil, fmt.Errorf("%w: %d", ErrDuplicateShardCut, cut.ShardID)
		}
		shardIDs[idx] = cut.ShardID
	}

	return cuts, shardIDs, nil
}

func buildSettlements(
	windowID uint64,
	shardIDs []int64,
	results []model.IntentResult,
) ([]model.ShardSettlement, []merkle.Leaf, error) {
	byShard := make(map[int64]*model.ShardSettlement, len(shardIDs))
	for _, shardID := range shardIDs {
		byShard[shardID] = &model.ShardSettlement{
			WindowID: windowID, ShardID: shardID, ChunkIndex: 0, ChunkCount: 1,
		}
	}
	for _, result := range results {
		outgoing, ok := byShard[result.Intent.SourceShard]
		if !ok {
			return nil, nil, fmt.Errorf("%w: source shard %d", ErrMissingShardCut, result.Intent.SourceShard)
		}
		incoming, ok := byShard[result.Intent.DestinationShard]
		if !ok {
			return nil, nil, fmt.Errorf("%w: destination shard %d", ErrMissingShardCut, result.Intent.DestinationShard)
		}
		outgoing.Outgoing = append(outgoing.Outgoing, cloneResult(result))
		if result.MatchedAmount.Sign() > 0 {
			incoming.Incoming = append(incoming.Incoming, cloneResult(result))
		}
	}

	settlements := make([]model.ShardSettlement, 0, len(shardIDs))
	shardLeaves := make([]merkle.Leaf, 0, len(shardIDs))
	for _, shardID := range shardIDs {
		settlement := byShard[shardID]
		sortResults(settlement.Outgoing)
		sortResults(settlement.Incoming)
		chunkPayload, err := SettlementPayload(*settlement)
		if err != nil {
			return nil, nil, err
		}
		chunkTree, err := merkle.Build(ChunkLeafDomain, []merkle.Leaf{{
			Key: uint32Key(settlement.ChunkIndex), Payload: chunkPayload,
		}})
		if err != nil {
			return nil, nil, fmt.Errorf("build shard %d chunk tree: %w", shardID, err)
		}
		shardCommitment := model.ShardCommitment{
			ShardID: shardID, ShardRoot: chunkTree.Root(), ChunkCount: settlement.ChunkCount,
		}
		shardLeaves = append(shardLeaves, merkle.Leaf{
			Key: int64Key(shardID), Payload: ShardCommitmentPayload(shardCommitment),
		})
		settlements = append(settlements, settlement.Clone())
	}

	return settlements, shardLeaves, nil
}

func CutPayload(cut model.ShardCut) []byte {
	var out bytes.Buffer
	writeUint64(&out, uint64(cut.ShardID))
	writeUint64(&out, cut.PreviousHeight)
	writeUint64(&out, cut.EndHeight)
	out.Write(cut.EndBlockHash[:])

	return out.Bytes()
}

func ResultPayload(result model.IntentResult) ([]byte, error) {
	if err := result.Validate(); err != nil {
		return nil, fmt.Errorf("validate intent result: %w", err)
	}
	var out bytes.Buffer
	out.Write(result.IntentID[:])
	writeBigInt(&out, result.MatchedAmount)
	writeBigInt(&out, result.FallbackAmount)

	return out.Bytes(), nil
}

func SettlementPayload(settlement model.ShardSettlement) ([]byte, error) {
	if settlement.ShardID < 0 || settlement.ChunkCount == 0 || settlement.ChunkIndex >= settlement.ChunkCount {
		return nil, fmt.Errorf("invalid settlement coordinates")
	}
	var out bytes.Buffer
	writeUint64(&out, settlement.WindowID)
	writeUint64(&out, uint64(settlement.ShardID))
	writeUint32(&out, settlement.ChunkIndex)
	writeUint32(&out, settlement.ChunkCount)
	writeUint32(&out, uint32(len(settlement.Outgoing)))
	for _, result := range settlement.Outgoing {
		payload, err := ResultPayload(result)
		if err != nil {
			return nil, err
		}
		writeBytes(&out, payload)
	}
	writeUint32(&out, uint32(len(settlement.Incoming)))
	for _, result := range settlement.Incoming {
		payload, err := ResultPayload(result)
		if err != nil {
			return nil, err
		}
		writeBytes(&out, payload)
	}

	return out.Bytes(), nil
}

func ShardCommitmentPayload(shard model.ShardCommitment) []byte {
	var out bytes.Buffer
	writeUint64(&out, uint64(shard.ShardID))
	out.Write(shard.ShardRoot[:])
	writeUint32(&out, shard.ChunkCount)

	return out.Bytes()
}

func cutLeaves(cuts []model.ShardCut) []merkle.Leaf {
	leaves := make([]merkle.Leaf, len(cuts))
	for idx, cut := range cuts {
		leaves[idx] = merkle.Leaf{Key: int64Key(cut.ShardID), Payload: CutPayload(cut)}
	}

	return leaves
}

func resultLeaves(results []model.IntentResult) []merkle.Leaf {
	leaves := make([]merkle.Leaf, len(results))
	for idx, result := range results {
		payload, _ := ResultPayload(result)
		leaves[idx] = merkle.Leaf{Key: append([]byte(nil), result.IntentID[:]...), Payload: payload}
	}

	return leaves
}

func sortedResults(input []model.IntentResult) []model.IntentResult {
	results := make([]model.IntentResult, len(input))
	for idx, result := range input {
		results[idx] = cloneResult(result)
	}
	sortResults(results)

	return results
}

func sortResults(results []model.IntentResult) {
	sort.Slice(results, func(i, j int) bool {
		return bytes.Compare(results[i].IntentID[:], results[j].IntentID[:]) < 0
	})
}

func cloneResult(result model.IntentResult) model.IntentResult {
	cloned := result
	cloned.Intent = result.Intent.Clone()
	cloned.MatchedAmount = cloneBigInt(result.MatchedAmount)
	cloned.FallbackAmount = cloneBigInt(result.FallbackAmount)

	return cloned
}

func cloneBigInt(value *big.Int) *big.Int {
	if value == nil {
		return nil
	}

	return new(big.Int).Set(value)
}

func cloneReceipts(receipts []model.FinalizedBlockReceipt) []model.FinalizedBlockReceipt {
	cloned := make([]model.FinalizedBlockReceipt, len(receipts))
	for idx := range receipts {
		cloned[idx] = receipts[idx].Clone()
	}

	return cloned
}

func int64Key(value int64) []byte {
	return uint64Key(uint64(value))
}

func uint32Key(value uint32) []byte {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)

	return encoded[:]
}

func uint64Key(value uint64) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)

	return encoded[:]
}

func writeUint64(out *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	out.Write(encoded[:])
}

func writeUint32(out *bytes.Buffer, value uint32) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	out.Write(encoded[:])
}

func writeBigInt(out *bytes.Buffer, value *big.Int) {
	writeBytes(out, value.Bytes())
}

func writeBytes(out *bytes.Buffer, value []byte) {
	writeUint32(out, uint32(len(value)))
	out.Write(value)
}
