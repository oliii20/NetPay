package message_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
)

func TestProposalRequiresExactlyOnePayloadAndHashesDeterministically(t *testing.T) {
	t.Parallel()

	_, err := (&message.Proposal{}).Hash()
	require.Error(t, err)
	_, err = (&message.Proposal{Block: &block.Block{}, AggregationBatch: &model.AggregationBatch{}}).Hash()
	require.Error(t, err)

	proposal := message.WrapAggregationProposal(model.AggregationBatch{
		Header: model.AggregationBatchHeader{WindowID: 1},
	})
	first, err := proposal.Hash()
	require.NoError(t, err)
	second, err := proposal.Hash()
	require.NoError(t, err)
	require.Equal(t, first, second)
}
