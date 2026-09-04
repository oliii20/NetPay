package batchstore_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/batchstore"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
)

func TestStorePersistsStateAndProposalAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "solver.db")
	store, err := batchstore.Open(path)
	require.NoError(t, err)

	id := storeHash(0x42)
	state := batchstore.SolverState{ChainTip: id, PendingBatchIDs: []merkle.Hash{id}}
	proposal := model.BatchProposal{Header: model.MatchRootBlockBody{BatchID: id, WindowID: 1}}
	require.NoError(t, store.PutProposalAndState(proposal, state))
	require.NoError(t, store.Close())

	store, err = batchstore.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	loadedState, found, err := store.LoadState()
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, state.ChainTip, loadedState.ChainTip)
	require.Equal(t, state.PendingBatchIDs, loadedState.PendingBatchIDs)
	loadedProposal, err := store.GetProposal(id)
	require.NoError(t, err)
	require.Equal(t, proposal.Header, loadedProposal.Header)
	require.Empty(t, loadedProposal.Sidecar.FinalizedBlocks)
	require.Empty(t, loadedProposal.Sidecar.IntentResults)
	require.Empty(t, loadedProposal.Sidecar.ShardSettlements)

	_, err = store.GetProposal(storeHash(0xff))
	require.ErrorIs(t, err, batchstore.ErrProposalNotFound)
}

func storeHash(value byte) merkle.Hash {
	var result merkle.Hash
	result[0] = value

	return result
}
