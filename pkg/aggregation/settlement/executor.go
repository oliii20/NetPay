// Package settlement validates and applies Beacon-finalized account credits.
package settlement

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/holiman/uint256"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/outbox"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
)

const (
	executedSlotDomain      = "aggregation:executed"
	balanceChangeSettlement = byte(27)
)

var (
	ErrUnknownAggregationRoot = errors.New("aggregation root is not confirmed")
	ErrInvalidProof           = errors.New("invalid aggregation proof")
	ErrWrongShard             = errors.New("aggregation belongs to another shard")
	ErrDuplicateCredit        = errors.New("duplicate account credit")
	ErrInvalidCredit          = errors.New("invalid account credit")
)

var settlementInitialBalance, _ = uint256.FromDecimal(account.NormalInitBalanceStr)

type Executor struct{}

func (Executor) Execute(
	stateDB *state.StateDB,
	shardID int64,
	pack model.AggregationPackage,
	confirmed model.AggregationBatchHeader,
) error {
	if pack.Header.BatchID != confirmed.BatchID {
		return ErrUnknownAggregationRoot
	}

	aggregation := pack.Aggregation
	if aggregation.ShardID != shardID || pack.ShardCommitment.ShardID != shardID {
		return ErrWrongShard
	}

	if IsExecuted(stateDB, aggregation.BatchID, shardID, aggregation.ChunkIndex) {
		return nil
	}

	if err := batch.VerifyAggregationPackage(pack, confirmed); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidProof, err)
	}

	if err := validateCredits(aggregation.Credits, aggregation.BatchOnly); err != nil {
		return err
	}

	snapshot := stateDB.Snapshot()

	for _, credit := range aggregation.Credits {
		amount, overflow := uint256.FromBig(credit.Amount)
		if overflow {
			stateDB.RevertToSnapshot(snapshot)
			return ErrInvalidCredit
		}

		recipient := common.Address(credit.Recipient)
		if !stateDB.Exist(recipient) {
			stateDB.SetBalance(
				recipient,
				settlementInitialBalance,
				tracing.BalanceChangeReason(balanceChangeSettlement),
			)
		}

		stateDB.AddBalance(recipient, amount, tracing.BalanceChangeReason(balanceChangeSettlement))
	}

	markExecuted(stateDB, aggregation.BatchID, shardID, aggregation.ChunkIndex)

	return nil
}

func validateCredits(credits []model.AggregatedCredit, batchOnly bool) error {
	seen := make(map[account.Address]struct{}, len(credits))
	for _, credit := range credits {
		if credit.Amount == nil || credit.Amount.Sign() <= 0 || credit.ReceiptCount == 0 ||
			credit.AssetID != paymentmodel.NativeAssetID {
			return ErrInvalidCredit
		}

		if _, exists := seen[credit.Recipient]; exists && !batchOnly {
			return ErrDuplicateCredit
		}

		seen[credit.Recipient] = struct{}{}
	}

	return nil
}

func IsExecuted(stateDB *state.StateDB, batchID merkle.Hash, shardID int64, chunkIndex uint32) bool {
	value := stateDB.GetState(common.Address(outbox.SystemAccountAddress), executedSlot(batchID, shardID, chunkIndex))
	return value != (common.Hash{})
}

func markExecuted(stateDB *state.StateDB, batchID merkle.Hash, shardID int64, chunkIndex uint32) {
	address := common.Address(outbox.SystemAccountAddress)
	if stateDB.GetNonce(address) == 0 {
		stateDB.SetNonce(address, 1, tracing.NonceChangeUnspecified)
	}

	stateDB.SetState(address, executedSlot(batchID, shardID, chunkIndex), common.BigToHash(big.NewInt(1)))
}

func executedSlot(batchID merkle.Hash, shardID int64, chunkIndex uint32) common.Hash {
	var (
		shard [8]byte
		chunk [4]byte
	)

	binary.BigEndian.PutUint64(shard[:], uint64(shardID))
	binary.BigEndian.PutUint32(chunk[:], chunkIndex)

	hasher := sha256.New()
	hasher.Write([]byte(executedSlotDomain))
	hasher.Write(batchID[:])
	hasher.Write(shard[:])
	hasher.Write(chunk[:])

	return common.BytesToHash(hasher.Sum(nil))
}
