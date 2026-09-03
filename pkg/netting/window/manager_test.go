package window_test

import (
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/window"
)

func TestManagerAdvancesOnlyContinuousReceipts(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	manager := newManager(t, window.Config{ShardCount: 2, BatchSize: 10, MaxWindowDuration: time.Minute}, clock)

	block2Hash := testHash(0x12)
	block3 := testReceipt(0, 3, block2Hash, testHash(0x13), 2)
	sealed, err := manager.AddReceipt(block3)
	require.NoError(t, err)
	require.Nil(t, sealed)
	requireWatermark(t, manager, 0, 1, testHash(0x10))
	require.Zero(t, manager.PendingIntentCount())

	block2 := testReceipt(0, 2, testHash(0x10), block2Hash, 1)
	sealed, err = manager.AddReceipt(block2)
	require.NoError(t, err)
	require.Nil(t, sealed)
	requireWatermark(t, manager, 0, 3, testHash(0x13))
	require.Equal(t, 2, manager.PendingIntentCount())

	sealed, err = manager.AddReceipt(block2)
	require.NoError(t, err)
	require.Nil(t, sealed)
	require.Equal(t, 2, manager.PendingIntentCount())
}

func TestManagerClosesAtGlobalBatchSizeWithVectorCut(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	manager := newManager(t, window.Config{ShardCount: 3, BatchSize: 3, MaxWindowDuration: time.Minute}, clock)

	sealed, err := manager.AddReceipt(testReceipt(0, 2, testHash(0x10), testHash(0x11), 1, 2))
	require.NoError(t, err)
	require.Nil(t, sealed)

	sealed, err = manager.AddReceipt(testReceipt(1, 2, testHash(0x20), testHash(0x21), 3))
	require.NoError(t, err)
	require.NotNil(t, sealed)
	require.Equal(t, uint64(1), sealed.WindowID)
	require.Len(t, sealed.Intents, 3)
	require.Len(t, sealed.Receipts, 2)
	require.Equal(t, []model.ShardCut{
		{ShardID: 0, PreviousHeight: 1, EndHeight: 2, EndBlockHash: testHash(0x11)},
		{ShardID: 1, PreviousHeight: 1, EndHeight: 2, EndBlockHash: testHash(0x21)},
		{ShardID: 2, PreviousHeight: 1, EndHeight: 1, EndBlockHash: testHash(0x30)},
	}, sealed.Cuts)
	require.Zero(t, manager.PendingIntentCount())
}

func TestManagerDoesNotStartTimerForEmptyBlocks(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	manager := newManager(t, window.Config{ShardCount: 2, BatchSize: 10, MaxWindowDuration: 10 * time.Second}, clock)

	sealed, err := manager.AddReceipt(testReceipt(0, 2, testHash(0x10), testHash(0x11)))
	require.NoError(t, err)
	require.Nil(t, sealed)
	clock.Advance(time.Hour)
	require.Nil(t, manager.TryClose())

	sealed, err = manager.AddReceipt(testReceipt(0, 3, testHash(0x11), testHash(0x12), 1))
	require.NoError(t, err)
	require.Nil(t, sealed)
	clock.Advance(9 * time.Second)
	require.Nil(t, manager.TryClose())
	clock.Advance(time.Second)

	sealed = manager.TryClose()
	require.NotNil(t, sealed)
	require.Equal(t, uint64(3), sealed.Cuts[0].EndHeight)
	require.Len(t, sealed.Receipts, 2)
}

func TestBatchSizeIsThresholdNotStrictLimit(t *testing.T) {
	t.Parallel()

	manager := newManager(
		t,
		window.Config{ShardCount: 2, BatchSize: 2, MaxWindowDuration: time.Minute},
		newFakeClock(),
	)
	sealed, err := manager.AddReceipt(testReceipt(0, 2, testHash(0x10), testHash(0x11), 1, 2, 3))
	require.NoError(t, err)
	require.NotNil(t, sealed)
	require.Len(t, sealed.Intents, 3)
}

