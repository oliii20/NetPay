// Package beacon validates and persists aggregation batches and sidecars.
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

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
)

var (
	ErrBatchNotFound    = errors.New("beacon aggregation batch not found")
	ErrBatchCommitted   = errors.New("beacon aggregation batch already committed")
	ErrBeaconTipChanged = errors.New("beacon chain tip changed")
)

var (
	blockBucket    = []byte("aggregation_blocks")
	sidecarBucket  = []byte("aggregation_sidecars")
	consumedBucket = []byte("consumed_payments")
	metaBucket     = []byte("beacon_meta")
	tipKey         = []byte("tip")
)

type AggregationBlock struct {
	Number     uint64
	ParentHash merkle.Hash
	Body       model.AggregationBatchHeader
	CommitTime time.Time
}

func (b AggregationBlock) Hash() (merkle.Hash, error) {
	encoded, err := encode(b)
	if err != nil {
		return merkle.Hash{}, fmt.Errorf("encode aggregation block: %w", err)
	}

	return merkle.Hash(sha256.Sum256(encoded)), nil
}

type Store struct{ db *bbolt.DB }

func OpenStore(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("beacon store path must not be empty")
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

func (s *Store) Tip() (AggregationBlock, bool, error) {
	var encoded []byte

	if err := s.db.View(func(tx *bbolt.Tx) error {
		encoded = bytes.Clone(tx.Bucket(metaBucket).Get(tipKey))
		return nil
	}); err != nil {
		return AggregationBlock{}, false, err
	}

	if len(encoded) == 0 {
		return AggregationBlock{}, false, nil
	}

	var block AggregationBlock
	if err := decode(encoded, &block); err != nil {
		return AggregationBlock{}, false, fmt.Errorf("decode Beacon tip: %w", err)
	}

	return block, true, nil
}

func (s *Store) HasPayment(id paymentmodel.ID) (bool, error) {
	var exists bool

	err := s.db.View(func(tx *bbolt.Tx) error {
		exists = tx.Bucket(consumedBucket).Get(id[:]) != nil
		return nil
	})

	return exists, err
}

// validationSnapshot keeps the tip and all replay lookups in one read transaction.
// The lookup callback is valid only for the duration of fn.
func (s *Store) validationSnapshot(fn func(AggregationBlock, bool, func(paymentmodel.ID) bool) error) error {
	return s.db.View(func(tx *bbolt.Tx) error {
		var tip AggregationBlock
		encoded := tx.Bucket(metaBucket).Get(tipKey)
		if len(encoded) > 0 {
			if err := decode(encoded, &tip); err != nil {
				return fmt.Errorf("decode validation tip: %w", err)
			}
		}
		consumed := tx.Bucket(consumedBucket)
		return fn(tip, len(encoded) > 0, func(id paymentmodel.ID) bool {
			return consumed.Get(id[:]) != nil
		})
	})
}

func (s *Store) Commit(proposal model.AggregationBatch, now time.Time) (AggregationBlock, error) {
	var committed AggregationBlock

	err := s.db.Update(func(tx *bbolt.Tx) error {
		header := proposal.Header
		if tx.Bucket(blockBucket).Get(header.BatchID[:]) != nil {
			return fmt.Errorf("%w: %s", ErrBatchCommitted, header.BatchID.String())
		}

		var tip AggregationBlock

		tipBytes := tx.Bucket(metaBucket).Get(tipKey)
		if tipBytes != nil {
			if err := decode(tipBytes, &tip); err != nil {
				return err
			}

			if header.PreviousBatchID != tip.Body.BatchID || header.WindowID != tip.Body.WindowID+1 {
				return ErrBeaconTipChanged
			}
		} else if header.PreviousBatchID != (merkle.Hash{}) || header.WindowID != 1 {
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

		committed = AggregationBlock{Number: tip.Number + 1, ParentHash: parentHash, Body: header, CommitTime: now}

		blockBytes, err := encode(committed)
		if err != nil {
			return err
		}

		sidecarBytes, err := encode(proposal.Sidecar)
		if err != nil {
			return err
		}

		if err = tx.Bucket(blockBucket).Put(header.BatchID[:], blockBytes); err != nil {
			return err
		}

		if err = tx.Bucket(sidecarBucket).Put(header.BatchID[:], sidecarBytes); err != nil {
			return err
		}

		for _, receipt := range proposal.Sidecar.Receipts {
			for _, payment := range receipt.Payments {
				ids, idErr := payment.LogicalIDs()
				if idErr != nil {
					return idErr
				}

				for _, id := range ids {
					if err = tx.Bucket(consumedBucket).Put(id[:], header.BatchID[:]); err != nil {
						return err
					}
				}
			}
		}

		return tx.Bucket(metaBucket).Put(tipKey, blockBytes)
	})
	if err != nil {
		return AggregationBlock{}, err
	}

	return committed, nil
}

func (s *Store) GetBatch(id merkle.Hash) (model.AggregationBatch, error) {
	var blockBytes, sidecarBytes []byte

	if err := s.db.View(func(tx *bbolt.Tx) error {
		blockBytes = bytes.Clone(tx.Bucket(blockBucket).Get(id[:]))
		sidecarBytes = bytes.Clone(tx.Bucket(sidecarBucket).Get(id[:]))
		return nil
	}); err != nil {
		return model.AggregationBatch{}, err
	}

	if len(blockBytes) == 0 || len(sidecarBytes) == 0 {
		return model.AggregationBatch{}, fmt.Errorf("%w: %s", ErrBatchNotFound, id.String())
	}

	var (
		block   AggregationBlock
		sidecar model.AggregationSidecar
	)

	if err := decode(blockBytes, &block); err != nil {
		return model.AggregationBatch{}, err
	}

	if err := decode(sidecarBytes, &sidecar); err != nil {
		return model.AggregationBatch{}, err
	}

	return model.AggregationBatch{Header: block.Body, Sidecar: sidecar}.Clone(), nil
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
