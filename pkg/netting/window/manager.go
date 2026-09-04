// Package window groups asynchronously finalized shard blocks into immutable Vector-Cut windows.
package window

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
)

var (
	ErrInvalidConfig      = errors.New("invalid window configuration")
	ErrMissingCheckpoint  = errors.New("missing shard checkpoint")
	ErrUnknownShard       = errors.New("unknown shard")
	ErrConflictingReceipt = errors.New("conflicting finalized block receipt")
	ErrStaleReceipt       = errors.New("stale finalized block receipt")
	ErrParentHashMismatch = errors.New("receipt parent hash mismatch")
	ErrInvalidReceipt     = errors.New("invalid finalized block receipt")
	ErrDuplicateIntent    = errors.New("duplicate intent in window stream")
	ErrFrozenQueueEmpty   = errors.New("frozen window queue is empty")
	ErrUnexpectedWindow   = errors.New("unexpected frozen window ID")
	ErrInvalidState       = errors.New("invalid restored window state")
)

type Clock interface {
	Now() time.Time
}

type Config struct {
	ShardCount        int64
	BatchSize         int
	MaxWindowDuration time.Duration
}

type Watermark struct {
	Height    uint64
	BlockHash merkle.Hash
}

type FrozenWindow struct {
	WindowID    uint64
	OpenedAt    time.Time
	SealedAt    time.Time
	CloseReason string
	Cuts        []model.ShardCut
	Receipts    []model.FinalizedBlockReceipt
	Intents     []intent.PaymentIntent
}

type Manager struct {
	cfg          Config
	clock        Clock
	nextWindowID uint64
	openedAt     *time.Time
	pending      map[intent.ID]intent.PaymentIntent
	assigned     map[intent.ID]uint64
	lastAssigned map[int64]uint64
	streams      map[int64]*StreamState
	frozen       []FrozenWindow
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}

func New(cfg Config, clock Clock, checkpoints map[int64]model.ShardCheckpoint) (*Manager, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if len(checkpoints) != int(cfg.ShardCount) {
		return nil, fmt.Errorf("%w: got %d checkpoints, want %d", ErrMissingCheckpoint, len(checkpoints), cfg.ShardCount)
	}
	if clock == nil {
		clock = systemClock{}
	}

	manager := &Manager{
		cfg:          cfg,
		clock:        clock,
		nextWindowID: 1,
		pending:      make(map[intent.ID]intent.PaymentIntent),
		assigned:     make(map[intent.ID]uint64),
		lastAssigned: make(map[int64]uint64, cfg.ShardCount),
		streams:      make(map[int64]*StreamState, cfg.ShardCount),
	}
	for shardID := range cfg.ShardCount {
		checkpoint, exists := checkpoints[shardID]
		if !exists || checkpoint.Height == math.MaxUint64 {
			return nil, fmt.Errorf("%w: shard %d", ErrMissingCheckpoint, shardID)
		}

		manager.lastAssigned[shardID] = checkpoint.Height
		manager.streams[shardID] = &StreamState{
			NextExpectedHeight: checkpoint.Height + 1,
			ContinuousHeight:   checkpoint.Height,
			ContinuousHash:     checkpoint.BlockHash,
			BufferedBlocks:     make(map[uint64]model.FinalizedBlockReceipt),
			UnassignedBlocks:   make(map[uint64]model.FinalizedBlockReceipt),
			SeenHashes:         map[uint64]merkle.Hash{checkpoint.Height: checkpoint.BlockHash},
		}
	}

	return manager, nil
}

