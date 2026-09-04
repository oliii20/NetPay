package measure_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
	"github.com/HuangLab-SYSU/block-emulator-x/supervisor/measure"
)

func TestRouterSeparatesLegacyMetricsAndProtocolTraffic(t *testing.T) {
	t.Parallel()

	legacy := &recordingMeasure{}
	netting := &recordingMeasure{}
	router := measure.NewRouter(legacy, netting)
	require.NoError(t, router.UpdateMeasureRecord(&rpcserver.WrappedMsg{MsgType: message.RelayBlockInfoMessageType}))
	require.NoError(t, router.UpdateMeasureRecord(&rpcserver.WrappedMsg{MsgType: message.NettingBatchMetricMessageType}))
	require.NoError(t, router.UpdateMeasureRecord(&rpcserver.WrappedMsg{MsgType: message.FallbackCompletedMessageType}))
	require.Equal(t, []string{message.RelayBlockInfoMessageType}, legacy.types)
	require.Equal(t, []string{message.NettingBatchMetricMessageType}, netting.types)
	require.NoError(t, router.OutputResultAndClose())
	require.True(t, legacy.closed)
	require.True(t, netting.closed)
}

type recordingMeasure struct {
	types  []string
	closed bool
}

func (r *recordingMeasure) UpdateMeasureRecord(msg *rpcserver.WrappedMsg) error {
	r.types = append(r.types, msg.GetMsgType())

	return nil
}

func (r *recordingMeasure) OutputResultAndClose() error {
	r.closed = true

	return nil
}