func TestBufferedCatchUpSealsAtEachBlockBoundary(t *testing.T) {
	t.Parallel()

	manager := newManager(
		t,
		window.Config{ShardCount: 2, BatchSize: 1, MaxWindowDuration: time.Minute},
		newFakeClock(),
	)
	block2Hash := testHash(0x11)
	block3Hash := testHash(0x12)
	_, err := manager.AddReceipt(testReceipt(0, 4, block3Hash, testHash(0x13), 3))
	require.NoError(t, err)
	_, err = manager.AddReceipt(testReceipt(0, 3, block2Hash, block3Hash, 2))
	require.NoError(t, err)

	first, err := manager.AddReceipt(testReceipt(0, 2, testHash(0x10), block2Hash, 1))
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Equal(t, uint64(1), first.WindowID)
	require.Equal(t, uint64(2), first.Cuts[0].EndHeight)

	for windowID, endHeight := range []uint64{2, 3, 4} {
		frozen, ok := manager.PeekFrozen()
		require.True(t, ok)
		require.Equal(t, uint64(windowID+1), frozen.WindowID)
		require.Equal(t, endHeight, frozen.Cuts[0].EndHeight)
		require.NoError(t, manager.AcknowledgeFrozen(frozen.WindowID))
	}
	_, ok := manager.PeekFrozen()
	require.False(t, ok)
}

func TestSuccessiveWindowsUsePreviousVectorCut(t *testing.T) {
	t.Parallel()

	manager := newManager(
		t,
		window.Config{ShardCount: 2, BatchSize: 1, MaxWindowDuration: time.Minute},
		newFakeClock(),
	)
	first, err := manager.AddReceipt(testReceipt(0, 2, testHash(0x10), testHash(0x11), 1))
	require.NoError(t, err)
	require.Equal(t, uint64(1), first.WindowID)
	require.Equal(t, uint64(1), first.Cuts[0].PreviousHeight)
	require.Equal(t, uint64(2), first.Cuts[0].EndHeight)

	second, err := manager.AddReceipt(testReceipt(0, 3, testHash(0x11), testHash(0x12), 2))
	require.NoError(t, err)
	require.Equal(t, uint64(2), second.WindowID)
	require.Equal(t, uint64(2), second.Cuts[0].PreviousHeight)
	require.Equal(t, uint64(3), second.Cuts[0].EndHeight)

	peeked, ok := manager.PeekFrozen()
	require.True(t, ok)
	require.Equal(t, first, peeked)
	require.ErrorIs(t, manager.AcknowledgeFrozen(second.WindowID), window.ErrUnexpectedWindow)
	require.NoError(t, manager.AcknowledgeFrozen(first.WindowID))
	peeked, ok = manager.PeekFrozen()
	require.True(t, ok)
	require.Equal(t, second, peeked)
	require.NoError(t, manager.AcknowledgeFrozen(second.WindowID))
	_, ok = manager.PeekFrozen()
	require.False(t, ok)
}

func TestSnapshotRestorePreservesBufferedGapAndTimer(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	cfg := window.Config{ShardCount: 2, BatchSize: 10, MaxWindowDuration: 10 * time.Second}
	manager := newManager(t, cfg, clock)

	block2Hash := testHash(0x11)
	_, err := manager.AddReceipt(testReceipt(0, 3, block2Hash, testHash(0x12), 2))
	require.NoError(t, err)
	state := manager.Snapshot()
	restored, err := window.Restore(cfg, clock, state)
	require.NoError(t, err)
	_, err = restored.AddReceipt(testReceipt(0, 2, testHash(0x10), block2Hash, 1))
	require.NoError(t, err)
	require.Equal(t, 2, restored.PendingIntentCount())
	clock.Advance(10 * time.Second)
	sealed := restored.TryClose()
	require.NotNil(t, sealed)
	require.Equal(t, uint64(1), sealed.WindowID)
	require.Len(t, sealed.Intents, 2)
	require.Equal(t, uint64(3), sealed.Cuts[0].EndHeight)
}

func TestManagerRejectsConflictingReceiptAndBrokenParent(t *testing.T) {
	t.Parallel()

	manager := newManager(
		t,
		window.Config{ShardCount: 2, BatchSize: 10, MaxWindowDuration: time.Minute},
		newFakeClock(),
	)
	future := testReceipt(0, 3, testHash(0x11), testHash(0x12), 2)
	_, err := manager.AddReceipt(future)
	require.NoError(t, err)
	conflict := future
	conflict.BlockHash = testHash(0xff)
	_, err = manager.AddReceipt(conflict)
	require.ErrorIs(t, err, window.ErrConflictingReceipt)

	_, err = manager.AddReceipt(testReceipt(0, 2, testHash(0xee), testHash(0x11), 1))
	require.ErrorIs(t, err, window.ErrParentHashMismatch)
	requireWatermark(t, manager, 0, 1, testHash(0x10))
}

