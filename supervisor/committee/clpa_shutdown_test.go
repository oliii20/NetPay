package committee

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCLPAShutdownWaitsForMigration(t *testing.T) {
	for _, kind := range []string{"relay", "broker"} {
		t.Run(kind, func(t *testing.T) {
			for _, tc := range []struct {
				name       string
				unsent     int64
				empty      int64
				shardEpoch []int64
				wantStop   bool
			}{
				{"transactions remain", 1, 8, []int64{2, 2}, false},
				{"draining transactions", 0, 7, []int64{2, 2}, false},
				{"migration in progress", 0, 8, []int64{2, 1}, false},
				{"migration complete", 0, 8, []int64{2, 2}, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					runner := partitionRunner{supervisorEpoch: 2, shardEpoch: tc.shardEpoch}
					relay := &CLPARelayCommittee{partitionRunner: runner, unsentTxNum: tc.unsent}
					broker := &CLPABrokerCommittee{partitionRunner: runner, unsentTxNum: tc.unsent}
					relay.sl.stopCnt, relay.sl.stopThreshold = tc.empty, 8
					broker.sl = relay.sl
					if kind == "relay" {
						require.Equal(t, tc.wantStop, relay.ShouldStop())
					} else {
						require.Equal(t, tc.wantStop, broker.ShouldStop())
					}
				})
			}
		})
	}
}

func TestCLPACompletedWorkloadDoesNotStartNewRound(t *testing.T) {
	// The CLPA timer has expired just as the final empty-block reports arrive.
	// Nil network/source dependencies ensure stopping performs no further work.
	for _, kind := range []string{"relay", "broker"} {
		t.Run(kind, func(t *testing.T) {
			runner := partitionRunner{
				epochSynced: true, supervisorEpoch: 2,
				shardEpoch: []int64{2, 2}, lastRunTime: time.Now().Add(-time.Hour),
			}
			if kind == "relay" {
				c := &CLPARelayCommittee{partitionRunner: runner}
				c.sl.stopCnt, c.sl.stopThreshold = 8, 8
				require.NoError(t, c.SendTxsAndConsensus(context.Background()))
				require.EqualValues(t, 2, c.supervisorEpoch)
			} else {
				c := &CLPABrokerCommittee{partitionRunner: runner}
				c.sl.stopCnt, c.sl.stopThreshold = 8, 8
				require.NoError(t, c.SendTxsAndConsensus(context.Background()))
				require.EqualValues(t, 2, c.supervisorEpoch)
			}
		})
	}
}
