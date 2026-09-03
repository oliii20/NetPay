package window

import (
	"fmt"
	"math"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
)

type StreamState struct {
	NextExpectedHeight uint64
	ContinuousHeight   uint64
	ContinuousHash     merkle.Hash
	BufferedBlocks     map[uint64]model.FinalizedBlockReceipt
	UnassignedBlocks   map[uint64]model.FinalizedBlockReceipt
	SeenHashes         map[uint64]merkle.Hash
}

type State struct {
	NextWindowID uint64
	OpenedAt     *time.Time
	Pending      map[intent.ID]intent.PaymentIntent
	Assigned     map[intent.ID]uint64
	LastAssigned map[int64]uint64
	Streams      map[int64]StreamState
	Frozen       []FrozenWindow
}

func Restore(cfg Config, clock Clock, state State) (*Manager, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if clock == nil {
		clock = systemClock{}
	}
	if err := validateState(cfg, state); err != nil {
		return nil, err
	}

	cloned := cloneState(state)
	streams := make(map[int64]*StreamState, len(cloned.Streams))
	for shardID, stream := range cloned.Streams {
		streamCopy := stream
		streams[shardID] = &streamCopy
	}

	return &Manager{
		cfg:          cfg,
		clock:        clock,
		nextWindowID: cloned.NextWindowID,
		openedAt:     cloned.OpenedAt,
		pending:      cloned.Pending,
		assigned:     cloned.Assigned,
		lastAssigned: cloned.LastAssigned,
		streams:      streams,
		frozen:       cloned.Frozen,
	}, nil
}

func (m *Manager) Snapshot() State {
	streams := make(map[int64]StreamState, len(m.streams))
	for shardID, stream := range m.streams {
		streams[shardID] = cloneStreamState(*stream)
	}

	return cloneState(State{
		NextWindowID: m.nextWindowID,
		OpenedAt:     m.openedAt,
		Pending:      m.pending,
		Assigned:     m.assigned,
		LastAssigned: m.lastAssigned,
		Streams:      streams,
		Frozen:       m.frozen,
	})
}

func validateState(cfg Config, state State) error {
	if state.NextWindowID == 0 {
		return fmt.Errorf("%w: next window ID must be positive", ErrInvalidState)
	}
	if len(state.Streams) != int(cfg.ShardCount) || len(state.LastAssigned) != int(cfg.ShardCount) {
		return fmt.Errorf("%w: shard state count mismatch", ErrInvalidState)
	}
	if (len(state.Pending) == 0) != (state.OpenedAt == nil) {
		return fmt.Errorf("%w: pending intents and opened time disagree", ErrInvalidState)
	}

	blockIntentIDs := make(map[intent.ID]struct{}, len(state.Pending))
	for shardID := range cfg.ShardCount {
		stream, exists := state.Streams[shardID]
		if !exists {
			return fmt.Errorf("%w: missing stream for shard %d", ErrInvalidState, shardID)
		}
		lastAssigned, exists := state.LastAssigned[shardID]
		if !exists {
			return fmt.Errorf("%w: missing last assigned height for shard %d", ErrInvalidState, shardID)
		}
		if err := validateStreamState(shardID, lastAssigned, stream, blockIntentIDs); err != nil {
			return err
		}
	}
	if err := validatePendingState(state, blockIntentIDs); err != nil {
		return err
	}
	if err := validateAssignedState(state); err != nil {
		return err
	}
	if err := validateFrozenQueue(cfg, state); err != nil {
		return err
	}

	return nil
}

