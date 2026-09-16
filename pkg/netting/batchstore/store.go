// Package batchstore persists Solver recovery state and complete validation
// sidecars. The store uses one Bolt transaction for each state transition.
package batchstore

import (
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"go.etcd.io/bbolt"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/window"
)

var ErrProposalNotFound = errors.New("batch proposal not found")

var (
	stateBucket    = []byte("solver_state")
	proposalBucket = []byte("batch_proposals")
	assignedBucket = []byte("assigned_intents")
	stateKey       = []byte("current")
)

type SolverState struct {
	ManagerState      *window.State
	BootstrapReceipts map[int64][]model.FinalizedBlockReceipt
	ChainTip          merkle.Hash
	PendingBatchIDs   []merkle.Hash
}

type Store struct {
	db *bbolt.DB
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("batch store path must not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create batch store directory: %w", err)
	}
	db, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		return nil, fmt.Errorf("open batch store: %w", err)
	}
	if err = db.Update(func(tx *bbolt.Tx) error {
		if _, createErr := tx.CreateBucketIfNotExists(stateBucket); createErr != nil {
			return fmt.Errorf("create solver state bucket: %w", createErr)
		}
		if _, createErr := tx.CreateBucketIfNotExists(proposalBucket); createErr != nil {
			return fmt.Errorf("create proposal bucket: %w", createErr)
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

func (s *Store) SaveState(state SolverState) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		return writeState(tx, state, false)
	})
}

// SaveCheckpoint merges newly assigned intents with the durable history.
func (s *Store) SaveCheckpoint(state SolverState) error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		return writeState(tx, state, true)
	})
}

func (s *Store) PutProposalAndState(proposal model.BatchProposal, state SolverState) error {
	return s.putProposal(proposal, state, false)
}

func (s *Store) PutProposalAndCheckpoint(proposal model.BatchProposal, state SolverState) error {
	return s.putProposal(proposal, state, true)
}

func (s *Store) putProposal(proposal model.BatchProposal, state SolverState, incremental bool) error {
	proposalBytes, err := encode(proposal)
	if err != nil {
		return fmt.Errorf("encode batch proposal: %w", err)
	}
	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := tx.Bucket(proposalBucket).Put(proposal.Header.BatchID[:], proposalBytes); err != nil {
			return fmt.Errorf("save batch proposal: %w", err)
		}
		return writeState(tx, state, incremental)
	})
}

func writeState(tx *bbolt.Tx, state SolverState, incremental bool) error {
	if !incremental && tx.Bucket(assignedBucket) != nil {
		if err := tx.DeleteBucket(assignedBucket); err != nil {
			return fmt.Errorf("replace assigned intents: %w", err)
		}
	}
	assignments := tx.Bucket(assignedBucket)
	if assignments == nil {
		var err error
		assignments, err = tx.CreateBucket(assignedBucket)
		if err != nil {
			return fmt.Errorf("create assigned intents: %w", err)
		}
		// Old databases kept the complete history inside the gob snapshot.
		if incremental {
			if previous := tx.Bucket(stateBucket).Get(stateKey); previous != nil {
				var legacy SolverState
				if err = decode(previous, &legacy); err != nil {
					return fmt.Errorf("decode legacy solver state: %w", err)
				}
				if legacy.ManagerState != nil {
					if err = writeAssignments(assignments, legacy.ManagerState.Assigned); err != nil {
						return err
					}
				}
			}
		}
	}
	if state.ManagerState != nil {
		if err := writeAssignments(assignments, state.ManagerState.Assigned); err != nil {
			return err
		}
		managerState := *state.ManagerState
		managerState.Assigned = nil
		state.ManagerState = &managerState
	}
	encoded, err := encode(state)
	if err != nil {
		return fmt.Errorf("encode solver state: %w", err)
	}
	if err = tx.Bucket(stateBucket).Put(stateKey, encoded); err != nil {
		return fmt.Errorf("save solver state: %w", err)
	}
	return nil
}

func writeAssignments(bucket *bbolt.Bucket, assigned map[intent.ID]uint64) error {
	ids := make([]intent.ID, 0, len(assigned))
	for id := range assigned {
		ids = append(ids, id)
	}
	// Ordered inserts avoid repeatedly shifting large Bolt leaf nodes on migration.
	sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })
	for _, id := range ids {
		windowID := assigned[id]
		value := binary.BigEndian.AppendUint64(nil, windowID)
		if err := bucket.Put(id[:], value); err != nil {
			return fmt.Errorf("save assigned intent: %w", err)
		}
	}
	return nil
}

func (s *Store) LoadState() (SolverState, bool, error) {
	var state SolverState
	var found bool
	if err := s.db.View(func(tx *bbolt.Tx) error {
		encoded := tx.Bucket(stateBucket).Get(stateKey)
		if len(encoded) == 0 {
			return nil
		}
		found = true
		if err := decode(encoded, &state); err != nil {
			return fmt.Errorf("decode solver state: %w", err)
		}
		if bucket := tx.Bucket(assignedBucket); bucket != nil && state.ManagerState != nil {
			if state.ManagerState.Assigned == nil {
				state.ManagerState.Assigned = make(map[intent.ID]uint64)
			}
			return bucket.ForEach(func(key, value []byte) error {
				var id intent.ID
				if len(key) != len(id) || len(value) != 8 {
					return fmt.Errorf("invalid assigned intent record")
				}
				copy(id[:], key)
				state.ManagerState.Assigned[id] = binary.BigEndian.Uint64(value)
				return nil
			})
		}
		return nil
	}); err != nil {
		return SolverState{}, false, fmt.Errorf("load solver state: %w", err)
	}
	return state, found, nil
}

func (s *Store) GetProposal(id merkle.Hash) (model.BatchProposal, error) {
	var encoded []byte
	if err := s.db.View(func(tx *bbolt.Tx) error {
		encoded = bytes.Clone(tx.Bucket(proposalBucket).Get(id[:]))
		return nil
	}); err != nil {
		return model.BatchProposal{}, fmt.Errorf("read batch proposal: %w", err)
	}
	if len(encoded) == 0 {
		return model.BatchProposal{}, fmt.Errorf("%w: %s", ErrProposalNotFound, id.String())
	}
	var proposal model.BatchProposal
	if err := decode(encoded, &proposal); err != nil {
		return model.BatchProposal{}, fmt.Errorf("decode batch proposal: %w", err)
	}

	return proposal.Clone(), nil
}

func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close batch store: %w", err)
	}
	return nil
}

func encode(value any) ([]byte, error) {
	var out bytes.Buffer
	if err := gob.NewEncoder(&out).Encode(value); err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}

func decode(encoded []byte, target any) error {
	return gob.NewDecoder(bytes.NewReader(encoded)).Decode(target)
}
