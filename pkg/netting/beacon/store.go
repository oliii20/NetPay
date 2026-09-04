// Package beacon validates and persists MatchRoot blocks and their sidecars.
package beacon

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"go.etcd.io/bbolt"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
)

var (
	ErrBatchNotFound    = errors.New("Beacon batch not found")
	ErrBatchCommitted   = errors.New("Beacon batch already committed")
	ErrBeaconTipChanged = errors.New("Beacon chain tip changed")
)

var (
	blockBucket    = []byte("match_root_blocks")
	sidecarBucket  = []byte("batch_sidecars")
	consumedBucket = []byte("consumed_intents")
	metaBucket     = []byte("beacon_meta")
	tipKey         = []byte("tip")
)

type MatchRootBlock struct {
	Number     uint64
	ParentHash merkle.Hash
	Body       model.MatchRootBlockBody
	CommitTime time.Time
}

func (b MatchRootBlock) Hash() (merkle.Hash, error) {
	encoded, err := encode(b)
	if err != nil {
		return merkle.Hash{}, fmt.Errorf("encode MatchRoot block: %w", err)
	}

	return merkle.Hash(sha256.Sum256(encoded)), nil
}

type Store struct {
	db *bbolt.DB
}

func OpenStore(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("Beacon store path must not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create Beacon store directory: %w", err)
	}
	db, err := bbolt.Open(path, 0o600, nil)
	if err != nil {
		return nil, fmt.Errorf("open Beacon store: %w", err)
	}
	if err = db.Update(func(tx *bbolt.Tx) error {
		for _, name := range [][]byte{blockBucket, sidecarBucket, consumedBucket, metaBucket} {
			if _, createErr := tx.CreateBucketIfNotExists(name); createErr != nil {
				return fmt.Errorf("create Beacon bucket %s: %w", name, createErr)
			}
		}
		return nil
	}); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

func (s *Store) Tip() (MatchRootBlock, bool, error) {
	var encoded []byte
	if err := s.db.View(func(tx *bbolt.Tx) error {
		encoded = bytes.Clone(tx.Bucket(metaBucket).Get(tipKey))
		return nil
	}); err != nil {
		return MatchRootBlock{}, false, fmt.Errorf("read Beacon tip: %w", err)
	}
	if len(encoded) == 0 {
		return MatchRootBlock{}, false, nil
	}
	var block MatchRootBlock
	if err := decode(encoded, &block); err != nil {
		return MatchRootBlock{}, false, fmt.Errorf("decode Beacon tip: %w", err)
	}

	return block, true, nil
}

func (s *Store) HasIntent(id intent.ID) (bool, error) {
	var exists bool
	if err := s.db.View(func(tx *bbolt.Tx) error {
		exists = tx.Bucket(consumedBucket).Get(id[:]) != nil
		return nil
	}); err != nil {
		return false, fmt.Errorf("read consumed intent: %w", err)
	}

	return exists, nil
}

func (s *Store) Commit(proposal model.BatchProposal, now time.Time) (MatchRootBlock, error) {
	var committed MatchRootBlock
	err := s.db.Update(func(tx *bbolt.Tx) error {
		if tx.Bucket(blockBucket).Get(proposal.Header.BatchID[:]) != nil {
			return fmt.Errorf("%w: %s", ErrBatchCommitted, proposal.Header.BatchID.String())
		}
		var tip MatchRootBlock
		tipBytes := tx.Bucket(metaBucket).Get(tipKey)
		if tipBytes != nil {
			if err := decode(tipBytes, &tip); err != nil {
				return fmt.Errorf("decode Beacon tip: %w", err)
			}
			if proposal.Header.PreviousBatchID != tip.Body.BatchID || proposal.Header.WindowID != tip.Body.WindowID+1 {
				return ErrBeaconTipChanged
			}
		} else if proposal.Header.PreviousBatchID != (merkle.Hash{}) || proposal.Header.WindowID != 1 {
			return ErrBeaconTipChanged
		}

		parentHash := merkle.Hash{}
		if tipBytes != nil {
			var err error
			parentHash, err = tip.Hash()
			if err != nil {
				return err
			}
		}
		committed = MatchRootBlock{
			Number: tip.Number + 1, ParentHash: parentHash, Body: proposal.Header, CommitTime: now,
		}
		blockBytes, err := encode(committed)
		if err != nil {
			return fmt.Errorf("encode committed MatchRoot block: %w", err)
		}
		sidecarBytes, err := encode(proposal.Sidecar)
		if err != nil {
			return fmt.Errorf("encode committed sidecar: %w", err)
		}
		if err = tx.Bucket(blockBucket).Put(proposal.Header.BatchID[:], blockBytes); err != nil {
			return fmt.Errorf("save MatchRoot block: %w", err)
		}
		if err = tx.Bucket(sidecarBucket).Put(proposal.Header.BatchID[:], sidecarBytes); err != nil {
			return fmt.Errorf("save batch sidecar: %w", err)
		}
		for _, result := range proposal.Sidecar.IntentResults {
			if err = tx.Bucket(consumedBucket).Put(result.IntentID[:], proposal.Header.BatchID[:]); err != nil {
				return fmt.Errorf("save consumed intent: %w", err)
			}
		}
		if err = tx.Bucket(metaBucket).Put(tipKey, blockBytes); err != nil {
			return fmt.Errorf("save Beacon tip: %w", err)
		}

		return nil
	})
	if err != nil {
		return MatchRootBlock{}, err
	}

	return committed, nil
}

func (s *Store) GetProposal(id merkle.Hash) (model.BatchProposal, error) {
	var blockBytes, sidecarBytes []byte
	if err := s.db.View(func(tx *bbolt.Tx) error {
		blockBytes = bytes.Clone(tx.Bucket(blockBucket).Get(id[:]))
		sidecarBytes = bytes.Clone(tx.Bucket(sidecarBucket).Get(id[:]))
		return nil
	}); err != nil {
		return model.BatchProposal{}, fmt.Errorf("read Beacon batch: %w", err)
	}
	if len(blockBytes) == 0 || len(sidecarBytes) == 0 {
		return model.BatchProposal{}, fmt.Errorf("%w: %s", ErrBatchNotFound, id.String())
	}
	var block MatchRootBlock
	var sidecar model.BatchSidecar
	if err := decode(blockBytes, &block); err != nil {
		return model.BatchProposal{}, fmt.Errorf("decode MatchRoot block: %w", err)
	}
	if err := decode(sidecarBytes, &sidecar); err != nil {
		return model.BatchProposal{}, fmt.Errorf("decode batch sidecar: %w", err)
	}

	return model.BatchProposal{Header: block.Body, Sidecar: sidecar}.Clone(), nil
}

func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close Beacon store: %w", err)
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
