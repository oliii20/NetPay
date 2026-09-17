package beacon

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"sort"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/matcher"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/window"
)

var (
	ErrBatchSequence        = errors.New("invalid Beacon batch sequence")
	ErrCutSequence          = errors.New("invalid vector-cut sequence")
	ErrReceiptCoverage      = errors.New("finalized receipts do not cover vector cut")
	ErrDuplicateBatchIntent = errors.New("duplicate or previously consumed batch intent")
	ErrExpiredBatchIntent   = errors.New("batch contains expired intent")
	ErrResultCoverage       = errors.New("intent results do not cover batch intents")
	ErrMatchedConservation  = errors.New("matched amount does not conserve by shard pair and asset")
	ErrSettlementCoverage   = errors.New("settlements do not cover intent results")
	ErrInvalidCommitment    = errors.New("invalid Beacon batch commitment")
	ErrDerivedBatchMismatch = errors.New("batch differs from deterministic derivation")
)

type ValidationMode string

const (
	ValidationModeLight ValidationMode = "light"
	ValidationModeFull  ValidationMode = "full"
)

type StateReader interface {
	Tip() (MatchRootBlock, bool, error)
	HasIntent(intent.ID) (bool, error)
}

type Validator struct {
	state StateReader
	mode  ValidationMode
}

func NewValidator(state StateReader) *Validator {
	return NewValidatorWithMode(state, ValidationModeLight)
}

func NewFullValidator(state StateReader) *Validator {
	return NewValidatorWithMode(state, ValidationModeFull)
}

func NewValidatorWithMode(state StateReader, mode ValidationMode) *Validator {
	return &Validator{state: state, mode: normalizeValidationMode(mode)}
}

func (v *Validator) Validate(proposal model.BatchProposal) error {
	tip, hasTip, err := v.state.Tip()
	if err != nil {
		return err
	}
	if err = validateSequence(proposal.Header, tip, hasTip); err != nil {
		return err
	}
	intents, acceptanceEpoch, err := validateReceiptCoverage(proposal, tip, hasTip)
	if err != nil {
		return err
	}
	seen := make(map[intent.ID]struct{}, len(intents))
	for _, payment := range intents {
		id, idErr := payment.ID()
		if idErr != nil {
			return fmt.Errorf("calculate batch intent ID: %w", idErr)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("%w: %x", ErrDuplicateBatchIntent, id)
		}
		consumed, stateErr := v.state.HasIntent(id)
		if stateErr != nil {
			return stateErr
		}
		if consumed {
			return fmt.Errorf("%w: %x", ErrDuplicateBatchIntent, id)
		}
		if payment.ExpiryEpoch <= acceptanceEpoch {
			return fmt.Errorf("%w: %x", ErrExpiredBatchIntent, id)
		}
		seen[id] = struct{}{}
	}
	for _, result := range proposal.Sidecar.IntentResults {
		if err = result.Validate(); err != nil {
			return fmt.Errorf("validate intent result: %w", err)
		}
	}
	if err = validateResultCoverage(intents, proposal.Sidecar.IntentResults); err != nil {
		return err
	}
	if err = validateMatchedConservation(proposal.Sidecar.IntentResults); err != nil {
		return err
	}
	if err = validateSettlementCoverage(proposal.Header, proposal.Sidecar.IntentResults, proposal.Sidecar.ShardSettlements); err != nil {
		return err
	}
	if err = batch.VerifyProposalCommitments(proposal); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidCommitment, err)
	}
	if v.mode == ValidationModeFull {
		return validateDerivedBatch(proposal, intents)
	}

	return nil
}

