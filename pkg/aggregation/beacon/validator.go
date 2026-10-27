package beacon

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
)

var (
	ErrBatchSequence   = errors.New("invalid Beacon aggregation batch sequence")
	ErrReceiptCoverage = errors.New("finalized receipts do not cover vector cut")
	ErrPaymentConsumed = errors.New("payment already consumed by an earlier batch")
	ErrInvalidPayment  = errors.New("invalid finalized cross-shard payment")
)

type Validator struct {
	mux          sync.Mutex
	cached       bool
	cachedDigest [32]byte
	cachedTip    merkle.Hash
	cachedHasTip bool
	store        *Store
	chainID      uint64
	shardCount   int64
}

func NewValidator(store *Store, chainID uint64, shardCount int64) *Validator {
	return &Validator{store: store, chainID: chainID, shardCount: shardCount}
}

// Validate caches only successful checks of the entire encoded proposal against
// an unchanged committed tip. Header BatchID alone cannot authenticate a sidecar.
func (v *Validator) Validate(proposal model.AggregationBatch) error {
	v.mux.Lock()
	defer v.mux.Unlock()

	hasher := sha256.New()
	if err := gob.NewEncoder(hasher).Encode(proposal); err != nil {
		return fmt.Errorf("hash validation proposal: %w", err)
	}

	var digest [32]byte
	copy(digest[:], hasher.Sum(nil))

	return v.store.validationSnapshot(
		func(tip AggregationBlock, hasTip bool, hasPayment func(paymentmodel.ID) bool) error {
			if v.cached && digest == v.cachedDigest && hasTip == v.cachedHasTip && tip.Body.BatchID == v.cachedTip {
				return nil
			}

			if err := validateSequence(proposal.Header, tip, hasTip); err != nil {
				return err
			}

			payments, err := v.validateReceiptCoverage(proposal, tip, hasTip, hasPayment)
			if err != nil {
				return err
			}

			if len(payments) == 0 {
				return batch.ErrEmptyWindow
			}

			if err = batch.VerifyBatch(proposal); err != nil {
				return fmt.Errorf("verify aggregation batch: %w", err)
			}

			v.cached, v.cachedDigest, v.cachedTip, v.cachedHasTip = true, digest, tip.Body.BatchID, hasTip

			return nil
		},
	)
}

func validateSequence(header model.AggregationBatchHeader, tip AggregationBlock, hasTip bool) error {
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

func (v *Validator) validateReceiptCoverage(
	proposal model.AggregationBatch,
	tip AggregationBlock,
	hasTip bool,
	hasPayment func(paymentmodel.ID) bool,
) ([]paymentmodel.CrossShardPayment, error) {
	previousCuts := make(map[int64]model.ShardCut)

	if hasTip {
		for _, cut := range tip.Body.Cuts {
			previousCuts[cut.ShardID] = cut
		}
	}

	receiptsByShard := make(map[int64][]model.FinalizedBlockReceipt)
	for _, receipt := range proposal.Sidecar.Receipts {
		receiptsByShard[receipt.ShardID] = append(receiptsByShard[receipt.ShardID], receipt.Clone())
	}

	payments := make([]paymentmodel.CrossShardPayment, 0)
	seen := make(map[paymentmodel.ID]struct{})

	for _, cut := range proposal.Header.Cuts {
		if hasTip {
			previous, exists := previousCuts[cut.ShardID]
			if !exists || cut.PreviousHeight != previous.EndHeight {
				return nil, ErrReceiptCoverage
			}
		}

		receipts := receiptsByShard[cut.ShardID]
		sort.Slice(receipts, func(i, j int) bool { return receipts[i].Height < receipts[j].Height })

		if uint64(len(receipts)) != cut.EndHeight-cut.PreviousHeight {
			return nil, fmt.Errorf("%w: shard %d", ErrReceiptCoverage, cut.ShardID)
		}

		parent := merkle.Hash{}
		if previous, exists := previousCuts[cut.ShardID]; exists {
			parent = previous.EndBlockHash
		} else if len(receipts) > 0 {
			parent = receipts[0].ParentHash
		}

		for idx, receipt := range receipts {
			expectedHeight := cut.PreviousHeight + uint64(idx) + 1
			if receipt.Height != expectedHeight || receipt.ParentHash != parent {
				return nil, ErrReceiptCoverage
			}

			parent = receipt.BlockHash
			for _, payment := range receipt.Payments {
				if payment.SourceShard != receipt.ShardID {
					return nil, ErrInvalidPayment
				}

				currentEpoch := uint64(0)
				if receipt.Epoch >= 0 {
					currentEpoch = uint64(receipt.Epoch)
				}

				validationCtx := paymentmodel.ValidationContext{
					ChainID: v.chainID, ShardID: receipt.ShardID,
					ShardCount: v.shardCount, CurrentEpoch: currentEpoch,
				}

				var validationErr error
				if payment.SourceAggregated {
					validationErr = payment.ValidateSourceAbstract(validationCtx)
				} else {
					validationErr = payment.Validate(validationCtx)
				}

				if validationErr != nil {
					return nil, fmt.Errorf("%w: %w", ErrInvalidPayment, validationErr)
				}

				ids, err := payment.LogicalIDs()
				if err != nil {
					return nil, err
				}

				for _, id := range ids {
					if _, duplicate := seen[id]; duplicate {
						return nil, fmt.Errorf("%w: %x", batch.ErrDuplicatePayment, id)
					}

					if hasPayment(id) {
						return nil, fmt.Errorf("%w: %x", ErrPaymentConsumed, id)
					}

					seen[id] = struct{}{}
				}

				payments = append(payments, payment.Clone())
			}
		}

		if len(receipts) > 0 && !bytes.Equal(parent[:], cut.EndBlockHash[:]) {
			return nil, ErrReceiptCoverage
		}

		delete(receiptsByShard, cut.ShardID)
	}

	if len(receiptsByShard) != 0 {
		return nil, ErrReceiptCoverage
	}

	return payments, nil
}