func validateStreamState(
	shardID int64,
	lastAssigned uint64,
	stream StreamState,
	blockIntentIDs map[intent.ID]struct{},
) error {
	if stream.ContinuousHeight == math.MaxUint64 || stream.NextExpectedHeight != stream.ContinuousHeight+1 {
		return fmt.Errorf("%w: invalid stream for shard %d", ErrInvalidState, shardID)
	}
	if lastAssigned > stream.ContinuousHeight {
		return fmt.Errorf("%w: invalid last assigned height for shard %d", ErrInvalidState, shardID)
	}
	continuousHash, exists := stream.SeenHashes[stream.ContinuousHeight]
	if !exists || continuousHash != stream.ContinuousHash {
		return fmt.Errorf("%w: continuous hash mismatch for shard %d", ErrInvalidState, shardID)
	}

	parentHash, exists := stream.SeenHashes[lastAssigned]
	if !exists {
		return fmt.Errorf("%w: missing assigned hash for shard %d height %d", ErrInvalidState, shardID, lastAssigned)
	}
	for height := lastAssigned + 1; height <= stream.ContinuousHeight; height++ {
		receipt, exists := stream.UnassignedBlocks[height]
		if !exists {
			return fmt.Errorf("%w: missing unassigned shard %d height %d", ErrInvalidState, shardID, height)
		}
		if receipt.ShardID != shardID || receipt.Height != height || receipt.ParentHash != parentHash {
			return fmt.Errorf("%w: malformed unassigned shard %d height %d", ErrInvalidState, shardID, height)
		}
		seenHash, seen := stream.SeenHashes[height]
		if !seen || seenHash != receipt.BlockHash {
			return fmt.Errorf("%w: unassigned hash mismatch for shard %d height %d", ErrInvalidState, shardID, height)
		}
		if err := collectReceiptIntentIDs(receipt, blockIntentIDs); err != nil {
			return err
		}
		parentHash = receipt.BlockHash
	}
	for height, receipt := range stream.UnassignedBlocks {
		outsideCut := height <= lastAssigned || height > stream.ContinuousHeight
		wrongReceipt := receipt.ShardID != shardID || receipt.Height != height
		if outsideCut || wrongReceipt {
			return fmt.Errorf("%w: unexpected unassigned shard %d height %d", ErrInvalidState, shardID, height)
		}
	}
	for height, receipt := range stream.BufferedBlocks {
		if height <= stream.NextExpectedHeight || receipt.ShardID != shardID || receipt.Height != height {
			return fmt.Errorf("%w: invalid buffered shard %d height %d", ErrInvalidState, shardID, height)
		}
		seenHash, seen := stream.SeenHashes[height]
		if !seen || seenHash != receipt.BlockHash {
			return fmt.Errorf("%w: buffered hash mismatch for shard %d height %d", ErrInvalidState, shardID, height)
		}
	}

	return nil
}

func collectReceiptIntentIDs(
	receipt model.FinalizedBlockReceipt,
	blockIntentIDs map[intent.ID]struct{},
) error {
	for _, payment := range receipt.Intents {
		id, err := payment.ID()
		if err != nil {
			return fmt.Errorf("%w: malformed unassigned intent: %w", ErrInvalidState, err)
		}
		if _, exists := blockIntentIDs[id]; exists {
			return fmt.Errorf("%w: duplicate unassigned intent %x", ErrInvalidState, id)
		}
		blockIntentIDs[id] = struct{}{}
	}

	return nil
}

func validatePendingState(state State, blockIntentIDs map[intent.ID]struct{}) error {
	if len(blockIntentIDs) != len(state.Pending) {
		return fmt.Errorf("%w: pending intent count mismatch", ErrInvalidState)
	}
	for id, payment := range state.Pending {
		calculatedID, err := payment.ID()
		if err != nil || calculatedID != id {
			return fmt.Errorf("%w: malformed pending intent %x", ErrInvalidState, id)
		}
		if _, exists := blockIntentIDs[id]; !exists {
			return fmt.Errorf("%w: pending intent has no unassigned block %x", ErrInvalidState, id)
		}
		if _, exists := state.Assigned[id]; exists {
			return fmt.Errorf("%w: pending intent already assigned %x", ErrInvalidState, id)
		}
	}

	return nil
}

