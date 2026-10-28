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
		require.Equal(t, int64(500), cfg.AdaptiveRouting.FreezeHighMS)
		require.Equal(t, int64(100), cfg.AdaptiveRouting.FreezeLowMS)
		require.Equal(t, int64(10000), cfg.AdaptiveRouting.CooldownMS)
		require.Equal(t, int64(2000), cfg.AdaptiveRouting.RecoveryWindowMS)
		require.Equal(t, 3, cfg.AdaptiveRouting.RecoveryWindows)
		require.Equal(t, 75, cfg.AdaptiveRouting.RecoveryChangePercent)
	})
	for name, mutate := range map[string]func(*Config){
		"fixed allowlist":          func(c *Config) { c.DestinationShards = []int64{1} },
		"broker":                   func(c *Config) { c.GlobalSys.ConsensusType = StaticBrokerConsensus },
		"disabled aggregation":     func(c *Config) { c.Enabled = false },
		"batch ablation":           func(c *Config) { c.BatchOnly = true },
		"cutoff":                   func(c *Config) { c.MaxAggregationWindow = 2 },
		"bad hysteresis":           func(c *Config) { c.AdaptiveRouting.FreezeLowMS = 3000 },
		"negative experiment cost": func(c *Config) { c.ExperimentWorkUSPerPayment = -1 },
		"negative recovery window": func(c *Config) { c.AdaptiveRouting.RecoveryWindowMS = -1 },
		"invalid recovery samples": func(c *Config) { c.AdaptiveRouting.RecoveryWindows = -1 },
		"invalid recovery change":  func(c *Config) { c.AdaptiveRouting.RecoveryChangePercent = 101 },
	} {
		t.Run(name, func(t *testing.T) { cfg := newConfig(); mutate(cfg); require.Error(t, validateAggregationConfig(cfg)) })
	}
	t.Run("experiment requires new routing path", func(t *testing.T) {
		cfg := newConfig()
		cfg.ExperimentWorkUSPerPayment = 100
		cfg.AdaptiveRouting.Enabled = false
		require.Error(t, validateAggregationConfig(cfg))
		cfg.AdaptiveRouting.Enabled = true
		cfg.AdaptiveRouting.MinDestinationPercent = 100
		require.NoError(t, validateAggregationConfig(cfg))
	})
}