func validateDerivedBatch(proposal model.BatchProposal, intents []intent.PaymentIntent) error {
	expected, err := (batch.Builder{
		MatcherMode:         matcher.Mode(proposal.Header.MatcherMode),
		SettlementChunkSize: int(proposal.Header.SettlementChunkSize),
	}).Build(window.FrozenWindow{
		WindowID: proposal.Header.WindowID,
		Cuts:     append([]model.ShardCut(nil), proposal.Header.Cuts...),
		Receipts: cloneFinalizedReceipts(proposal.Sidecar.FinalizedBlocks),
		Intents:  clonePayments(intents),
	}, proposal.Header.PreviousBatchID)
	if err != nil {
		return fmt.Errorf("derive batch proposal: %w", err)
	}
	if !reflect.DeepEqual(expected.Header, proposal.Header) ||
		!reflect.DeepEqual(expected.Sidecar.IntentResults, proposal.Sidecar.IntentResults) ||
		!equalShardSettlements(expected.Sidecar.ShardSettlements, proposal.Sidecar.ShardSettlements) {
		return ErrDerivedBatchMismatch
	}

	return nil
}

func normalizeValidationMode(mode ValidationMode) ValidationMode {
	switch mode {
	case "", ValidationModeLight:
		return ValidationModeLight
	case ValidationModeFull:
		return ValidationModeFull
	default:
		return ValidationModeLight
	}
}

func validateResultCoverage(intents []intent.PaymentIntent, results []model.IntentResult) error {
	if len(intents) == 0 {
		return ErrResultCoverage
	}
	expected := make(map[intent.ID]struct{}, len(intents))
	for _, payment := range intents {
		id, err := payment.ID()
		if err != nil {
			return fmt.Errorf("calculate batch intent ID: %w", err)
		}
		expected[id] = struct{}{}
	}
	seen := make(map[intent.ID]struct{}, len(results))
	for _, result := range results {
		if _, ok := expected[result.IntentID]; !ok {
			return fmt.Errorf("%w: unexpected result %x", ErrResultCoverage, result.IntentID)
		}
		if _, exists := seen[result.IntentID]; exists {
			return fmt.Errorf("%w: duplicate result %x", ErrResultCoverage, result.IntentID)
		}
		seen[result.IntentID] = struct{}{}
	}
	if len(seen) != len(expected) {
		return ErrResultCoverage
	}

	return nil
}

type shardPairAsset struct {
	lower   int64
	higher  int64
	assetID intent.AssetID
}

type directionalMatched struct {
	lowerToHigher *big.Int
	higherToLower *big.Int
}

func validateMatchedConservation(results []model.IntentResult) error {
	groups := make(map[shardPairAsset]*directionalMatched)
	for _, result := range results {
		source, destination := result.Intent.SourceShard, result.Intent.DestinationShard
		if source < 0 || destination < 0 || source == destination {
			return fmt.Errorf("%w: invalid shard pair %d -> %d", ErrMatchedConservation, source, destination)
		}
		lower, higher := source, destination
		lowerToHigher := true
		if lower > higher {
			lower, higher = higher, lower
			lowerToHigher = false
		}
		key := shardPairAsset{lower: lower, higher: higher, assetID: result.Intent.AssetID}
		group := groups[key]
		if group == nil {
			group = &directionalMatched{lowerToHigher: new(big.Int), higherToLower: new(big.Int)}
			groups[key] = group
		}
		if lowerToHigher {
			group.lowerToHigher.Add(group.lowerToHigher, result.MatchedAmount)
		} else {
			group.higherToLower.Add(group.higherToLower, result.MatchedAmount)
		}
	}
	for key, group := range groups {
		if group.lowerToHigher.Cmp(group.higherToLower) != 0 {
			return fmt.Errorf("%w: shards %d-%d", ErrMatchedConservation, key.lower, key.higher)
		}
	}

	return nil
}

type settlementOccurrence struct {
	shardID  int64
	intentID intent.ID
	role     string
}

