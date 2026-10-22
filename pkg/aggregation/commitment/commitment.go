// Package commitment constructs the roots finalized by the aggregation Beacon chain.
package commitment

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
)

const (
	CutLeafDomain              = "BLOCKEMULATOR_AGGREGATION_CUT_V1"
	ReceiptLeafDomain          = "BLOCKEMULATOR_AGGREGATION_RECEIPT_V1"
	ShardAggregationLeafDomain = "BLOCKEMULATOR_SHARD_AGGREGATION_V1"
	aggregationRootDomain      = "BLOCKEMULATOR_AGGREGATION_ROOT_V1"
	batchIDDomain              = "BLOCKEMULATOR_AGGREGATION_BATCH_ID_V1"
)

type Roots struct {
	CutRoot              merkle.Hash
	ReceiptRoot          merkle.Hash
	ShardAggregationRoot merkle.Hash
}

type Trees struct {
	Cuts              *merkle.Tree
	Receipts          *merkle.Tree
	ShardAggregations *merkle.Tree
}

func BuildTrees(cuts, receipts, shardAggregations []merkle.Leaf) (Trees, error) {
	cutTree, err := merkle.Build(CutLeafDomain, cuts)
	if err != nil {
		return Trees{}, fmt.Errorf("build cut tree: %w", err)
	}

	receiptTree, err := merkle.Build(ReceiptLeafDomain, receipts)
	if err != nil {
		return Trees{}, fmt.Errorf("build receipt tree: %w", err)
	}

	aggregationTree, err := merkle.Build(ShardAggregationLeafDomain, shardAggregations)
	if err != nil {
		return Trees{}, fmt.Errorf("build shard aggregation tree: %w", err)
	}

	return Trees{Cuts: cutTree, Receipts: receiptTree, ShardAggregations: aggregationTree}, nil
}

func BuildRoots(cuts, receipts, shardAggregations []merkle.Leaf) (Roots, error) {
	trees, err := BuildTrees(cuts, receipts, shardAggregations)
	if err != nil {
		return Roots{}, err
	}

	return trees.Roots(), nil
}

func (t Trees) Roots() Roots {
	return Roots{
		CutRoot:              t.Cuts.Root(),
		ReceiptRoot:          t.Receipts.Root(),
		ShardAggregationRoot: t.ShardAggregations.Root(),
	}
}

func AggregationRoot(roots Roots) merkle.Hash {
	hasher := sha256.New()
	hasher.Write([]byte(aggregationRootDomain))
	hasher.Write(roots.CutRoot[:])
	hasher.Write(roots.ReceiptRoot[:])
	hasher.Write(roots.ShardAggregationRoot[:])

	return sumHash(hasher.Sum(nil))
}

func BatchID(previousBatchID merkle.Hash, windowID uint64, root merkle.Hash) merkle.Hash {
	var encodedWindowID [8]byte
	binary.BigEndian.PutUint64(encodedWindowID[:], windowID)

	hasher := sha256.New()
	hasher.Write([]byte(batchIDDomain))
	hasher.Write(previousBatchID[:])
	hasher.Write(encodedWindowID[:])
	hasher.Write(root[:])

	return sumHash(hasher.Sum(nil))
}

func sumHash(sum []byte) merkle.Hash {
	var hash merkle.Hash
	copy(hash[:], sum)

	return hash
}
