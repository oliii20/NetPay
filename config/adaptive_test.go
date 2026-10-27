package config

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/routing"
)

func TestAdaptiveRoutingConfiguration(t *testing.T) {
	newConfig := func() *Config {
		return &Config{GlobalSys: SystemCfg{ShardNum: 24, ConsensusType: StaticRelayConsensus},
			AggregationCfg: AggregationCfg{Enabled: true, MaxWindowDurationMS: 2000, BeaconStorePath: "beacon.db", AdaptiveRouting: routing.Config{Enabled: true}}}
	}
	t.Run("defaults start with all destinations", func(t *testing.T) {
		cfg := newConfig()
		require.NoError(t, validateAggregationConfig(cfg))
		require.Nil(t, cfg.DestinationShards)
		require.Equal(t, int64(2000), cfg.AdaptiveRouting.FreezeHighMS)
	})
	for name, mutate := range map[string]func(*Config){
		"fixed allowlist":      func(c *Config) { c.DestinationShards = []int64{1} },
		"broker":               func(c *Config) { c.GlobalSys.ConsensusType = StaticBrokerConsensus },
		"disabled aggregation": func(c *Config) { c.Enabled = false },
		"batch ablation":       func(c *Config) { c.BatchOnly = true },
		"cutoff":               func(c *Config) { c.MaxAggregationWindow = 2 },
		"bad hysteresis":       func(c *Config) { c.AdaptiveRouting.FreezeLowMS = 3000 },
	} {
		t.Run(name, func(t *testing.T) { cfg := newConfig(); mutate(cfg); require.Error(t, validateAggregationConfig(cfg)) })
	}
}
