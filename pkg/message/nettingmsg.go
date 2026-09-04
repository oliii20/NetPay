package message

import "github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"

const FinalizedBlockReceiptMessageType = "FinalizedBlockReceipt"

type FinalizedBlockReceiptMsg struct {
	NodeID  int64
	Receipt model.FinalizedBlockReceipt
}
