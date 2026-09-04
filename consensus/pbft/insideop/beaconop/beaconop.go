// Package beaconop adapts netting batches to the shared PBFT state machine.
package beaconop

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/beacon"
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
	conn       *network.ConnHandler
	resolver   nodetopo.NodeMapper
	store      *beacon.Store
	validator  *beacon.Validator
	shardCount int64
	nodeID     int64
	queue      []model.BatchProposal
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
	}
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

	return nil
}

func (o *Op) BuildProposal(context.Context) (*message.Proposal, error) {
	if len(o.queue) == 0 {
		return nil, nil
	}
	if err := o.validator.Validate(o.queue[0]); err != nil {
		return nil, fmt.Errorf("validate queued Beacon batch: %w", err)
	}

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

	return nil
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
