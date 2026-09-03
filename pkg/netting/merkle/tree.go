// Package merkle implements the domain-separated Merkle tree used by netting batches.
package merkle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
)

const emptyRootDomain = "BLOCKEMULATOR_MERKLE_EMPTY_V1"

var (
	ErrEmptyDomain  = errors.New("Merkle domain must not be empty")
	ErrDuplicateKey = errors.New("duplicate Merkle leaf key")
	ErrLeafNotFound = errors.New("Merkle leaf not found")
)

type Hash [sha256.Size]byte

var emptyRoot = Hash(sha256.Sum256([]byte(emptyRootDomain)))

func (h Hash) String() string {
	return hex.EncodeToString(h[:])
}

type Leaf struct {
	// Key determines canonical ordering and must also be bound inside Payload.
	Key     []byte
	Payload []byte
}

type Step struct {
	Sibling       Hash
	SiblingOnLeft bool
}

type Proof struct {
	Steps []Step
}

type Tree struct {
	domain  string
	leaves  []Leaf
	indexes map[string]int
	levels  [][]Hash
	root    Hash
}

// Build clones and sorts leaves by Key before constructing the tree.
func Build(domain string, input []Leaf) (*Tree, error) {
	if domain == "" {
		return nil, ErrEmptyDomain
	}

	leaves := cloneLeaves(input)
	sort.Slice(leaves, func(i, j int) bool {
		return bytes.Compare(leaves[i].Key, leaves[j].Key) < 0
	})

	indexes := make(map[string]int, len(leaves))
	for idx, leaf := range leaves {
		key := string(leaf.Key)
		if _, exists := indexes[key]; exists {
			return nil, fmt.Errorf("%w: %x", ErrDuplicateKey, leaf.Key)
		}
		indexes[key] = idx
	}

	tree := &Tree{
		domain:  domain,
		leaves:  leaves,
		indexes: indexes,
		root:    emptyRoot,
	}
	if len(leaves) == 0 {
		return tree, nil
	}

	leafLevel := make([]Hash, len(leaves))
	for idx, leaf := range leaves {
		leafLevel[idx] = hashLeaf(domain, leaf.Payload)
	}
	tree.levels = append(tree.levels, leafLevel)

	for len(tree.levels[len(tree.levels)-1]) > 1 {
		current := tree.levels[len(tree.levels)-1]
		parent := make([]Hash, (len(current)+1)/2)
		for idx := 0; idx < len(current); idx += 2 {
			right := idx + 1
			if right == len(current) {
				right = idx
			}
			parent[idx/2] = hashNode(current[idx], current[right])
		}
		tree.levels = append(tree.levels, parent)
	}

	tree.root = tree.levels[len(tree.levels)-1][0]

	return tree, nil
}

func (t *Tree) Root() Hash {
	return t.root
}

func (t *Tree) Len() int {
	return len(t.leaves)
}

func (t *Tree) Proof(key []byte) (Proof, error) {
	index, exists := t.indexes[string(key)]
	if !exists {
		return Proof{}, fmt.Errorf("%w: %x", ErrLeafNotFound, key)
	}

	steps := make([]Step, 0, len(t.levels)-1)
	for levelIdx := 0; levelIdx < len(t.levels)-1; levelIdx++ {
		level := t.levels[levelIdx]
		siblingIndex := index ^ 1
		if siblingIndex >= len(level) {
			siblingIndex = index
		}

		steps = append(steps, Step{
			Sibling:       level[siblingIndex],
			SiblingOnLeft: siblingIndex < index,
		})
		index /= 2
	}

	return Proof{Steps: steps}, nil
}

func Verify(domain string, payload []byte, proof Proof, expectedRoot Hash) bool {
	current := hashLeaf(domain, payload)
	for _, step := range proof.Steps {
		if step.SiblingOnLeft {
			current = hashNode(step.Sibling, current)
		} else {
			current = hashNode(current, step.Sibling)
		}
	}

	return current == expectedRoot
}

func cloneLeaves(input []Leaf) []Leaf {
	leaves := make([]Leaf, len(input))
	for idx, leaf := range input {
		leaves[idx] = Leaf{
			Key:     bytes.Clone(leaf.Key),
			Payload: bytes.Clone(leaf.Payload),
		}
	}

	return leaves
}

func hashLeaf(domain string, payload []byte) Hash {
	hasher := sha256.New()
	hasher.Write([]byte{0x00})
	hasher.Write([]byte(domain))
	hasher.Write(payload)

	return hashFromSum(hasher.Sum(nil))
}

func hashNode(left, right Hash) Hash {
	hasher := sha256.New()
	hasher.Write([]byte{0x01})
	hasher.Write(left[:])
	hasher.Write(right[:])

	return hashFromSum(hasher.Sum(nil))
}

func hashFromSum(sum []byte) Hash {
	var hash Hash
	copy(hash[:], sum)

	return hash
}
