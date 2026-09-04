// Package beaconop adapts netting batches to the shared PBFT state machine.
package beaconop

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/beacon"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/metrics"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

var (
	ErrInvalidBatchSender = errors.New("batch proposal is not from Solver")
	ErrInvalidProposal    = errors.New("invalid Beacon PBFT proposal")
)

type Op struct {
	conn           *network.ConnHandler
	resolver       nodetopo.NodeMapper
	store          *beacon.Store
	validator      *beacon.Validator
	shardCount     int64
	nodeID         int64
	queue          []model.BatchProposal
	metrics        *metrics.Publisher
	receivedAt     map[[32]byte]time.Time
	validationTime map[[32]byte]time.Duration
}

func New(
	conn *network.ConnHandler,
	resolver nodetopo.NodeMapper,
	store *beacon.Store,
	shardCount, nodeID int64,
) *Op {
	return &Op{
		conn: conn, resolver: resolver, store: store, validator: beacon.NewValidator(store),
		shardCount: shardCount, nodeID: nodeID,
		metrics:    metrics.NewPublisher(false, nodeID, conn, resolver),
		receivedAt: make(map[[32]byte]time.Time), validationTime: make(map[[32]byte]time.Duration),
	}
}

func (o *Op) EnableMetrics() {
	o.metrics = metrics.NewPublisher(true, o.nodeID, o.conn, o.resolver)
}

func (o *Op) HandleMsgOutsideShard(_ context.Context, wrapped *rpcserver.WrappedMsg) error {
	if wrapped.GetMsgType() != message.BatchProposalMessageType {
		return fmt.Errorf("unknown Beacon message type: %s", wrapped.GetMsgType())
	}
	var msg message.BatchProposalMsg
	if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(&msg); err != nil {
		return fmt.Errorf("decode BatchProposal: %w", err)
	}
	if msg.NodeID != 0 {
		return fmt.Errorf("%w: node %d", ErrInvalidBatchSender, msg.NodeID)
	}
	for _, queued := range o.queue {
		if queued.Header.BatchID == msg.Proposal.Header.BatchID {
			return nil
		}
	}
	o.queue = append(o.queue, msg.Proposal.Clone())
	o.receivedAt[msg.Proposal.Header.BatchID] = time.Now()

	return nil
}

func (o *Op) BuildProposal(context.Context) (*message.Proposal, error) {
	if len(o.queue) == 0 {
		return nil, nil
	}
	started := time.Now()
	if err := o.validator.Validate(o.queue[0]); err != nil {
		return nil, fmt.Errorf("validate queued Beacon batch: %w", err)
	}
	o.validationTime[o.queue[0].Header.BatchID] = time.Since(started)

	return message.WrapNettingProposal(o.queue[0]), nil
}

func (o *Op) ValidateProposal(_ context.Context, proposal *message.Proposal) error {
	if proposal == nil || proposal.Block != nil || proposal.NettingBatch == nil {
		return ErrInvalidProposal
	}
	if err := o.validator.Validate(*proposal.NettingBatch); err != nil {
		return fmt.Errorf("validate Beacon proposal: %w", err)
	}

	return nil
}

func (o *Op) ProposalCommitAndDeliver(
	ctx context.Context,
	isLeader bool,
	proposal *message.Proposal,
) error {
	if proposal == nil || proposal.NettingBatch == nil {
		return ErrInvalidProposal
	}
	committed, err := o.store.Commit(*proposal.NettingBatch, time.Now())
	if err != nil {
		return fmt.Errorf("commit MatchRoot block: %w", err)
	}
	o.removeQueued(committed.Body.BatchID)
	if !isLeader {
		return nil
	}
	wrapped, err := message.WrapMsg(&message.MatchRootFinalizedMsg{NodeID: o.nodeID, Header: committed.Body})
	if err != nil {
		return fmt.Errorf("wrap finalized MatchRoot: %w", err)
	}
	if err = o.broadcastFinalized(ctx, wrapped); err != nil {
		return err
	}
	if metric, metricErr := o.buildMetric(*proposal.NettingBatch, committed); metricErr != nil {
		slog.WarnContext(ctx, "build Beacon metrics failed", "err", metricErr)
	} else if metricErr = o.metrics.PublishBeacon(ctx, metric); metricErr != nil {
		slog.WarnContext(ctx, "publish Beacon metrics failed", "err", metricErr)
	}
	delete(o.receivedAt, committed.Body.BatchID)
	delete(o.validationTime, committed.Body.BatchID)

	return nil
}

