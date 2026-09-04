package measure

import (
	"errors"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
)

type Router struct {
	legacy  Measure
	netting Measure
}

func NewRouter(legacy, netting Measure) *Router {
	return &Router{legacy: legacy, netting: netting}
}

func (r *Router) UpdateMeasureRecord(wrapped *rpcserver.WrappedMsg) error {
	switch wrapped.GetMsgType() {
	case message.NettingBatchMetricMessageType,
		message.NettingBeaconMetricMessageType,
		message.NettingExecutionMetricMessageType:
		return r.netting.UpdateMeasureRecord(wrapped)
	case message.FinalizedBlockReceiptMessageType,
		message.BatchProposalMessageType,
		message.MatchRootFinalizedMessageType,
		message.SettlementPackageMessageType,
		message.FallbackTxMessageType,
		message.FallbackCompletedMessageType:
		return nil
	default:
		return r.legacy.UpdateMeasureRecord(wrapped)
	}
}

func (r *Router) OutputResultAndClose() error {
	return errors.Join(r.legacy.OutputResultAndClose(), r.netting.OutputResultAndClose())
}
