// Package settlement validates and executes Beacon-confirmed shard chunks.
package settlement

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"reflect"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/holiman/uint256"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/commitment"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/registry"
)

const (
	executedSlotDomain      = "settlement:executed"
	balanceChangeSettlement = byte(27)
)

var (
	ErrUnknownMatchRoot       = errors.New("settlement MatchRoot is not confirmed")
	ErrInvalidProof           = errors.New("invalid settlement proof")
	ErrWrongShard             = errors.New("settlement belongs to another shard")
	ErrSettlementExecuted     = errors.New("settlement chunk already executed")
	ErrSettlementConservation = errors.New("settlement chunk does not conserve matched amount")
	ErrDuplicateIntent        = errors.New("duplicate intent in settlement chunk")
)

type balanceKey struct {
	Counterparty int64
	AssetID      intent.AssetID
}

var settlementInitialBalance, _ = uint256.FromDecimal(account.NormalInitBalanceStr)

type Executor struct{}

func (Executor) Execute(
	stateDB *state.StateDB,
	shardID int64,
	pack model.SettlementPackage,
	confirmed model.MatchRootBlockBody,
) error {
	if !reflect.DeepEqual(pack.Header, confirmed) {
		return ErrUnknownMatchRoot
	}
	settlement := pack.Settlement
	if settlement.ShardID != shardID || pack.ShardCommitment.ShardID != shardID {
		return ErrWrongShard
	}
	if settlement.BatchID != confirmed.BatchID || settlement.WindowID != confirmed.WindowID ||
		settlement.ChunkCount != pack.ShardCommitment.ChunkCount {
		return ErrInvalidProof
	}
	if IsExecuted(stateDB, settlement.BatchID, shardID, settlement.ChunkIndex) {
		return nil
	}
	if err := verifyProofs(pack); err != nil {
		return err
	}
	registryState := registry.New(stateDB)
	if err := validateInstructions(registryState, shardID, settlement); err != nil {
		return err
	}

	totalIncoming := new(big.Int)
	for _, result := range settlement.Incoming {
		totalIncoming.Add(totalIncoming, result.MatchedAmount)
	}
	amount, overflow := uint256.FromBig(totalIncoming)
	if overflow || !core.CanTransfer(stateDB, common.Address(registry.EscrowAccountAddress), amount) {
		return registry.ErrInsufficientBalance
	}

	snapshot := stateDB.Snapshot()
	for _, result := range settlement.Outgoing {
		if err := registryState.Consume(result, settlement.BatchID); err != nil {
			stateDB.RevertToSnapshot(snapshot)
			return fmt.Errorf("consume outgoing reservation: %w", err)
		}
	}
	for _, result := range settlement.Incoming {
		matched, overflow := uint256.FromBig(result.MatchedAmount)
		if overflow {
			stateDB.RevertToSnapshot(snapshot)
			return ErrSettlementConservation
		}
		recipient := common.Address(result.Intent.Recipient)
		if !stateDB.Exist(recipient) {
			stateDB.SetBalance(
				recipient,
				settlementInitialBalance,
				tracing.BalanceChangeReason(balanceChangeSettlement),
			)
		}
		stateDB.SubBalance(
			common.Address(registry.EscrowAccountAddress),
			matched,
			tracing.BalanceChangeReason(balanceChangeSettlement),
		)
		stateDB.AddBalance(
			recipient,
			matched,
			tracing.BalanceChangeReason(balanceChangeSettlement),
		)
	}
	markExecuted(stateDB, settlement.BatchID, shardID, settlement.ChunkIndex)

	return nil
}

func IsExecuted(stateDB *state.StateDB, batchID merkle.Hash, shardID int64, chunkIndex uint32) bool {
	value := stateDB.GetState(
		common.Address(registry.IntentRegistryAddress),
		executedSlot(batchID, shardID, chunkIndex),
	)

	return value != (common.Hash{})
}

