package measure

import (
	"errors"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
)

type Router struct {
	legacy      Measure
	aggregation Measure
}

func NewRouter(legacy, aggregation Measure) *Router {
	return &Router{legacy: legacy, aggregation: aggregation}
}

func (r *Router) UpdateMeasureRecord(wrapped *rpcserver.WrappedMsg) error {
	switch wrapped.GetMsgType() {
	case message.AggregationRoutingPolicyMessageType:
		return r.aggregation.UpdateMeasureRecord(wrapped)
	case message.AggregationBatchMetricMessageType,
		message.AggregationBeaconMetricMessageType,
		message.AggregationExecutionMetricMessageType,
		message.AggregationFinalizedMessageType,
		message.AggregationAppliedMessageType:
		return r.aggregation.UpdateMeasureRecord(wrapped)
	case message.FinalizedBlockReceiptMessageType,
		message.AggregationPackageMessageType:
		return nil
	default:
		return r.legacy.UpdateMeasureRecord(wrapped)
	}
}

func (r *Router) OutputResultAndClose() error {
	return errors.Join(r.legacy.OutputResultAndClose(), r.aggregation.OutputResultAndClose())
}
