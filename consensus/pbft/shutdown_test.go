package pbft

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
)

func TestStopTakesPriorityOverQueuedWork(t *testing.T) {
	for _, tc := range []struct {
		name  string
		types []string
		stop  bool
	}{
		{"empty batch", nil, false},
		{"normal work", []string{message.CLPARepartitionStartMessageType}, false},
		{"stop before migration", []string{message.StopConsensusMessageType, message.CLPARepartitionStartMessageType}, true},
		{"stop after migration", []string{message.CLPARepartitionStartMessageType, message.StopConsensusMessageType}, true},
		{"stop with consensus", []string{message.PreprepareMessageType, message.CommitMessageType, message.StopConsensusMessageType}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := &Node{pbftMeta: newConsensusMeta(consensusCfg, localParams)}
			msgs := make([]*rpcserver.WrappedMsg, 0, len(tc.types))
			for _, kind := range tc.types {
				msgs = append(msgs, &rpcserver.WrappedMsg{MsgType: kind})
			}
			require.Equal(t, tc.stop, n.stopRequested(context.Background(), msgs))
			require.Equal(t, tc.stop, n.pbftMeta.closed)
		})
	}
}
