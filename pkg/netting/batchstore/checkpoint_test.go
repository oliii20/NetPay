package batchstore_test

import (
	"bytes"
	"encoding/gob"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/batchstore"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/window"
)

func TestCheckpointPreservesAssignmentsAcrossReopen(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "current"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "solver.db")
			first := intent.ID{1}
			second := intent.ID{2}
			state := batchstore.SolverState{ManagerState: &window.State{
				NextWindowID: 2, Assigned: map[intent.ID]uint64{first: 1},
			}}
			store, err := batchstore.Open(path)
			require.NoError(t, err)
			require.NoError(t, store.SaveState(state))
			require.NoError(t, store.Close())
			if legacy {
				var encoded bytes.Buffer
				require.NoError(t, gob.NewEncoder(&encoded).Encode(state))
				db, openErr := bbolt.Open(path, 0o600, nil)
				require.NoError(t, openErr)
				require.NoError(t, db.Update(func(tx *bbolt.Tx) error {
					if deleteErr := tx.DeleteBucket([]byte("assigned_intents")); deleteErr != nil {
						return deleteErr
					}
					return tx.Bucket([]byte("solver_state")).Put([]byte("current"), encoded.Bytes())
				}))
				require.NoError(t, db.Close())
			}
			store, err = batchstore.Open(path)
			require.NoError(t, err)
			loaded, found, err := store.LoadState()
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, state.ManagerState.Assigned, loaded.ManagerState.Assigned)
			checkpoint := batchstore.SolverState{ManagerState: &window.State{
				NextWindowID: 3, Assigned: map[intent.ID]uint64{second: 2},
			}}
			proposal := model.BatchProposal{Header: model.MatchRootBlockBody{BatchID: storeHash(3)}}
			require.NoError(t, store.PutProposalAndCheckpoint(proposal, checkpoint))
			require.Len(t, checkpoint.ManagerState.Assigned, 1)
			checkpoint.ManagerState.Assigned = nil
			require.NoError(t, store.SaveCheckpoint(checkpoint))
			require.NoError(t, store.Close())
			store, err = batchstore.Open(path)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, store.Close()) })
			loaded, found, err = store.LoadState()
			require.NoError(t, err)
			require.True(t, found)
			require.Equal(t, map[intent.ID]uint64{first: 1, second: 2}, loaded.ManagerState.Assigned)
			_, err = store.GetProposal(proposal.Header.BatchID)
			require.NoError(t, err)
			require.NoError(t, store.SaveState(state))
			loaded, _, err = store.LoadState()
			require.NoError(t, err)
			require.Equal(t, state.ManagerState.Assigned, loaded.ManagerState.Assigned)
		})
	}
}
