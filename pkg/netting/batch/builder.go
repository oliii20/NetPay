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
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
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
	ErrInvalidSettlement = errors.New("invalid shard settlement collection")
)

type Builder struct {
	MatcherMode         matcher.Mode
	SettlementChunkSize int
}

func (b Builder) Build(frozen window.FrozenWindow, previousBatchID merkle.Hash) (model.BatchProposal, error) {
	proposal, _, err := b.BuildMeasured(frozen, previousBatchID)

	return proposal, err
}

func (b Builder) BuildMeasured(
	frozen window.FrozenWindow,
	previousBatchID merkle.Hash,
) (model.BatchProposal, model.NettingBatchMetric, error) {
	if len(frozen.Intents) == 0 {
		return model.BatchProposal{}, model.NettingBatchMetric{}, ErrEmptyWindow
	}
	matcherMode := matcher.NormalizeMode(b.MatcherMode)

	cuts, shardIDs, err := canonicalCuts(frozen.Cuts)
	if err != nil {
		return model.BatchProposal{}, model.NettingBatchMetric{}, err
	}
	matchStarted := time.Now()
	matchOutput, err := matcher.MatchWithMode(frozen.Intents, matcherMode)
	if err != nil {
		return model.BatchProposal{}, model.NettingBatchMetric{}, fmt.Errorf("match frozen window %d: %w", frozen.WindowID, err)
	}
	matchTime := time.Since(matchStarted)
	results := sortedResults(matchOutput.Results)
	chunkSize := normalizeSettlementChunkSize(b.SettlementChunkSize)

	merkleStarted := time.Now()
	settlements, shardLeaves, err := buildSettlements(frozen.WindowID, shardIDs, results, chunkSize)
	if err != nil {
		return model.BatchProposal{}, model.NettingBatchMetric{}, err
	}
	roots, err := commitment.BuildRoots(cutLeaves(cuts), resultLeaves(results), shardLeaves)
	if err != nil {
		return model.BatchProposal{}, model.NettingBatchMetric{}, fmt.Errorf("build batch roots: %w", err)
	}
	merkleBuildTime := time.Since(merkleStarted)
	matchRoot := commitment.MatchRoot(roots)
	batchID := commitment.BatchID(previousBatchID, frozen.WindowID, matchRoot)
	for idx := range settlements {
		settlements[idx].BatchID = batchID
	}

	proposal := model.BatchProposal{
		Header: model.MatchRootBlockBody{
			BatchID:             batchID,
			PreviousBatchID:     previousBatchID,
			WindowID:            frozen.WindowID,
			MatcherMode:         string(matcherMode),
			SettlementChunkSize: uint32(chunkSize),
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
	}
	metric := summarizeBatchMetric(frozen, proposal.Header.BatchID, matchOutput, matchTime, merkleBuildTime)

	return proposal, metric, nil
}

func summarizeBatchMetric(
	frozen window.FrozenWindow,
	batchID merkle.Hash,
	output matcher.Output,
	matchTime, merkleBuildTime time.Duration,
) model.NettingBatchMetric {
	metric := model.NettingBatchMetric{
		BatchID: batchID, WindowID: frozen.WindowID, CloseReason: frozen.CloseReason,
		IntentCount: len(frozen.Intents), WindowOpenDuration: frozen.SealedAt.Sub(frozen.OpenedAt),
		MatchTime: matchTime, MerkleBuildTime: merkleBuildTime, BuiltAt: time.Now(),
		OriginalValue: "0", MatchedValue: "0", FallbackValue: "0",
	}
	if metric.CloseReason == "" {
		metric.CloseReason = "unspecified"
	}
	metric.Shards, metric.WatermarkSkew = summarizeCuts(frozen.Cuts)
	phaseIntents := map[matcher.Phase]map[intent.ID]struct{}{
		matcher.ExactPhase: {}, matcher.BestFitPhase: {}, matcher.SplitPhase: {},
	}
	for _, allocation := range output.Allocations {
		phaseIntents[allocation.Phase][allocation.LowerToHigherIntentID] = struct{}{}
		phaseIntents[allocation.Phase][allocation.HigherToLowerIntentID] = struct{}{}
		if allocation.Phase == matcher.SplitPhase {
			metric.SplitAllocationCount++
		}
	}
	metric.ExactMatchedIntentCount = len(phaseIntents[matcher.ExactPhase])
	metric.BestFitMatchedIntentCount = len(phaseIntents[matcher.BestFitPhase])
	metric.SplitMatchedIntentCount = len(phaseIntents[matcher.SplitPhase])
	original, matched, fallback := new(big.Int), new(big.Int), new(big.Int)
	for _, result := range output.Results {
		original.Add(original, result.Intent.Amount)
		matched.Add(matched, result.MatchedAmount)
		fallback.Add(fallback, result.FallbackAmount)
		if result.MatchedAmount.Sign() > 0 {
			metric.MatchedIntentCount++
		}
		if result.FallbackAmount.Sign() > 0 {
			metric.FallbackIntentCount++
		}
	}
	metric.OriginalValue = original.String()
	metric.MatchedValue = matched.String()
	metric.FallbackValue = fallback.String()

	return metric
}

func summarizeCuts(cuts []model.ShardCut) ([]model.ShardWindowMetric, uint64) {
	shards := make([]model.ShardWindowMetric, len(cuts))
	var minimum, maximum uint64
	for idx, cut := range cuts {
		shards[idx] = model.ShardWindowMetric{
			ShardID: cut.ShardID, BlockCount: cut.EndHeight - cut.PreviousHeight, CutHeight: cut.EndHeight,
		}
		if idx == 0 || cut.EndHeight < minimum {
			minimum = cut.EndHeight
		}
		if idx == 0 || cut.EndHeight > maximum {
			maximum = cut.EndHeight
		}
	}
	sort.Slice(shards, func(i, j int) bool { return shards[i].ShardID < shards[j].ShardID })

	return shards, maximum - minimum
}

func BuildSettlementPackages(proposal model.BatchProposal) ([]model.SettlementPackage, error) {
	byShard := make(map[int64][]model.ShardSettlement)
	for _, settlement := range proposal.Sidecar.ShardSettlements {
		byShard[settlement.ShardID] = append(byShard[settlement.ShardID], settlement.Clone())
	}
	shardIDs := make([]int64, 0, len(byShard))
	for shardID := range byShard {
		shardIDs = append(shardIDs, shardID)
	}
	sort.Slice(shardIDs, func(i, j int) bool { return shardIDs[i] < shardIDs[j] })

	type shardProofData struct {
		commitment model.ShardCommitment
		tree       *merkle.Tree
	}
	proofData := make(map[int64]shardProofData, len(shardIDs))
	shardLeaves := make([]merkle.Leaf, 0, len(shardIDs))
	for _, shardID := range shardIDs {
		settlements := byShard[shardID]
		sort.Slice(settlements, func(i, j int) bool { return settlements[i].ChunkIndex < settlements[j].ChunkIndex })
		chunkLeaves := make([]merkle.Leaf, len(settlements))
		for idx, settlement := range settlements {
			if settlement.ChunkCount != uint32(len(settlements)) || settlement.ChunkIndex != uint32(idx) {
				return nil, fmt.Errorf("%w: shard %d chunk %d", ErrInvalidSettlement, shardID, settlement.ChunkIndex)
			}
			payload, err := SettlementPayload(settlement)
			if err != nil {
				return nil, err
			}
			chunkLeaves[idx] = merkle.Leaf{Key: uint32Key(settlement.ChunkIndex), Payload: payload}
		}
		chunkTree, err := merkle.Build(ChunkLeafDomain, chunkLeaves)
		if err != nil {
			return nil, fmt.Errorf("build shard %d chunk tree: %w", shardID, err)
		}
		shardCommitment := model.ShardCommitment{
			ShardID: shardID, ShardRoot: chunkTree.Root(), ChunkCount: uint32(len(settlements)),
		}
		proofData[shardID] = shardProofData{commitment: shardCommitment, tree: chunkTree}
		shardLeaves = append(shardLeaves, merkle.Leaf{
			Key: int64Key(shardID), Payload: ShardCommitmentPayload(shardCommitment),
		})
	}
	shardTree, err := merkle.Build(commitment.ShardSettlementLeafDomain, shardLeaves)
	if err != nil {
		return nil, fmt.Errorf("build global shard settlement tree: %w", err)
	}
	if shardTree.Root() != proposal.Header.ShardSettlementRoot {
		return nil, ErrInvalidSettlement
	}

	packages := make([]model.SettlementPackage, 0, len(proposal.Sidecar.ShardSettlements))
	for _, shardID := range shardIDs {
		data := proofData[shardID]
		shardProof, proofErr := shardTree.Proof(int64Key(shardID))
		if proofErr != nil {
			return nil, proofErr
		}
		for _, settlement := range byShard[shardID] {
			chunkProof, proofErr := data.tree.Proof(uint32Key(settlement.ChunkIndex))
			if proofErr != nil {
				return nil, proofErr
			}
			packages = append(packages, model.SettlementPackage{
				Header: proposal.Header, Settlement: settlement.Clone(), ChunkProof: chunkProof,
				ShardCommitment: data.commitment, ShardProof: shardProof,
			})
		}
	}

	return packages, nil
}

func VerifySettlementPackage(pack model.SettlementPackage) error {
	if pack.Settlement.BatchID != pack.Header.BatchID ||
		pack.Settlement.WindowID != pack.Header.WindowID ||
		pack.Settlement.ShardID != pack.ShardCommitment.ShardID ||
		pack.Settlement.ChunkCount != pack.ShardCommitment.ChunkCount {
		return ErrInvalidSettlement
	}
	chunkPayload, err := SettlementPayload(pack.Settlement)
	if err != nil {
		return err
	}
	if !merkle.Verify(ChunkLeafDomain, chunkPayload, pack.ChunkProof, pack.ShardCommitment.ShardRoot) {
		return ErrInvalidSettlement
	}
	if !merkle.Verify(
		commitment.ShardSettlementLeafDomain,
		ShardCommitmentPayload(pack.ShardCommitment),
		pack.ShardProof,
		pack.Header.ShardSettlementRoot,
	) {
		return ErrInvalidSettlement
	}
	roots := commitment.Roots{
		CutRoot: pack.Header.CutRoot, IntentResultRoot: pack.Header.IntentResultRoot,
		ShardSettlementRoot: pack.Header.ShardSettlementRoot,
	}
	if commitment.MatchRoot(roots) != pack.Header.MatchRoot ||
		commitment.BatchID(pack.Header.PreviousBatchID, pack.Header.WindowID, pack.Header.MatchRoot) != pack.Header.BatchID {
		return ErrInvalidSettlement
	}

	return nil
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
	chunkSize int,
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
		if result.FallbackAmount.Sign() > 0 {
			incoming.FallbackIncoming = append(incoming.FallbackIncoming, cloneResult(result))
		}
	}

	settlements := make([]model.ShardSettlement, 0, len(shardIDs))
	shardLeaves := make([]merkle.Leaf, 0, len(shardIDs))
	for _, shardID := range shardIDs {
		settlement := byShard[shardID]
		sortResults(settlement.Outgoing)
		sortResults(settlement.Incoming)
		sortResults(settlement.FallbackIncoming)
		chunks, err := splitSettlement(*settlement, chunkSize)
		if err != nil {
			return nil, nil, err
		}
		chunkLeaves := make([]merkle.Leaf, len(chunks))
		for idx, chunk := range chunks {
			chunkPayload, payloadErr := SettlementPayload(chunk)
			if payloadErr != nil {
				return nil, nil, payloadErr
			}
			chunkLeaves[idx] = merkle.Leaf{Key: uint32Key(chunk.ChunkIndex), Payload: chunkPayload}
		}
		chunkTree, err := merkle.Build(ChunkLeafDomain, chunkLeaves)
		if err != nil {
			return nil, nil, fmt.Errorf("build shard %d chunk tree: %w", shardID, err)
		}
		shardCommitment := model.ShardCommitment{
			ShardID: shardID, ShardRoot: chunkTree.Root(), ChunkCount: uint32(len(chunks)),
		}
		shardLeaves = append(shardLeaves, merkle.Leaf{
			Key: int64Key(shardID), Payload: ShardCommitmentPayload(shardCommitment),
		})
		settlements = append(settlements, chunks...)
	}

	return settlements, shardLeaves, nil
}

