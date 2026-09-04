package model

import (
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
)

const (
	MetricPhaseReservation = "reservation"
	MetricPhaseSettlement  = "settlement"
	MetricPhaseFallback    = "fallback"
)

type ShardWindowMetric struct {
	ShardID    int64
	BlockCount uint64
	CutHeight  uint64
}

type NettingBatchMetric struct {
	BatchID                   merkle.Hash
	WindowID                  uint64
	CloseReason               string
	IntentCount               int
	Shards                    []ShardWindowMetric
	WatermarkSkew             uint64
	WindowOpenDuration        time.Duration
	FrozenWindowQueueLength   int
	ExactMatchedIntentCount   int
	BestFitMatchedIntentCount int
	SplitMatchedIntentCount   int
	SplitAllocationCount      int
	MatchedIntentCount        int
	FallbackIntentCount       int
	OriginalValue             string
	MatchedValue              string
	FallbackValue             string
	MatchTime                 time.Duration
	MerkleBuildTime           time.Duration
	BatchProposalBytes        int
	SidecarBytes              int
	BuiltAt                   time.Time
}

type NettingBeaconMetric struct {
	BatchID                merkle.Hash
	WindowID               uint64
	ValidationTime         time.Duration
	BeaconConsensusLatency time.Duration
	PreprepareBytes        int
	PrepareBytes           int
	CommitBytes            int
	MatchRootStorageBytes  int
	FinalizedAt            time.Time
}

type NettingExecutionMetric struct {
	Phase                 string
	BatchID               merkle.Hash
	WindowID              uint64
	IntentID              intent.ID
	ShardID               int64
	CreatedAt             time.Time
	CommittedAt           time.Time
	Final                 bool
	StateReadCount        int64
	StateWriteCount       int64
	ProofVerificationTime time.Duration
}
