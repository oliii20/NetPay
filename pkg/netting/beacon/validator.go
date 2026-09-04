package beacon

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sort"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/batch"
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
	ErrDerivedBatchMismatch = errors.New("batch differs from deterministic derivation")
)

type StateReader interface {
	Tip() (MatchRootBlock, bool, error)
	HasIntent(intent.ID) (bool, error)
}

type Validator struct {
	state StateReader
}

func NewValidator(state StateReader) *Validator {
	return &Validator{state: state}
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

	expected, err := (batch.Builder{}).Build(window.FrozenWindow{
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
		!reflect.DeepEqual(expected.Sidecar.ShardSettlements, proposal.Sidecar.ShardSettlements) {
		return ErrDerivedBatchMismatch
	}

	return nil
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
