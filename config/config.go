package config

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/utils"
)

type Config struct {
	GlobalSys        SystemCfg `json:"system"         yaml:"system"`
	ConsensusNodeCfg `          json:"consensus_node" yaml:"consensus_node"`
	SupervisorCfg    `          json:"supervisor"     yaml:"supervisor"`
	NetworkCfg       `          json:"network"        yaml:"network"`
	AggregationCfg   `          json:"aggregation"    yaml:"aggregation"`
}

type LocalParams struct {
	NodeID     int64
	ShardID    int64
	WalletAddr account.Address
}

// local parameters are defined here, and they are read from command lines.
var (
	// localNodeID is the node ID of this node.
	localNodeID = flag.Int64("node_id", -1, "local node id")

	// localShardID is the shard ID of this node.
	localShardID = flag.Int64("shard_id", -1, "local shard id, 0x7fffffff denotes the supervisor shard")

	// accountAddr is the miner address of this node.
	accountAddr = flag.String("account_addr", "", "miner address")
)

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file %s: %w", path, err)
	}

	cfg := new(Config)

	switch filepath.Ext(path) {
	case ".json":
		if err = json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("json unmarshal %s: %w", path, err)
		}
	case ".yaml", ".yml":
		if err = yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("yaml unmarshal %s: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("unsupported cfg type")
	}

	if err = validateAggregationConfig(cfg); err != nil {
		return nil, err
	}

	// pass the global system config to the supervisor config
	cfg.SystemCfg = cfg.GlobalSys
	cfg.SupervisorCfg.AggregationCfg = cfg.AggregationCfg
	cfg.ChainID = cfg.ConsensusNodeCfg.ChainID
	// pass the global system config to the consensus config
	cfg.ConsensusNodeCfg.SystemCfg = cfg.GlobalSys
	cfg.ConsensusNodeCfg.AggregationCfg = cfg.AggregationCfg

	return cfg, nil
}

func validateAggregationConfig(cfg *Config) error {
	if cfg.AdaptiveRouting.Enabled {
		if err := validateAdaptiveRoutingConfig(cfg); err != nil {
			return err
		}
	}

	if cfg.DestinationShards != nil {
		if !cfg.Enabled || cfg.GlobalSys.ConsensusType != StaticRelayConsensus {
			return fmt.Errorf("static destination selection requires enabled aggregation and static_relay")
		}

		seen := make(map[int64]bool)
		for _, id := range cfg.DestinationShards {
			if id < 0 || id >= cfg.GlobalSys.ShardNum || seen[id] {
				return fmt.Errorf("invalid or duplicate destination shard: %d", id)
			}

			seen[id] = true
		}
	}

	if !cfg.Enabled {
		return nil
	}

	if cfg.GlobalSys.ShardNum <= 1 {
		return fmt.Errorf("aggregation requires at least two shards")
	}

	if cfg.ConsensusNodeCfg.ChainID < 0 {
		return fmt.Errorf("aggregation requires a non-negative chain ID")
	}

	if cfg.AggregationOnly && cfg.BatchOnly {
		return fmt.Errorf("aggregation_only and batch_only are mutually exclusive")
	}

	if cfg.AggregationOnly && cfg.SourceAggregateAbstracts {
		return fmt.Errorf("aggregation_only requires source_aggregate_abstracts=false")
	}

	if cfg.AggregationOnly {
		cfg.CreditChunkSize = 1
	}

	if cfg.BatchOnly && cfg.SourceAggregateAbstracts {
		return fmt.Errorf("batch_only requires source_aggregate_abstracts=false")
	}

	if cfg.BatchSize < 0 || cfg.CreditChunkSize < 0 {
		return fmt.Errorf("aggregation batch and credit chunk sizes must be non-negative")
	}

	if cfg.MaxWindowDurationMS <= 0 {
		return fmt.Errorf("aggregation max_window_duration_ms must be positive")
	}

	if cfg.BeaconStorePath == "" {
		return fmt.Errorf("aggregation beacon_store_path must not be empty")
	}

	if cfg.GlobalSys.ConsensusType != StaticRelayConsensus && cfg.GlobalSys.ConsensusType != StaticBrokerConsensus {
		return fmt.Errorf("aggregation currently requires static_relay or static_broker consensus")
	}

	return nil
}

func LoadLocalParams() (*LocalParams, error) {
	walletAddr, err := utils.Hex2Addr(*accountAddr)
	if err != nil {
		return nil, fmt.Errorf("load wallet address: %w", err)
	}

	if *localNodeID < 0 || *localShardID < 0 {
		return nil, fmt.Errorf("invalid local params, node id: %d, shard id: %d", *localNodeID, *localShardID)
	}

	return &LocalParams{
		NodeID:     *localNodeID,
		ShardID:    *localShardID,
		WalletAddr: walletAddr,
	}, nil
}
