package message

import (
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/routing"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
)

const (
	AggregationQueueMetricMessageType     = "AggregationQueueMetric"
	AggregationRoutingPolicyMessageType   = "AggregationRoutingPolicy"
	FinalizedBlockReceiptMessageType      = "FinalizedBlockReceipt"
	AggregationFinalizedMessageType       = "AggregationFinalized"
	AggregationPackageMessageType         = "AggregationPackage"
	AggregationBatchMetricMessageType     = "AggregationBatchMetric"
	AggregationBeaconMetricMessageType    = "AggregationBeaconMetric"
	AggregationExecutionMetricMessageType = "AggregationExecutionMetric"
	AggregationAppliedMessageType         = "AggregationApplied"
)

type AggregationQueueMetricMsg struct {
	NodeID int64
	Metric model.AggregationQueueMetric
}

type AggregationRoutingPolicyMsg struct {
	NodeID  int64
	ShardID int64
	Policy  routing.Policy
}

type FinalizedBlockReceiptMsg struct {
	NodeID  int64
	Receipt model.FinalizedBlockReceipt
}

type PaymentSettlement struct {
	PaymentID  paymentmodel.ID
	ShardID    int64
	ChunkIndex uint32
}

type AggregationFinalizedMsg struct {
	Settlements []PaymentSettlement
	NodeID      int64
	Header      model.AggregationBatchHeader
	PaymentIDs  []paymentmodel.ID
}

type AggregationPackageMsg struct {
	NodeID  int64
	Package model.AggregationPackage
}

type AggregationBatchMetricMsg struct {
	NodeID int64
	Metric model.AggregationBatchMetric
}

type AggregationBeaconMetricMsg struct {
	NodeID int64
	Metric model.AggregationBeaconMetric
}

type AggregationExecutionMetricMsg struct {
	NodeID        int64
	Metrics       []model.AggregationExecutionMetric
	SettlementTxs []model.SettlementTxMetric
}

// AggregationApplied identifies settlement chunks durably committed by a
// destination shard. It is protocol progress, not an optional metric.
type AggregationApplied struct {
	BatchID      merkle.Hash
	WindowID     uint64
	ShardID      int64
	ChunkIndex   uint32
	ReceiptCount int
	CommittedAt  time.Time
}

type AggregationAppliedMsg struct {
	NodeID int64
	Chunks []AggregationApplied
}