func normalizeSettlementChunkSize(size int) int {
	if size <= 0 {
		return 0
	}

	return size
}

type settlementDraft struct {
	Outgoing         []model.IntentResult
	Incoming         []model.IntentResult
	FallbackIncoming []model.IntentResult
}

type settlementGroupKey struct {
	Counterparty int64
	AssetID      intent.AssetID
}

type matchedSettlementGroup struct {
	Outgoing []model.IntentResult
	Incoming []model.IntentResult
}

func splitSettlement(settlement model.ShardSettlement, chunkSize int) ([]model.ShardSettlement, error) {
	if chunkSize <= 0 {
		settlement.ChunkIndex = 0
		settlement.ChunkCount = 1

		return []model.ShardSettlement{settlement.Clone()}, nil
	}

	matchedGroups := make(map[settlementGroupKey]*matchedSettlementGroup)
	fallbackOutgoing := make([]model.IntentResult, 0)
	for _, result := range settlement.Outgoing {
		if result.MatchedAmount.Sign() == 0 {
			fallbackOutgoing = append(fallbackOutgoing, cloneResult(result))
			continue
		}
		key := settlementGroupKey{Counterparty: result.Intent.DestinationShard, AssetID: result.Intent.AssetID}
		group := matchedGroups[key]
		if group == nil {
			group = &matchedSettlementGroup{}
			matchedGroups[key] = group
		}
		group.Outgoing = append(group.Outgoing, cloneResult(result))
	}
	for _, result := range settlement.Incoming {
		key := settlementGroupKey{Counterparty: result.Intent.SourceShard, AssetID: result.Intent.AssetID}
		group := matchedGroups[key]
		if group == nil {
			group = &matchedSettlementGroup{}
			matchedGroups[key] = group
		}
		group.Incoming = append(group.Incoming, cloneResult(result))
	}

	drafts := make([]settlementDraft, 0)
	keys := make([]settlementGroupKey, 0, len(matchedGroups))
	for key := range matchedGroups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Counterparty != keys[j].Counterparty {
			return keys[i].Counterparty < keys[j].Counterparty
		}

		return bytes.Compare(keys[i].AssetID[:], keys[j].AssetID[:]) < 0
	})
	for _, key := range keys {
		group := matchedGroups[key]
		sortResults(group.Outgoing)
		sortResults(group.Incoming)
		groupDrafts, err := splitMatchedGroup(group.Outgoing, group.Incoming, chunkSize)
		if err != nil {
			return nil, err
		}
		drafts = append(drafts, groupDrafts...)
	}
	drafts = append(drafts, splitOutgoingOnly(fallbackOutgoing, chunkSize)...)
	drafts = append(drafts, splitFallbackIncoming(settlement.FallbackIncoming, chunkSize)...)
	if len(drafts) == 0 {
		drafts = append(drafts, settlementDraft{})
	}

	chunks := make([]model.ShardSettlement, 0, len(drafts))
	for idx, draft := range drafts {
		chunk := model.ShardSettlement{
			BatchID: settlement.BatchID, WindowID: settlement.WindowID, ShardID: settlement.ShardID,
			ChunkIndex: uint32(idx), ChunkCount: uint32(len(drafts)),
			Outgoing: cloneResults(draft.Outgoing), Incoming: cloneResults(draft.Incoming),
			FallbackIncoming: cloneResults(draft.FallbackIncoming),
		}
		sortResults(chunk.Outgoing)
		sortResults(chunk.Incoming)
		sortResults(chunk.FallbackIncoming)
		chunks = append(chunks, chunk)
	}

	return chunks, nil
}

