package message_test

import (
	"bytes"
	"encoding/gob"
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
	_, err = (&message.Proposal{AggregationBatch: &model.AggregationBatch{}, AggregationHeader: &model.AggregationBatchHeader{}}).Hash()
	require.Error(t, err)

	proposal := message.WrapAggregationProposal(model.AggregationBatch{
		Header: model.AggregationBatchHeader{WindowID: 1},
	})
	first, err := proposal.Hash()
	require.NoError(t, err)
	second, err := proposal.Hash()
	require.NoError(t, err)
	require.Equal(t, first, second)

	header := message.WrapAggregationHeaderProposal(proposal.AggregationBatch.Header)
	headerHash, err := header.Hash()
	require.NoError(t, err)
	header.AggregationHeader.WindowID++
	changedHash, err := header.Hash()
	require.NoError(t, err)
	require.NotEqual(t, headerHash, changedHash)
}

func TestAggregationHeaderHashSurvivesGobRoundTrip(t *testing.T) {
	t.Parallel()
	header := message.WrapAggregationHeaderProposal(model.AggregationBatchHeader{
		WindowID: 7,
		Cuts:     []model.ShardCut{{ShardID: 2, PreviousHeight: 3, EndHeight: 5}},
	})
	want, err := header.Hash()
	require.NoError(t, err)

	var encoded bytes.Buffer
	require.NoError(t, gob.NewEncoder(&encoded).Encode(header))
	var decoded message.Proposal
	require.NoError(t, gob.NewDecoder(&encoded).Decode(&decoded))
	got, err := decoded.Hash()
	require.NoError(t, err)
	require.Equal(t, want, got)
}
