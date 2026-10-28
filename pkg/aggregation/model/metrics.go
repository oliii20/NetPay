package model

import (
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
)

const (
	MetricPhaseSourceDebit = "source_debit"
	MetricPhaseSettlement  = "settlement"
)

type ShardWindowMetric struct {
	ShardID    int64
	BlockCount uint64
	CutHeight  uint64
}

type AggregationBatchMetric struct {
	ServiceStartedAt        time.Time
	ServiceQueueWait        time.Duration
	ExperimentServiceTime   time.Duration
	ReceiptTimingCount      int64
	SourceToHandlerMean     time.Duration
	HandlerToBuildMean      time.Duration
	WindowOpenedAt          time.Time
	WindowSealedAt          time.Time
	BuildStartedAt          time.Time
	ProposalReadyAt         time.Time
	FrozenQueueWait         time.Duration
	BuildTime               time.Duration
	LocalValidationTime     time.Duration
	BatchID                 merkle.Hash
	WindowID                uint64
	CloseReason             string
	ReceiptCount            int
	SourceAbstractCount     int
	SourceCollapsedCount    int
	CreditCount             int
	CollapsedReceiptCount   int
	SettlementTxCount       int
	Shards                  []ShardWindowMetric
	WatermarkSkew           uint64
	WindowOpenDuration      time.Duration
	FrozenWindowQueueLength int
	OriginalValue           string
	AggregatedValue         string
	AggregationTime         time.Duration
	MerkleBuildTime         time.Duration
	BatchProposalBytes      int
	SidecarBytes            int
	BuiltAt                 time.Time
}

// AggregationQueueMetric samples the leader queue even while service/PBFT is busy.
type AggregationQueueMetric struct {
	ObservedAt              time.Time
	FrozenWindows           int
	QueuedWindows           int
	PendingPayments         int
	QueuedOriginals         int
	FrozenQueueWait         time.Duration
	ActiveWindowID          uint64
	PolicyVersion           uint64
	AggregationDestinations int
}

type AggregationBeaconMetric struct {
	SupervisorNotifyTime   time.Duration
	HeaderBroadcastTime    time.Duration
	PackageDispatchTime    time.Duration
	CommitDeliveryTime     time.Duration
	BatchID                merkle.Hash
	WindowID               uint64
	ValidationTime         time.Duration
	BeaconConsensusLatency time.Duration
	PreprepareBytes        int
	PrepareBytes           int
	CommitBytes            int
	BatchStorageBytes      int
	FinalizedAt            time.Time
}

type AggregationExecutionMetric struct {
	Phase                 string
	PaymentID             paymentmodel.ID
	BatchID               merkle.Hash
	WindowID              uint64
	ShardID               int64
	ChunkIndex            uint32
	ItemCount             int
	ReceiverUpdateCount   int
	UniqueAccountCount    int
	CreatedAt             time.Time
	CommittedAt           time.Time
	StateReadCount        int64
	StateWriteCount       int64
	ProofVerificationTime time.Duration
}

type SettlementTxMetric struct {
	BatchID     merkle.Hash
	WindowID    uint64
	ShardID     int64
	ChunkIndex  uint32
	CreditCount int
}