func TestManagerRejectsDuplicateIntentAcrossBlocks(t *testing.T) {
	t.Parallel()

	manager := newManager(
		t,
		window.Config{ShardCount: 2, BatchSize: 10, MaxWindowDuration: time.Minute},
		newFakeClock(),
	)
	first := testReceipt(0, 2, testHash(0x10), testHash(0x11), 1)
	_, err := manager.AddReceipt(first)
	require.NoError(t, err)

	second := testReceipt(0, 3, testHash(0x11), testHash(0x12))
	second.Intents = append(second.Intents, first.Intents[0])
	_, err = manager.AddReceipt(second)
	require.ErrorIs(t, err, window.ErrDuplicateIntent)
}

func TestManagerRejectsInvalidConfigurationAndReceipts(t *testing.T) {
	t.Parallel()

	validCfg := window.Config{ShardCount: 2, BatchSize: 10, MaxWindowDuration: time.Minute}
	for name, cfg := range map[string]window.Config{
		"shard count": {ShardCount: 0, BatchSize: 1, MaxWindowDuration: time.Second},
		"batch size":  {ShardCount: 1, BatchSize: 0, MaxWindowDuration: time.Second},
		"duration":    {ShardCount: 1, BatchSize: 1, MaxWindowDuration: 0},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := window.New(cfg, newFakeClock(), nil)
			require.ErrorIs(t, err, window.ErrInvalidConfig)
		})
	}

	_, err := window.New(validCfg, newFakeClock(), map[int64]model.ShardCheckpoint{
		0: {Height: 1, BlockHash: testHash(0x10)},
	})
	require.ErrorIs(t, err, window.ErrMissingCheckpoint)

	manager := newManager(t, validCfg, newFakeClock())
	_, err = manager.AddReceipt(testReceipt(3, 2, testHash(0x30), testHash(0x31)))
	require.ErrorIs(t, err, window.ErrUnknownShard)
	_, err = manager.AddReceipt(testReceipt(0, 0, testHash(0x10), testHash(0x11)))
	require.ErrorIs(t, err, window.ErrInvalidReceipt)
	wrongSource := testReceipt(0, 2, testHash(0x10), testHash(0x11), 1)
	wrongSource.Intents[0].SourceShard = 1
	_, err = manager.AddReceipt(wrongSource)
	require.ErrorIs(t, err, window.ErrInvalidReceipt)
	_, err = manager.Watermark(3)
	require.ErrorIs(t, err, window.ErrUnknownShard)
	require.ErrorIs(t, manager.AcknowledgeFrozen(1), window.ErrFrozenQueueEmpty)
}

func TestManagerRejectsPreviouslyAssignedIntent(t *testing.T) {
	t.Parallel()

	manager := newManager(
		t,
		window.Config{ShardCount: 2, BatchSize: 1, MaxWindowDuration: time.Minute},
		newFakeClock(),
	)
	first := testReceipt(0, 2, testHash(0x10), testHash(0x11), 1)
	_, err := manager.AddReceipt(first)
	require.NoError(t, err)

	second := testReceipt(0, 3, testHash(0x11), testHash(0x12))
	second.Intents = append(second.Intents, first.Intents[0])
	_, err = manager.AddReceipt(second)
	require.ErrorIs(t, err, window.ErrDuplicateIntent)
}

