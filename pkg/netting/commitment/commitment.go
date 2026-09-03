// Package commitment constructs the roots committed by the netting Beacon chain.
package commitment

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
)

const (
	CutLeafDomain             = "BLOCKEMULATOR_SHARD_CUT_V1"
	IntentResultLeafDomain    = "BLOCKEMULATOR_INTENT_RESULT_V1"
	ShardSettlementLeafDomain = "BLOCKEMULATOR_SHARD_SETTLEMENT_V1"
	matchRootDomain           = "BLOCKEMULATOR_MATCH_ROOT_V1"
	batchIDDomain             = "BLOCKEMULATOR_BATCH_ID_V1"
)

type Roots struct {
	CutRoot             merkle.Hash
	IntentResultRoot    merkle.Hash
	ShardSettlementRoot merkle.Hash
}

type Trees struct {
	Cuts             *merkle.Tree
	IntentResults    *merkle.Tree
	ShardSettlements *merkle.Tree
}

func BuildTrees(cuts, intentResults, shardSettlements []merkle.Leaf) (Trees, error) {
	cutTree, err := merkle.Build(CutLeafDomain, cuts)
	if err != nil {
		return Trees{}, fmt.Errorf("build cut tree: %w", err)
	}

	intentResultTree, err := merkle.Build(IntentResultLeafDomain, intentResults)
	if err != nil {
		return Trees{}, fmt.Errorf("build intent-result tree: %w", err)
	}

	shardSettlementTree, err := merkle.Build(ShardSettlementLeafDomain, shardSettlements)
	if err != nil {
		return Trees{}, fmt.Errorf("build shard-settlement tree: %w", err)
	}

	return Trees{
		Cuts:             cutTree,
		IntentResults:    intentResultTree,
		ShardSettlements: shardSettlementTree,
	}, nil
}

func BuildRoots(cuts, intentResults, shardSettlements []merkle.Leaf) (Roots, error) {
	trees, err := BuildTrees(cuts, intentResults, shardSettlements)
	if err != nil {
		return Roots{}, err
	}

	return trees.Roots(), nil
}

func (t Trees) Roots() Roots {
	return Roots{
		CutRoot:             t.Cuts.Root(),
		IntentResultRoot:    t.IntentResults.Root(),
		ShardSettlementRoot: t.ShardSettlements.Root(),
	}
}

func MatchRoot(roots Roots) merkle.Hash {
	hasher := sha256.New()
	hasher.Write([]byte(matchRootDomain))
	hasher.Write(roots.CutRoot[:])
	hasher.Write(roots.IntentResultRoot[:])
	hasher.Write(roots.ShardSettlementRoot[:])

	return sumHash(hasher.Sum(nil))
}

func BatchID(previousBatchID merkle.Hash, windowID uint64, matchRoot merkle.Hash) merkle.Hash {
	var encodedWindowID [8]byte
	binary.BigEndian.PutUint64(encodedWindowID[:], windowID)

	hasher := sha256.New()
	hasher.Write([]byte(batchIDDomain))
	hasher.Write(previousBatchID[:])
	hasher.Write(encodedWindowID[:])
	hasher.Write(matchRoot[:])

	return sumHash(hasher.Sum(nil))
}

func sumHash(sum []byte) merkle.Hash {
	var hash merkle.Hash
	copy(hash[:], sum)

	return hash
}