func validateSettlementCoverage(
	header model.MatchRootBlockBody,
	results []model.IntentResult,
	settlements []model.ShardSettlement,
) error {
	byID := make(map[intent.ID]model.IntentResult, len(results))
	expected := make(map[settlementOccurrence]int, len(results)*3)
	for _, result := range results {
		byID[result.IntentID] = result
		expected[settlementOccurrence{shardID: result.Intent.SourceShard, intentID: result.IntentID, role: "outgoing"}]++
		if result.MatchedAmount.Sign() > 0 {
			expected[settlementOccurrence{shardID: result.Intent.DestinationShard, intentID: result.IntentID, role: "incoming"}]++
		}
		if result.FallbackAmount.Sign() > 0 {
			expected[settlementOccurrence{shardID: result.Intent.DestinationShard, intentID: result.IntentID, role: "fallback_incoming"}]++
		}
	}
	cutShards := make(map[int64]struct{}, len(header.Cuts))
	for _, cut := range header.Cuts {
		cutShards[cut.ShardID] = struct{}{}
	}
	for _, settlement := range settlements {
		if settlement.BatchID != header.BatchID || settlement.WindowID != header.WindowID {
			return ErrSettlementCoverage
		}
		if _, ok := cutShards[settlement.ShardID]; !ok {
			return fmt.Errorf("%w: shard %d outside vector cut", ErrSettlementCoverage, settlement.ShardID)
		}
		if err := consumeSettlementResults(expected, byID, settlement.ShardID, "outgoing", settlement.Outgoing); err != nil {
			return err
		}
		if err := consumeSettlementResults(expected, byID, settlement.ShardID, "incoming", settlement.Incoming); err != nil {
			return err
		}
		if err := consumeSettlementResults(
			expected, byID, settlement.ShardID, "fallback_incoming", settlement.FallbackIncoming,
		); err != nil {
			return err
		}
	}
	for occurrence, count := range expected {
		if count != 0 {
			return fmt.Errorf("%w: missing %s for shard %d intent %x",
				ErrSettlementCoverage, occurrence.role, occurrence.shardID, occurrence.intentID)
		}
	}

	return nil
}

func consumeSettlementResults(
	expected map[settlementOccurrence]int,
	byID map[intent.ID]model.IntentResult,
	shardID int64,
	role string,
	results []model.IntentResult,
) error {
	for _, result := range results {
		canonical, ok := byID[result.IntentID]
		if !ok || !reflect.DeepEqual(canonical, result) {
			return fmt.Errorf("%w: unexpected %s result %x", ErrSettlementCoverage, role, result.IntentID)
		}
		key := settlementOccurrence{shardID: shardID, intentID: result.IntentID, role: role}
		expected[key]--
		if expected[key] < 0 {
			return fmt.Errorf("%w: duplicate %s result %x", ErrSettlementCoverage, role, result.IntentID)
		}
	}

	return nil
}

func equalShardSettlements(expected, actual []model.ShardSettlement) bool {
	if len(expected) != len(actual) {
		return false
	}
	for idx := range expected {
		left, right := expected[idx], actual[idx]
		if left.BatchID != right.BatchID || left.WindowID != right.WindowID || left.ShardID != right.ShardID ||
			left.ChunkIndex != right.ChunkIndex || left.ChunkCount != right.ChunkCount ||
			!equalIntentResults(left.Outgoing, right.Outgoing) || !equalIntentResults(left.Incoming, right.Incoming) ||
			!equalIntentResults(left.FallbackIncoming, right.FallbackIncoming) {
			return false
		}
	}

	return true
}

func equalIntentResults(expected, actual []model.IntentResult) bool {
	if len(expected) != len(actual) {
		return false
	}
	for idx := range expected {
		if !reflect.DeepEqual(expected[idx], actual[idx]) {
			return false
		}
	}

	return true
}

func validateSequence(header model.MatchRootBlockBody, tip MatchRootBlock, hasTip bool) error {
	if !hasTip {
		if header.WindowID != 1 || header.PreviousBatchID != (merkle.Hash{}) {
			return ErrBatchSequence
		}
		return nil
	}
	if header.WindowID != tip.Body.WindowID+1 || header.PreviousBatchID != tip.Body.BatchID {
		return ErrBatchSequence
	}

	return nil
}