func TestRestoreRejectsCorruptState(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	cfg := window.Config{ShardCount: 2, BatchSize: 10, MaxWindowDuration: time.Minute}
	manager := newManager(t, cfg, clock)
	_, err := manager.AddReceipt(testReceipt(0, 2, testHash(0x10), testHash(0x11), 1))
	require.NoError(t, err)

	for name, corrupt := range map[string]func(*window.State){
		"next window ID": func(state *window.State) {
			state.NextWindowID = 0
		},
		"opened time": func(state *window.State) {
			state.OpenedAt = nil
		},
		"continuous hash": func(state *window.State) {
			stream := state.Streams[0]
			stream.ContinuousHash = testHash(0xff)
			state.Streams[0] = stream
		},
		"missing unassigned block": func(state *window.State) {
			stream := state.Streams[0]
			delete(stream.UnassignedBlocks, 2)
			state.Streams[0] = stream
		},
		"missing pending intent": func(state *window.State) {
			for id := range state.Pending {
				delete(state.Pending, id)
			}
			state.OpenedAt = nil
		},
		"invalid assigned window": func(state *window.State) {
			for id := range state.Pending {
				state.Assigned[id] = state.NextWindowID
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			state := manager.Snapshot()
			corrupt(&state)
			_, restoreErr := window.Restore(cfg, clock, state)
			require.ErrorIs(t, restoreErr, window.ErrInvalidState)
		})
	}
}

func TestSnapshotAndFrozenWindowAreDeepCopies(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	manager := newManager(
		t,
		window.Config{ShardCount: 2, BatchSize: 10, MaxWindowDuration: time.Second},
		clock,
	)
	_, err := manager.AddReceipt(testReceipt(0, 2, testHash(0x10), testHash(0x11), 1))
	require.NoError(t, err)

	state := manager.Snapshot()
	for id, payment := range state.Pending {
		payment.Amount.SetInt64(999)
		state.Pending[id] = payment
	}
	clock.Advance(time.Second)
	sealed := manager.TryClose()
	require.NotNil(t, sealed)
	require.Equal(t, int64(2), sealed.Intents[0].Amount.Int64())

	sealed.Intents[0].Amount.SetInt64(777)
	peeked, ok := manager.PeekFrozen()
	require.True(t, ok)
	require.Equal(t, int64(2), peeked.Intents[0].Amount.Int64())
}

type fakeClock struct {
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) Now() time.Time {
	return f.now
}

func (f *fakeClock) Advance(duration time.Duration) {
	f.now = f.now.Add(duration)
}

func newManager(t *testing.T, cfg window.Config, clock window.Clock) *window.Manager {
	t.Helper()
	checkpoints := make(map[int64]model.ShardCheckpoint, cfg.ShardCount)
	for shardID := range cfg.ShardCount {
		checkpoints[shardID] = model.ShardCheckpoint{Height: 1, BlockHash: testHash(byte(shardID+1) * 0x10)}
	}

	manager, err := window.New(cfg, clock, checkpoints)
	require.NoError(t, err)

	return manager
}

func testReceipt(
	shardID int64,
	height uint64,
	parentHash, blockHash merkle.Hash,
	intentTags ...byte,
) model.FinalizedBlockReceipt {
	intents := make([]intent.PaymentIntent, 0, len(intentTags))
	for _, tag := range intentTags {
		intents = append(intents, windowTestIntent(tag, shardID))
	}

	return model.FinalizedBlockReceipt{
		ShardID:    shardID,
		Height:     height,
		BlockHash:  blockHash,
		ParentHash: parentHash,
		StateRoot:  testHash(blockHash[0] + 1),
		Epoch:      1,
		Intents:    intents,
		CommitTime: time.Date(2026, 9, 3, 11, 0, int(height), 0, time.UTC),
	}
}

func windowTestIntent(tag byte, sourceShard int64) intent.PaymentIntent {
	var sender, recipient account.Address
	sender[0] = tag
	recipient[0] = tag + 0x40
	destinationShard := int64(0)
	if sourceShard == 0 {
		destinationShard = 1
	}

	return intent.PaymentIntent{
		Version:          intent.CurrentVersion,
		ChainID:          11,
		Sender:           sender,
		Recipient:        recipient,
		SourceShard:      sourceShard,
		DestinationShard: destinationShard,
		AssetID:          intent.NativeAssetID,
		Amount:           big.NewInt(int64(tag) + 1),
		Nonce:            uint64(tag),
		ExpiryEpoch:      10,
	}
}

func testHash(value byte) merkle.Hash {
	var hash merkle.Hash
	hash[0] = value
	return hash
}

func requireWatermark(t *testing.T, manager *window.Manager, shardID int64, height uint64, hash merkle.Hash) {
	t.Helper()
	watermark, err := manager.Watermark(shardID)
	require.NoError(t, err)
	require.Equal(t, height, watermark.Height)
	require.Equal(t, hash, watermark.BlockHash)
}