func (o *Op) buildMetric(
	proposal model.BatchProposal,
	committed beacon.MatchRootBlock,
) (model.NettingBeaconMetric, error) {
	digest, err := message.WrapNettingProposal(proposal).Hash()
	if err != nil {
		return model.NettingBeaconMetric{}, err
	}
	preprepareBytes, err := messageSize(&message.PreprepareMsg{P: *message.WrapNettingProposal(proposal), Digest: digest})
	if err != nil {
		return model.NettingBeaconMetric{}, err
	}
	prepareBytes, err := messageSize(&message.PrepareMsg{Digest: digest, ShardID: nodetopo.BeaconShardID})
	if err != nil {
		return model.NettingBeaconMetric{}, err
	}
	commitBytes, err := messageSize(&message.CommitMsg{Digest: digest, ShardID: nodetopo.BeaconShardID})
	if err != nil {
		return model.NettingBeaconMetric{}, err
	}
	nodes, err := o.resolver.GetNodesInShard(nodetopo.BeaconShardID)
	if err != nil {
		return model.NettingBeaconMetric{}, err
	}
	nodeCount := len(nodes)
	blockBytes, err := encodedSize(committed)
	if err != nil {
		return model.NettingBeaconMetric{}, err
	}
	consensusLatency := time.Duration(0)
	if received := o.receivedAt[committed.Body.BatchID]; !received.IsZero() {
		consensusLatency = committed.CommitTime.Sub(received)
	}

	return model.NettingBeaconMetric{
		BatchID: committed.Body.BatchID, WindowID: committed.Body.WindowID,
		ValidationTime: o.validationTime[committed.Body.BatchID], BeaconConsensusLatency: consensusLatency,
		PreprepareBytes:       preprepareBytes * nodeCount,
		PrepareBytes:          prepareBytes * nodeCount * nodeCount,
		CommitBytes:           commitBytes * nodeCount * nodeCount,
		MatchRootStorageBytes: blockBytes, FinalizedAt: committed.CommitTime,
	}, nil
}

func messageSize(value any) (int, error) {
	wrapper, err := message.WrapMsg(value)
	if err != nil {
		return 0, err
	}

	return len(wrapper.GetMsgType()) + len(wrapper.GetPayload()), nil
}

func encodedSize(value any) (int, error) {
	var out bytes.Buffer
	if err := gob.NewEncoder(&out).Encode(value); err != nil {
		return 0, err
	}

	return out.Len(), nil
}

func (o *Op) removeQueued(batchID [32]byte) {
	for idx := range o.queue {
		if o.queue[idx].Header.BatchID == batchID {
			o.queue = append(o.queue[:idx], o.queue[idx+1:]...)
			return
		}
	}
}

func (o *Op) broadcastFinalized(ctx context.Context, wrapped *rpcserver.WrappedMsg) error {
	destinations := make([]nodetopo.NodeInfo, 0)
	for shardID := range o.shardCount {
		nodes, err := o.resolver.GetNodesInShard(shardID)
		if err != nil {
			return fmt.Errorf("resolve shard %d nodes: %w", shardID, err)
		}
		destinations = append(destinations, nodes...)
	}
	if solverNode, err := o.resolver.GetSolver(); err == nil {
		destinations = append(destinations, solverNode)
	}
	if supervisorNode, err := o.resolver.GetSupervisor(); err == nil {
		destinations = append(destinations, supervisorNode)
	}
	o.conn.GroupBroadcastMessage(ctx, destinations, wrapped)

	return nil
}
