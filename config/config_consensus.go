package config

const (
	StaticRelayConsensus  = "static_relay"
	StaticBrokerConsensus = "static_broker"
	CLPARelayConsensus    = "clpa_relay"
	CLPABrokerConsensus   = "clpa_broker"
)

type SystemCfg struct {
	ShardNum       int64  `json:"shard_num"      yaml:"shard_num"`
	NodeNum        int64  `json:"node_num"       yaml:"node_num"`
	ConsensusType  string `json:"consensus_type" yaml:"consensus_type"`
	BlockSizeLimit int64  `json:"limit"          yaml:"limit"`
	LogCfg         `       json:"log"            yaml:"log"`
}

type SupervisorCfg struct {
	SystemCfg
	NettingCfg       NettingCfg `json:"-" yaml:"-"`
	ChainID          int64      `json:"-" yaml:"-"`
	TxNumber         int64      `json:"tx_number"          yaml:"tx_number"`
	TxInjectionSpeed int64      `json:"tx_injection_speed" yaml:"tx_injection_speed"` // transactions per second
	ResultOutputDir  string     `json:"result_output_dir"  yaml:"result_output_dir"`
	EpochDuration    int64      `json:"epoch_duration"     yaml:"epoch_duration"`
	TxSourceCfg      `       json:"tx_source"          yaml:"tx_source"`
	BrokerModuleCfg  `       json:"broker_module"      yaml:"broker_module"`
}

type ConsensusNodeCfg struct {
	BlockchainCfg         `       json:"blockchain"       yaml:"blockchain"`
	TxPoolCfg             `       json:"tx_pool"          yaml:"tx_pool"`
	NettingCfg            NettingCfg      `json:"-" yaml:"-"`
	BlockInterval         int64           `json:"block_interval"            yaml:"block_interval"` // ms
	ShardBlockIntervalsMS map[int64]int64 `json:"shard_block_intervals_ms" yaml:"shard_block_intervals_ms"`
	BlockRecordDir        string          `json:"block_record_dir"          yaml:"block_record_dir"`
}

type NettingCfg struct {
	Enabled              bool   `json:"enabled"                yaml:"enabled"`
	MetricsEnabled       bool   `json:"metrics_enabled"        yaml:"metrics_enabled"`
	BatchSize            int    `json:"batch_size"             yaml:"batch_size"`
	MaxWindowDurationMS  int64  `json:"max_window_duration_ms" yaml:"max_window_duration_ms"`
	SolverTickIntervalMS int64  `json:"solver_tick_interval_ms" yaml:"solver_tick_interval_ms"`
	MatcherMode          string `json:"matcher_mode"           yaml:"matcher_mode"`
	BatchStorePath       string `json:"batch_store_path"       yaml:"batch_store_path"`
	BeaconStorePath      string `json:"beacon_store_path"      yaml:"beacon_store_path"`
}

type TxSourceCfg struct {
	TxSourceType       string `json:"tx_source_type"       yaml:"tx_source_type"`
	TxSourceFile       string `json:"tx_source_file"       yaml:"tx_source_file"`
	ExcludeContractTxs bool   `json:"exclude_contract_txs" yaml:"exclude_contract_txs"`
}

type BrokerModuleCfg struct {
	BrokerFilePath string `json:"broker_file_path" yaml:"broker_file_path"`
	BrokerNum      int64  `json:"broker_num"       yaml:"broker_num"`
}
