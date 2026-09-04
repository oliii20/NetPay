package message

import "github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"

const (
	FinalizedBlockReceiptMessageType = "FinalizedBlockReceipt"
	BatchProposalMessageType         = "BatchProposal"
)

type FinalizedBlockReceiptMsg struct {
	NodeID  int64
	Receipt model.FinalizedBlockReceipt
}

type BatchProposalMsg struct {
	Proposal model.BatchProposal
}