func splitMatchedGroup(
	outgoing []model.IntentResult,
	incoming []model.IntentResult,
	chunkSize int,
) ([]settlementDraft, error) {
	if sumMatched(outgoing).Cmp(sumMatched(incoming)) != 0 {
		return nil, ErrInvalidSettlement
	}
	drafts := make([]settlementDraft, 0)
	var current settlementDraft
	outSum := new(big.Int)
	inSum := new(big.Int)
	outIdx, inIdx := 0, 0
	for outIdx < len(outgoing) || inIdx < len(incoming) {
		if inIdx >= len(incoming) || (outIdx < len(outgoing) && outSum.Cmp(inSum) <= 0) {
			current.Outgoing = append(current.Outgoing, cloneResult(outgoing[outIdx]))
			outSum.Add(outSum, outgoing[outIdx].MatchedAmount)
			outIdx++
		} else {
			current.Incoming = append(current.Incoming, cloneResult(incoming[inIdx]))
			inSum.Add(inSum, incoming[inIdx].MatchedAmount)
			inIdx++
		}
		if outSum.Cmp(inSum) == 0 && draftSize(current) >= chunkSize {
			drafts = append(drafts, current)
			current = settlementDraft{}
			outSum.SetInt64(0)
			inSum.SetInt64(0)
		}
	}
	if draftSize(current) > 0 {
		if outSum.Cmp(inSum) != 0 {
			return nil, ErrInvalidSettlement
		}
		drafts = append(drafts, current)
	}

	return drafts, nil
}

