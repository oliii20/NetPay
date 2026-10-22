package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadAggregationConfig(t *testing.T) {
	t.Parallel()

	cfg, err := LoadConfig("config_test.yaml")
	require.NoError(t, err)
	require.True(t, cfg.AggregationCfg.Enabled)
	require.True(t, cfg.SourceAggregateAbstracts)
	require.Equal(t, 100, cfg.BatchSize)
	require.Equal(t, 50, cfg.CreditChunkSize)
	require.Equal(t, "./exp_test/aggregation/beacon.db", cfg.BeaconStorePath)
	require.Equal(t, cfg.AggregationCfg, cfg.ConsensusNodeCfg.AggregationCfg)
	require.Equal(t, cfg.AggregationCfg, cfg.SupervisorCfg.AggregationCfg)
}

func TestValidateAggregationConfigRejectsDynamicSharding(t *testing.T) {
	t.Parallel()

	cfg := &Config{
		GlobalSys: SystemCfg{ShardNum: 4, ConsensusType: CLPARelayConsensus},
		AggregationCfg: AggregationCfg{
			Enabled: true, MaxWindowDurationMS: 1000, BeaconStorePath: "beacon.db",
		},
	}
	require.ErrorContains(t, validateAggregationConfig(cfg), "static_relay or static_broker")
}