func (m *Manager) AddReceipt(receipt model.FinalizedBlockReceipt) (*FrozenWindow, error) {
	stream, exists := m.streams[receipt.ShardID]
	if !exists {
		return nil, fmt.Errorf("%w: %d", ErrUnknownShard, receipt.ShardID)
	}
	if receipt.Height == 0 {
		return nil, fmt.Errorf("%w: height must be positive", ErrInvalidReceipt)
	}

	if seenHash, seen := stream.SeenHashes[receipt.Height]; seen {
		if seenHash != receipt.BlockHash {
			return nil, fmt.Errorf("%w: shard %d height %d", ErrConflictingReceipt, receipt.ShardID, receipt.Height)
		}
		return m.TryClose(), nil
	}
	if receipt.Height < stream.NextExpectedHeight {
		return nil, fmt.Errorf("%w: shard %d height %d", ErrStaleReceipt, receipt.ShardID, receipt.Height)
	}

	stream.BufferedBlocks[receipt.Height] = receipt.Clone()
	stream.SeenHashes[receipt.Height] = receipt.BlockHash
	firstSealed, err := m.advanceStream(receipt.ShardID, stream)
	if err != nil {
		return firstSealed, err
	}
	lastSealed := m.TryClose()
	if firstSealed != nil {
		return firstSealed, nil
	}

	return lastSealed, nil
}

func (m *Manager) TryClose() *FrozenWindow {
	if len(m.pending) == 0 || m.openedAt == nil {
		return nil
	}

	sizeReady := len(m.pending) >= m.cfg.BatchSize
	timeReady := !m.clock.Now().Before(m.openedAt.Add(m.cfg.MaxWindowDuration))
	if !sizeReady && !timeReady {
		return nil
	}

	reason := "max_window_duration"
	if sizeReady {
		reason = "batch_size"
	}
	window := m.seal(reason)
	cloned := cloneFrozenWindow(window)

	return &cloned
}

func (m *Manager) PendingIntentCount() int {
	return len(m.pending)
}

func (m *Manager) FrozenWindowCount() int {
	return len(m.frozen)
}

func (m *Manager) Watermark(shardID int64) (Watermark, error) {
	stream, exists := m.streams[shardID]
	if !exists {
		return Watermark{}, fmt.Errorf("%w: %d", ErrUnknownShard, shardID)
	}

	return Watermark{Height: stream.ContinuousHeight, BlockHash: stream.ContinuousHash}, nil
}

func (m *Manager) PeekFrozen() (*FrozenWindow, bool) {
	if len(m.frozen) == 0 {
		return nil, false
	}

	cloned := cloneFrozenWindow(m.frozen[0])

	return &cloned, true
}

func (m *Manager) AcknowledgeFrozen(windowID uint64) error {
	if len(m.frozen) == 0 {
		return ErrFrozenQueueEmpty
	}
	if m.frozen[0].WindowID != windowID {
		return fmt.Errorf("%w: got %d, want %d", ErrUnexpectedWindow, windowID, m.frozen[0].WindowID)
	}

	m.frozen = m.frozen[1:]

	return nil
}

func (m *Manager) advanceStream(shardID int64, stream *StreamState) (*FrozenWindow, error) {
	var firstSealed *FrozenWindow
	for {
		receipt, exists := stream.BufferedBlocks[stream.NextExpectedHeight]
		if !exists {
			return firstSealed, nil
		}
		if receipt.ParentHash != stream.ContinuousHash {
			m.dropBuffered(stream, receipt.Height)
			return firstSealed, fmt.Errorf(
				"%w: shard %d height %d",
				ErrParentHashMismatch,
				shardID,
				receipt.Height,
			)
		}

		intentIDs, err := m.validateReceiptIntents(receipt)
		if err != nil {
			m.dropBuffered(stream, receipt.Height)
			return firstSealed, err
		}

		delete(stream.BufferedBlocks, receipt.Height)
		stream.UnassignedBlocks[receipt.Height] = receipt.Clone()
		stream.ContinuousHeight = receipt.Height
		stream.ContinuousHash = receipt.BlockHash
		stream.NextExpectedHeight++

		for idx, id := range intentIDs {
			m.pending[id] = model.CloneIntent(receipt.Intents[idx])
		}
		if len(intentIDs) > 0 && m.openedAt == nil {
			now := m.clock.Now()
			m.openedAt = &now
		}
		if sealed := m.TryClose(); sealed != nil && firstSealed == nil {
			firstSealed = sealed
		}
	}
}