func verifyProofs(pack model.SettlementPackage) error {
	chunkPayload, err := batch.SettlementPayload(pack.Settlement)
	if err != nil {
		return fmt.Errorf("encode settlement payload: %w", err)
	}
	if !merkle.Verify(batch.ChunkLeafDomain, chunkPayload, pack.ChunkProof, pack.ShardCommitment.ShardRoot) {
		return ErrInvalidProof
	}
	shardPayload := batch.ShardCommitmentPayload(pack.ShardCommitment)
	if !merkle.Verify(
		commitment.ShardSettlementLeafDomain,
		shardPayload,
		pack.ShardProof,
		pack.Header.ShardSettlementRoot,
	) {
		return ErrInvalidProof
	}
	roots := commitment.Roots{
		CutRoot: pack.Header.CutRoot, IntentResultRoot: pack.Header.IntentResultRoot,
		ShardSettlementRoot: pack.Header.ShardSettlementRoot,
	}
	if commitment.MatchRoot(roots) != pack.Header.MatchRoot ||
		commitment.BatchID(pack.Header.PreviousBatchID, pack.Header.WindowID, pack.Header.MatchRoot) != pack.Header.BatchID {
		return ErrInvalidProof
	}

	return nil
}

func validateInstructions(registryState *registry.Registry, shardID int64, settlement model.ShardSettlement) error {
	outgoing := make(map[balanceKey]*big.Int)
	incoming := make(map[balanceKey]*big.Int)
	seen := make(map[intent.ID]struct{}, len(settlement.Outgoing)+len(settlement.Incoming))
	for _, result := range settlement.Outgoing {
		if err := result.Validate(); err != nil {
			return err
		}
		if result.Intent.SourceShard != shardID {
			return ErrWrongShard
		}
		if _, exists := seen[result.IntentID]; exists {
			return ErrDuplicateIntent
		}
		seen[result.IntentID] = struct{}{}
		reservation, err := registryState.Get(result.Intent)
		if err != nil {
			return err
		}
		if reservation.Status != registry.ReservationReserved || reservation.Amount.Cmp(result.Intent.Amount) != 0 {
			return registry.ErrInvalidStatus
		}
		addAmount(outgoing, balanceKey{
			Counterparty: result.Intent.DestinationShard, AssetID: result.Intent.AssetID,
		}, result.MatchedAmount)
	}
	for _, result := range settlement.Incoming {
		if err := result.Validate(); err != nil {
			return err
		}
		if result.Intent.DestinationShard != shardID || result.MatchedAmount.Sign() <= 0 {
			return ErrWrongShard
		}
		if _, exists := seen[result.IntentID]; exists {
			return ErrDuplicateIntent
		}
		seen[result.IntentID] = struct{}{}
		addAmount(incoming, balanceKey{
			Counterparty: result.Intent.SourceShard, AssetID: result.Intent.AssetID,
		}, result.MatchedAmount)
	}
	if !equalBalances(outgoing, incoming) {
		return ErrSettlementConservation
	}

	return nil
}

func addAmount(amounts map[balanceKey]*big.Int, key balanceKey, amount *big.Int) {
	if amounts[key] == nil {
		amounts[key] = new(big.Int)
	}
	amounts[key].Add(amounts[key], amount)
}

func equalBalances(left, right map[balanceKey]*big.Int) bool {
	for key, amount := range left {
		other := right[key]
		if other == nil || amount.Cmp(other) != 0 {
			return false
		}
	}
	for key, amount := range right {
		other := left[key]
		if other == nil || amount.Cmp(other) != 0 {
			return false
		}
	}

	return true
}

func markExecuted(stateDB *state.StateDB, batchID merkle.Hash, shardID int64, chunkIndex uint32) {
	address := common.Address(registry.IntentRegistryAddress)
	if stateDB.GetNonce(address) == 0 {
		stateDB.SetNonce(address, 1, tracing.NonceChangeUnspecified)
	}
	stateDB.SetState(address, executedSlot(batchID, shardID, chunkIndex), common.BigToHash(big.NewInt(1)))
}

func executedSlot(batchID merkle.Hash, shardID int64, chunkIndex uint32) common.Hash {
	var shard [8]byte
	var chunk [4]byte
	binary.BigEndian.PutUint64(shard[:], uint64(shardID))
	binary.BigEndian.PutUint32(chunk[:], chunkIndex)
	hasher := sha256.New()
	hasher.Write([]byte(executedSlotDomain))
	hasher.Write(batchID[:])
	hasher.Write(shard[:])
	hasher.Write(chunk[:])

	return common.BytesToHash(hasher.Sum(nil))
}
