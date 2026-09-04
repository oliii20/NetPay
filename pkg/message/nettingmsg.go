package message

import (
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
)

const (
	FinalizedBlockReceiptMessageType  = "FinalizedBlockReceipt"
	BatchProposalMessageType          = "BatchProposal"
	MatchRootFinalizedMessageType     = "MatchRootFinalized"
	SettlementPackageMessageType      = "SettlementPackage"
	FallbackTxMessageType             = "FallbackTx"
	FallbackCompletedMessageType      = "FallbackCompleted"
	NettingBatchMetricMessageType     = "NettingBatchMetric"
	NettingBeaconMetricMessageType    = "NettingBeaconMetric"
	NettingExecutionMetricMessageType = "NettingExecutionMetric"
	NettingProgressMessageType        = "NettingProgress"
)

type FinalizedBlockReceiptMsg struct {
	NodeID  int64
	Receipt model.FinalizedBlockReceipt
}

type BatchProposalMsg struct {
	NodeID   int64
	Proposal model.BatchProposal
}

type MatchRootFinalizedMsg struct {
	NodeID int64
	Header model.MatchRootBlockBody
}

type SettlementPackageMsg struct {
	NodeID  int64
	Package model.SettlementPackage
}

type FallbackTxMsg struct {
	NodeID   int64
	Fallback model.ReservedFallback
}

type FallbackCompletedMsg struct {
	NodeID      int64
	SourceShard int64
	Key         model.FallbackKey
}

type NettingBatchMetricMsg struct {
	NodeID int64
	Metric model.NettingBatchMetric
}

type NettingBeaconMetricMsg struct {
	NodeID int64
	Metric model.NettingBeaconMetric
}

type NettingExecutionMetricMsg struct {
	NodeID  int64
	Metrics []model.NettingExecutionMetric
}

type NettingProgressMsg struct {
	NodeID    int64
	IntentIDs []intent.ID
}