func (m *Manager) validateReceiptIntents(receipt model.FinalizedBlockReceipt) ([]intent.ID, error) {
	ids := make([]intent.ID, len(receipt.Intents))
	local := make(map[intent.ID]struct{}, len(receipt.Intents))
	for idx, payment := range receipt.Intents {
		if payment.SourceShard != receipt.ShardID {
			return nil, fmt.Errorf(
				"%w: intent source %d, receipt shard %d",
				ErrInvalidReceipt,
				payment.SourceShard,
				receipt.ShardID,
			)
		}

		id, err := payment.ID()
		if err != nil {
			return nil, fmt.Errorf("%w: calculate intent ID: %w", ErrInvalidReceipt, err)
		}
		if _, exists := local[id]; exists {
			return nil, fmt.Errorf("%w: %x", ErrDuplicateIntent, id)
		}
		if _, exists := m.pending[id]; exists {
			return nil, fmt.Errorf("%w: %x", ErrDuplicateIntent, id)
		}
		if _, exists := m.assigned[id]; exists {
			return nil, fmt.Errorf("%w: %x", ErrDuplicateIntent, id)
		}

		local[id] = struct{}{}
		ids[idx] = id
	}

	return ids, nil
}

func (m *Manager) dropBuffered(stream *StreamState, height uint64) {
	delete(stream.BufferedBlocks, height)
	delete(stream.SeenHashes, height)
}

func (m *Manager) seal(closeReason string) FrozenWindow {
	now := m.clock.Now()
	window := FrozenWindow{
		WindowID:    m.nextWindowID,
		OpenedAt:    *m.openedAt,
		SealedAt:    now,
		CloseReason: closeReason,
		Cuts:        make([]model.ShardCut, 0, m.cfg.ShardCount),
		Receipts:    make([]model.FinalizedBlockReceipt, 0),
		Intents:     sortedPendingIntents(m.pending),
	}

	for shardID := range m.cfg.ShardCount {
		stream := m.streams[shardID]
		previousHeight := m.lastAssigned[shardID]
		window.Cuts = append(window.Cuts, model.ShardCut{
			ShardID:        shardID,
			PreviousHeight: previousHeight,
			EndHeight:      stream.ContinuousHeight,
			EndBlockHash:   stream.ContinuousHash,
		})

		for height, receipt := range stream.UnassignedBlocks {
			if height > previousHeight && height <= stream.ContinuousHeight {
				window.Receipts = append(window.Receipts, receipt.Clone())
				delete(stream.UnassignedBlocks, height)
			}
		}
		m.lastAssigned[shardID] = stream.ContinuousHeight
	}
	sort.Slice(window.Receipts, func(i, j int) bool {
		if window.Receipts[i].ShardID != window.Receipts[j].ShardID {
			return window.Receipts[i].ShardID < window.Receipts[j].ShardID
		}
		return window.Receipts[i].Height < window.Receipts[j].Height
	})

	for id := range m.pending {
		m.assigned[id] = window.WindowID
	}
	m.pending = make(map[intent.ID]intent.PaymentIntent)
	m.openedAt = nil
	m.nextWindowID++
	m.frozen = append(m.frozen, cloneFrozenWindow(window))

	return window
}

func sortedPendingIntents(pending map[intent.ID]intent.PaymentIntent) []intent.PaymentIntent {
	ids := make([]intent.ID, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })

	payments := make([]intent.PaymentIntent, 0, len(ids))
	for _, id := range ids {
		payments = append(payments, model.CloneIntent(pending[id]))
	}

	return payments
}

func validateConfig(cfg Config) error {
	if cfg.ShardCount <= 0 {
		return fmt.Errorf("%w: shard count must be positive", ErrInvalidConfig)
	}
	if cfg.BatchSize <= 0 {
		return fmt.Errorf("%w: batch size must be positive", ErrInvalidConfig)
	}
	if cfg.MaxWindowDuration <= 0 {
		return fmt.Errorf("%w: max window duration must be positive", ErrInvalidConfig)
	}

	return nil
}