func splitOutgoingOnly(results []model.IntentResult, chunkSize int) []settlementDraft {
	sortResults(results)
	drafts := make([]settlementDraft, 0)
	for len(results) > 0 {
		take := min(chunkSize, len(results))
		drafts = append(drafts, settlementDraft{Outgoing: cloneResults(results[:take])})
		results = results[take:]
	}

	return drafts
}

func splitFallbackIncoming(results []model.IntentResult, chunkSize int) []settlementDraft {
	results = cloneResults(results)
	sortResults(results)
	drafts := make([]settlementDraft, 0)
	for len(results) > 0 {
		take := min(chunkSize, len(results))
		drafts = append(drafts, settlementDraft{FallbackIncoming: cloneResults(results[:take])})
		results = results[take:]
	}

	return drafts
}

func sumMatched(results []model.IntentResult) *big.Int {
	sum := new(big.Int)
	for _, result := range results {
		sum.Add(sum, result.MatchedAmount)
	}

	return sum
}

func draftSize(draft settlementDraft) int {
	return len(draft.Outgoing) + len(draft.Incoming) + len(draft.FallbackIncoming)
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
	writeUint32(&out, uint32(len(settlement.FallbackIncoming)))
	for _, result := range settlement.FallbackIncoming {
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

func cloneResults(results []model.IntentResult) []model.IntentResult {
	cloned := make([]model.IntentResult, len(results))
	for idx, result := range results {
		cloned[idx] = cloneResult(result)
	}

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
