package config

import "fmt"

func validateBeaconExperimentConfig(cfg *Config) error {
	if cfg.ExperimentWorkUSPerPayment < 0 {
		return fmt.Errorf("experiment_work_us_per_payment must not be negative")
	}

	if cfg.ExperimentWorkUSPerPayment > 0 && (!cfg.Enabled || !cfg.AdaptiveRouting.Enabled) {
		return fmt.Errorf("beacon work experiment requires the enabled post-commit adaptive routing path")
	}

	return nil
}

func validateAdaptiveRoutingConfig(cfg *Config) error {
	cfg.AdaptiveRouting = cfg.AdaptiveRouting.WithDefaults()
	if err := cfg.AdaptiveRouting.Validate(); err != nil {
		return fmt.Errorf("adaptive routing: %w", err)
	}

	if !cfg.Enabled || cfg.GlobalSys.ConsensusType != StaticRelayConsensus || cfg.DestinationShards != nil ||
		cfg.BatchOnly || cfg.AggregationOnly || cfg.MaxAggregationWindow != 0 || cfg.MaxInjectionDurationMS != 0 {
		return fmt.Errorf(
			"adaptive routing requires enabled static_relay aggregation without a fixed destination list, ablations or injection cutoffs",
		)
	}

	return nil
}
