package fallback

import (
	"errors"
	"math/big"
	"reflect"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/holiman/uint256"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
)

const executedDomain = "fallback:executed"

var (
	ErrFallbackWrongShard = errors.New("reserved fallback belongs to another destination shard")
	ErrFallbackProof      = errors.New("invalid reserved fallback proof")
)

var fallbackInitialBalance, _ = uint256.FromDecimal(account.NormalInitBalanceStr)

func ExecuteCredit(
	stateDB *state.StateDB,
	shardID int64,
	item model.ReservedFallback,
	confirmed model.MatchRootBlockBody,
) error {
	if item.DestinationShard != shardID {
		return ErrFallbackWrongShard
	}
	if !reflect.DeepEqual(item.Proof.Header, confirmed) || item.BatchID != confirmed.BatchID {
		return ErrFallbackProof
	}
	if err := batch.VerifySettlementPackage(item.Proof); err != nil {
		return ErrFallbackProof
	}
	var matched bool
	for _, result := range item.Proof.Settlement.Outgoing {
		if result.IntentID != item.IntentID {
			continue
		}
		if result.FallbackAmount.Cmp(item.Amount) != 0 || result.Intent.Sender != item.Sender ||
			result.Intent.Recipient != item.Recipient || result.Intent.SourceShard != item.SourceShard ||
			result.Intent.DestinationShard != item.DestinationShard {
			return ErrFallbackProof
		}
		matched = true
		break
	}
	if !matched || item.Amount == nil || item.Amount.Sign() <= 0 {
		return ErrFallbackProof
	}
	key := recordKey(item.Key())
	if stateDB.GetState(storageAddress(), executedSlot(key)) != (common.Hash{}) {
		return nil
	}
	amount, overflow := uint256.FromBig(item.Amount)
	if overflow {
		return ErrInvalidFallback
	}
	recipient := common.Address(item.Recipient)
	if !stateDB.Exist(recipient) {
		stateDB.SetBalance(recipient, fallbackInitialBalance, tracing.BalanceChangeReason(transferReason))
	}
	stateDB.AddBalance(recipient, amount, tracing.BalanceChangeReason(transferReason))
	if stateDB.GetNonce(storageAddress()) == 0 {
		stateDB.SetNonce(storageAddress(), 1, tracing.NonceChangeUnspecified)
	}
	stateDB.SetState(storageAddress(), executedSlot(key), common.BigToHash(big.NewInt(1)))

	return nil
}

func IsCreditExecuted(stateDB *state.StateDB, key model.FallbackKey) bool {
	return stateDB.GetState(storageAddress(), executedSlot(recordKey(key))) != (common.Hash{})
}

func executedSlot(key common.Hash) common.Hash {
	return recordSlot(executedDomain, key)
}
