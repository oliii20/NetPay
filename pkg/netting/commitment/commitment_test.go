package commitment_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/commitment"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
)

func TestBuildRootsUsesSeparateDomainsAndCanonicalKeyOrder(t *testing.T) {
	t.Parallel()

	forward, err := commitment.BuildRoots(
		[]merkle.Leaf{{Key: []byte("2"), Payload: []byte("cut-2")}, {Key: []byte("1"), Payload: []byte("cut-1")}},
		[]merkle.Leaf{{Key: []byte("b"), Payload: []byte("result-b")}, {Key: []byte("a"), Payload: []byte("result-a")}},
		[]merkle.Leaf{
			{Key: []byte("2"), Payload: []byte("settlement-2")},
			{Key: []byte("1"), Payload: []byte("settlement-1")},
		},
	)
	require.NoError(t, err)

	reversed, err := commitment.BuildRoots(
		[]merkle.Leaf{{Key: []byte("1"), Payload: []byte("cut-1")}, {Key: []byte("2"), Payload: []byte("cut-2")}},
		[]merkle.Leaf{{Key: []byte("a"), Payload: []byte("result-a")}, {Key: []byte("b"), Payload: []byte("result-b")}},
		[]merkle.Leaf{
			{Key: []byte("1"), Payload: []byte("settlement-1")},
			{Key: []byte("2"), Payload: []byte("settlement-2")},
		},
	)
	require.NoError(t, err)
	require.Equal(t, forward, reversed)
	require.NotEqual(t, forward.CutRoot, forward.IntentResultRoot)
	require.NotEqual(t, forward.IntentResultRoot, forward.ShardSettlementRoot)
}

func TestMatchRootAndBatchIDGoldenVector(t *testing.T) {
	t.Parallel()

	roots := commitment.Roots{
		CutRoot:             repeatedHash(0x11),
		IntentResultRoot:    repeatedHash(0x22),
		ShardSettlementRoot: repeatedHash(0x33),
	}
	matchRoot := commitment.MatchRoot(roots)
	require.Equal(t, "a87a57f3a0a349f32faafae311f383af99db4f89a3023a56d83a1291e2bfc9bd", matchRoot.String())

	batchID := commitment.BatchID(repeatedHash(0x44), 7, matchRoot)
	require.Equal(t, "39872fff4fdc41b6446429c5869a45d1dcf1e11d684d6d9b12d2dd1b53c8a80c", batchID.String())
}

func TestBuildRootsReportsWhichTreeHasDuplicateKeys(t *testing.T) {
	t.Parallel()

	duplicate := []merkle.Leaf{
		{Key: []byte("same"), Payload: []byte("first")},
		{Key: []byte("same"), Payload: []byte("second")},
	}
	tests := []struct {
		name             string
		cuts             []merkle.Leaf
		intentResults    []merkle.Leaf
		shardSettlements []merkle.Leaf
		wantContext      string
	}{
		{name: "cuts", cuts: duplicate, wantContext: "build cut tree"},
		{name: "intent results", intentResults: duplicate, wantContext: "build intent-result tree"},
		{name: "shard settlements", shardSettlements: duplicate, wantContext: "build shard-settlement tree"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := commitment.BuildRoots(tc.cuts, tc.intentResults, tc.shardSettlements)
			require.True(t, errors.Is(err, merkle.ErrDuplicateKey), "got error %v", err)
			require.ErrorContains(t, err, tc.wantContext)
		})
	}
}

func repeatedHash(value byte) merkle.Hash {
	var hash merkle.Hash
	for i := range hash {
		hash[i] = value
	}
	return hash
}