func validateAssignedState(state State) error {
	for id, windowID := range state.Assigned {
		if windowID == 0 || windowID >= state.NextWindowID {
			return fmt.Errorf("%w: invalid assigned window %d for intent %x", ErrInvalidState, windowID, id)
		}
	}

	return nil
}

func validateFrozenQueue(cfg Config, state State) error {
	var previousWindowID uint64
	for idx, frozen := range state.Frozen {
		if frozen.WindowID == 0 || frozen.WindowID >= state.NextWindowID {
			return fmt.Errorf("%w: invalid frozen window ID %d", ErrInvalidState, frozen.WindowID)
		}
		if idx > 0 && frozen.WindowID <= previousWindowID {
			return fmt.Errorf("%w: frozen windows are not ordered", ErrInvalidState)
		}
		if len(frozen.Cuts) != int(cfg.ShardCount) || frozen.SealedAt.Before(frozen.OpenedAt) {
			return fmt.Errorf("%w: malformed frozen window %d", ErrInvalidState, frozen.WindowID)
		}
		previousWindowID = frozen.WindowID
	}

	return nil
}

func cloneState(state State) State {
	cloned := State{
		NextWindowID: state.NextWindowID,
		Pending:      make(map[intent.ID]intent.PaymentIntent, len(state.Pending)),
		Assigned:     make(map[intent.ID]uint64, len(state.Assigned)),
		LastAssigned: make(map[int64]uint64, len(state.LastAssigned)),
		Streams:      make(map[int64]StreamState, len(state.Streams)),
		Frozen:       make([]FrozenWindow, len(state.Frozen)),
	}
	if state.OpenedAt != nil {
		openedAt := *state.OpenedAt
		cloned.OpenedAt = &openedAt
	}
	for id, payment := range state.Pending {
		cloned.Pending[id] = model.CloneIntent(payment)
	}
	for id, windowID := range state.Assigned {
		cloned.Assigned[id] = windowID
	}
	for shardID, height := range state.LastAssigned {
		cloned.LastAssigned[shardID] = height
	}
	for shardID, stream := range state.Streams {
		cloned.Streams[shardID] = cloneStreamState(stream)
	}
	for idx, frozen := range state.Frozen {
		cloned.Frozen[idx] = cloneFrozenWindow(frozen)
	}

	return cloned
}

func cloneStreamState(stream StreamState) StreamState {
	cloned := StreamState{
		NextExpectedHeight: stream.NextExpectedHeight,
		ContinuousHeight:   stream.ContinuousHeight,
		ContinuousHash:     stream.ContinuousHash,
		BufferedBlocks:     make(map[uint64]model.FinalizedBlockReceipt, len(stream.BufferedBlocks)),
		UnassignedBlocks:   make(map[uint64]model.FinalizedBlockReceipt, len(stream.UnassignedBlocks)),
		SeenHashes:         make(map[uint64]merkle.Hash, len(stream.SeenHashes)),
	}
	for height, receipt := range stream.BufferedBlocks {
		cloned.BufferedBlocks[height] = receipt.Clone()
	}
	for height, receipt := range stream.UnassignedBlocks {
		cloned.UnassignedBlocks[height] = receipt.Clone()
	}
	for height, hash := range stream.SeenHashes {
		cloned.SeenHashes[height] = hash
	}

	return cloned
}

func cloneFrozenWindow(window FrozenWindow) FrozenWindow {
	cloned := window
	cloned.Cuts = append([]model.ShardCut(nil), window.Cuts...)
	cloned.Receipts = make([]model.FinalizedBlockReceipt, len(window.Receipts))
	for idx, receipt := range window.Receipts {
		cloned.Receipts[idx] = receipt.Clone()
	}
	cloned.Intents = make([]intent.PaymentIntent, len(window.Intents))
	for idx, payment := range window.Intents {
		cloned.Intents[idx] = model.CloneIntent(payment)
	}

	return cloned
}
