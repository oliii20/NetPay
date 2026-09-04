package message

import "github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"

const (
	FinalizedBlockReceiptMessageType = "FinalizedBlockReceipt"
	BatchProposalMessageType         = "BatchProposal"
	MatchRootFinalizedMessageType    = "MatchRootFinalized"
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
