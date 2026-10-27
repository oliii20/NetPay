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

func TestAggregationOnlyConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		batchOnly, sourceAggregate bool
		wantErr                    string
	}{
		{name: "one credit per settlement"},
		{name: "conflicting modes", batchOnly: true, wantErr: "mutually exclusive"},
		{name: "source merging disabled", sourceAggregate: true, wantErr: "source_aggregate_abstracts=false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{GlobalSys: SystemCfg{ShardNum: 4, ConsensusType: StaticRelayConsensus}, AggregationCfg: AggregationCfg{
				Enabled: true, AggregationOnly: true, BatchOnly: tc.batchOnly, SourceAggregateAbstracts: tc.sourceAggregate,
				CreditChunkSize: 512, MaxWindowDurationMS: 2000, BeaconStorePath: "beacon.db",
			}}
			err := validateAggregationConfig(cfg)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, cfg.CreditChunkSize)
		})
	}
}

func TestDestinationAllowlistValidation(t *testing.T) {
	for _, tc := range []struct {
		ids       []int64
		enabled   bool
		consensus string
		valid     bool
	}{
		{nil, true, StaticRelayConsensus, true},
		{[]int64{}, true, StaticRelayConsensus, true},
		{[]int64{0, 3}, true, StaticRelayConsensus, true},
		{[]int64{4}, true, StaticRelayConsensus, false},
		{[]int64{1, 1}, true, StaticRelayConsensus, false},
		{[]int64{-1}, true, StaticRelayConsensus, false},
		{[]int64{1}, false, StaticRelayConsensus, false},
		{[]int64{1}, true, StaticBrokerConsensus, false},
	} {
		cfg := &Config{GlobalSys: SystemCfg{ShardNum: 4, ConsensusType: tc.consensus}, AggregationCfg: AggregationCfg{
			Enabled: tc.enabled, DestinationShards: tc.ids, MaxWindowDurationMS: 2000, BeaconStorePath: "beacon.db",
		}}
		err := validateAggregationConfig(cfg)
		if tc.valid {
			require.NoError(t, err)
		} else {
			require.Error(t, err)
		}
	}
}
