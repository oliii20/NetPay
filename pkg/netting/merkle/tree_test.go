package merkle_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
)

func TestBuildSortsLeavesAndVerifiesEveryProof(t *testing.T) {
	t.Parallel()

	left, err := merkle.Build("test-domain", []merkle.Leaf{
		{Key: []byte("c"), Payload: []byte("charlie")},
		{Key: []byte("a"), Payload: []byte("alpha")},
		{Key: []byte("b"), Payload: []byte("bravo")},
	})
	require.NoError(t, err)
	require.Equal(t, 3, left.Len())

	right, err := merkle.Build("test-domain", []merkle.Leaf{
		{Key: []byte("b"), Payload: []byte("bravo")},
		{Key: []byte("c"), Payload: []byte("charlie")},
		{Key: []byte("a"), Payload: []byte("alpha")},
	})
	require.NoError(t, err)
	require.Equal(t, left.Root(), right.Root())

	for _, leaf := range []merkle.Leaf{
		{Key: []byte("a"), Payload: []byte("alpha")},
		{Key: []byte("b"), Payload: []byte("bravo")},
		{Key: []byte("c"), Payload: []byte("charlie")},
	} {
		proof, proofErr := left.Proof(leaf.Key)
		require.NoError(t, proofErr)
		require.True(t, merkle.Verify("test-domain", leaf.Payload, proof, left.Root()))
	}
}

func TestVerifyRejectsTamperingAndWrongDomain(t *testing.T) {
	t.Parallel()

	tree, err := merkle.Build("domain-a", []merkle.Leaf{
		{Key: []byte("a"), Payload: []byte("alpha")},
		{Key: []byte("b"), Payload: []byte("bravo")},
	})
	require.NoError(t, err)

	proof, err := tree.Proof([]byte("a"))
	require.NoError(t, err)
	require.False(t, merkle.Verify("domain-a", []byte("tampered"), proof, tree.Root()))
	require.False(t, merkle.Verify("domain-b", []byte("alpha"), proof, tree.Root()))

	proof.Steps[0].Sibling[0] ^= 0xff
	require.False(t, merkle.Verify("domain-a", []byte("alpha"), proof, tree.Root()))
}

func TestBuildRejectsDuplicateKeys(t *testing.T) {
	t.Parallel()

	_, err := merkle.Build("domain", []merkle.Leaf{
		{Key: []byte("same"), Payload: []byte("first")},
		{Key: []byte("same"), Payload: []byte("second")},
	})
	require.ErrorIs(t, err, merkle.ErrDuplicateKey)
}

func TestBuildRejectsEmptyDomain(t *testing.T) {
	t.Parallel()

	_, err := merkle.Build("", nil)
	require.ErrorIs(t, err, merkle.ErrEmptyDomain)
}

func TestProofRejectsUnknownKey(t *testing.T) {
	t.Parallel()

	tree, err := merkle.Build("domain", []merkle.Leaf{{Key: []byte("a"), Payload: []byte("alpha")}})
	require.NoError(t, err)

	_, err = tree.Proof([]byte("missing"))
	require.ErrorIs(t, err, merkle.ErrLeafNotFound)
}

func TestEmptyRootAndThreeLeafGoldenVector(t *testing.T) {
	t.Parallel()

	empty, err := merkle.Build("ignored-for-empty-tree", nil)
	require.NoError(t, err)
	require.Zero(t, empty.Len())
	require.Equal(t, "53b5ca3f802083b374b1cf85c77461515c579d79bd3ac1bb8a8701d5d63207eb", empty.Root().String())

	tree, err := merkle.Build("test-domain", []merkle.Leaf{
		{Key: []byte("a"), Payload: []byte("alpha")},
		{Key: []byte("b"), Payload: []byte("bravo")},
		{Key: []byte("c"), Payload: []byte("charlie")},
	})
	require.NoError(t, err)
	require.Equal(t, "9643c0ace39e13b3455751a154e037eca3886aad8fbda754598720467ab498f1", tree.Root().String())
}
