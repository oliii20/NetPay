package solver_test

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/batchstore"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/solver"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/window"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network"
)

// Empty shard blocks continue arriving while the Solver drains its batch queue.
func BenchmarkSolverReceiptWithHistory(b *testing.B) {
	for _, count := range []int{0, 100000, 500000} {
		b.Run(fmt.Sprintf("assigned_%d", count), func(b *testing.B) {
			store, err := batchstore.Open(filepath.Join(b.TempDir(), "solver.db"))
			require.NoError(b, err)
			b.Cleanup(func() { require.NoError(b, store.Close()) })
			manager, err := window.New(window.Config{
				ShardCount: 2, MaxWindowDuration: time.Second,
			}, nil, map[int64]model.ShardCheckpoint{0: {}, 1: {}})
			require.NoError(b, err)
			state := manager.Snapshot()
			state.NextWindowID = 2
			for idx := range count {
				var id intent.ID
				binary.BigEndian.PutUint64(id[:8], uint64(idx))
				state.Assigned[id] = 1
			}
			require.NoError(b, store.SaveState(batchstore.SolverState{ManagerState: &state}))
			node, err := solver.New(solver.Config{
				ShardCount: 2, MaxWindowDuration: time.Second, TickInterval: time.Millisecond,
			}, network.NewConnHandler(&testP2P{}), testResolver{}, store)
			require.NoError(b, err)
			parent := merkle.Hash{}
			b.ReportAllocs()
			b.ResetTimer()
			for idx := range b.N {
				var hash merkle.Hash
				binary.BigEndian.PutUint64(hash[:8], uint64(idx+1))
				require.NoError(b, node.HandleReceipt(message.FinalizedBlockReceiptMsg{
					Receipt: model.FinalizedBlockReceipt{
						ShardID: 0, Height: uint64(idx + 1), ParentHash: parent, BlockHash: hash,
					},
				}))
				parent = hash
			}
		})
	}
}
