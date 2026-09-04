// Package batchstore persists Solver recovery state and complete validation
// sidecars. The store uses one Bolt transaction for each state transition.
package batchstore

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.etcd.io/bbolt"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/window"
)

var ErrProposalNotFound = errors.New("batch proposal not found")

var (
	stateBucket    = []byte("solver_state")
	proposalBucket = []byte("batch_proposals")
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
	encoded, err := encode(state)
	if err != nil {
		return fmt.Errorf("encode solver state: %w", err)
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := tx.Bucket(stateBucket).Put(stateKey, encoded); err != nil {
			return fmt.Errorf("save solver state: %w", err)
		}
		return nil
	})
}

func (s *Store) PutProposalAndState(proposal model.BatchProposal, state SolverState) error {
	proposalBytes, err := encode(proposal)
	if err != nil {
		return fmt.Errorf("encode batch proposal: %w", err)
	}
	stateBytes, err := encode(state)
	if err != nil {
		return fmt.Errorf("encode solver state: %w", err)
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		if err := tx.Bucket(proposalBucket).Put(proposal.Header.BatchID[:], proposalBytes); err != nil {
			return fmt.Errorf("save batch proposal: %w", err)
		}
		if err := tx.Bucket(stateBucket).Put(stateKey, stateBytes); err != nil {
			return fmt.Errorf("save solver state: %w", err)
		}
		return nil
	})
}

func (s *Store) LoadState() (SolverState, bool, error) {
	var encoded []byte
	if err := s.db.View(func(tx *bbolt.Tx) error {
		encoded = bytes.Clone(tx.Bucket(stateBucket).Get(stateKey))
		return nil
	}); err != nil {
		return SolverState{}, false, fmt.Errorf("load solver state: %w", err)
	}
	if len(encoded) == 0 {
		return SolverState{}, false, nil
	}
	var state SolverState
	if err := decode(encoded, &state); err != nil {
		return SolverState{}, false, fmt.Errorf("decode solver state: %w", err)
	}

	return state, true, nil
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