func validateReceiptCoverage(
	proposal model.BatchProposal,
	tip MatchRootBlock,
	hasTip bool,
) ([]intent.PaymentIntent, uint64, error) {
	previousCuts := make(map[int64]model.ShardCut)
	if hasTip {
		for _, cut := range tip.Body.Cuts {
			previousCuts[cut.ShardID] = cut
		}
	}
	receiptsByShard := make(map[int64][]model.FinalizedBlockReceipt)
	var acceptanceEpoch uint64
	for _, receipt := range proposal.Sidecar.FinalizedBlocks {
		receiptsByShard[receipt.ShardID] = append(receiptsByShard[receipt.ShardID], receipt.Clone())
		if receipt.Epoch >= 0 && uint64(receipt.Epoch) > acceptanceEpoch {
			acceptanceEpoch = uint64(receipt.Epoch)
		}
	}

	intents := make([]intent.PaymentIntent, 0)
	seenShards := make(map[int64]struct{}, len(proposal.Header.Cuts))
	for _, cut := range proposal.Header.Cuts {
		if _, exists := seenShards[cut.ShardID]; exists || cut.EndHeight < cut.PreviousHeight {
			return nil, 0, fmt.Errorf("%w: shard %d", ErrCutSequence, cut.ShardID)
		}
		seenShards[cut.ShardID] = struct{}{}
		previous, hasPrevious := previousCuts[cut.ShardID]
		if hasTip && (!hasPrevious || cut.PreviousHeight != previous.EndHeight) {
			return nil, 0, fmt.Errorf("%w: shard %d", ErrCutSequence, cut.ShardID)
		}
		receipts := receiptsByShard[cut.ShardID]
		sort.Slice(receipts, func(i, j int) bool { return receipts[i].Height < receipts[j].Height })
		if uint64(len(receipts)) != cut.EndHeight-cut.PreviousHeight {
			return nil, 0, fmt.Errorf("%w: shard %d", ErrReceiptCoverage, cut.ShardID)
		}
		parent := merkle.Hash{}
		if hasPrevious {
			parent = previous.EndBlockHash
		}
		for idx, receipt := range receipts {
			expectedHeight := cut.PreviousHeight + uint64(idx) + 1
			if receipt.Height != expectedHeight || receipt.ShardID != cut.ShardID {
				return nil, 0, fmt.Errorf("%w: shard %d height %d", ErrReceiptCoverage, cut.ShardID, receipt.Height)
			}
			if (idx > 0 || hasPrevious) && !bytes.Equal(receipt.ParentHash[:], parent[:]) {
				return nil, 0, fmt.Errorf("%w: parent shard %d height %d", ErrReceiptCoverage, cut.ShardID, receipt.Height)
			}
			parent = receipt.BlockHash
			intents = append(intents, clonePayments(receipt.Intents)...)
		}
		if len(receipts) > 0 && parent != cut.EndBlockHash {
			return nil, 0, fmt.Errorf("%w: end hash shard %d", ErrReceiptCoverage, cut.ShardID)
		}
		if len(receipts) == 0 && hasPrevious && cut.EndBlockHash != previous.EndBlockHash {
			return nil, 0, fmt.Errorf("%w: empty cut hash shard %d", ErrReceiptCoverage, cut.ShardID)
		}
		delete(receiptsByShard, cut.ShardID)
	}
	if len(receiptsByShard) != 0 {
		return nil, 0, ErrReceiptCoverage
	}

	return intents, acceptanceEpoch, nil
}

func cloneFinalizedReceipts(input []model.FinalizedBlockReceipt) []model.FinalizedBlockReceipt {
	cloned := make([]model.FinalizedBlockReceipt, len(input))
	for idx := range input {
		cloned[idx] = input[idx].Clone()
	}

	return cloned
}

func clonePayments(input []intent.PaymentIntent) []intent.PaymentIntent {
	cloned := make([]intent.PaymentIntent, len(input))
	for idx := range input {
		cloned[idx] = input[idx].Clone()
	}

	return cloned
}
