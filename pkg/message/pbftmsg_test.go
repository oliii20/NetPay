package message_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
)

func TestProposalRequiresExactlyOnePayloadAndHashesDeterministically(t *testing.T) {
	t.Parallel()

	_, err := (&message.Proposal{}).Hash()
	require.Error(t, err)
	_, err = (&message.Proposal{Block: &block.Block{}, NettingBatch: &model.BatchProposal{}}).Hash()
	require.Error(t, err)

	proposal := message.WrapNettingProposal(model.BatchProposal{
		Header: model.MatchRootBlockBody{WindowID: 1},
	})
	first, err := proposal.Hash()
	require.NoError(t, err)
	second, err := proposal.Hash()
	require.NoError(t, err)
	require.Equal(t, first, second)
}
