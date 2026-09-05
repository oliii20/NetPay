// Package solver implements the single-goroutine receipt collector and batch
// builder used by the off-chain netting Solver process.
package solver

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/batchstore"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/matcher"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/metrics"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/window"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

var (
	ErrInvalidConfig       = errors.New("invalid solver configuration")
	ErrInvalidReceiptNode  = errors.New("receipt is not from shard leader")
	ErrInvalidReceiptShard = errors.New("receipt has invalid shard")
	errStopped             = errors.New("solver stopped")
)

type Config struct {
	ShardCount        int64
	BatchSize         int
	MaxWindowDuration time.Duration
	TickInterval      time.Duration
	MetricsEnabled    bool
	MatcherMode       matcher.Mode
}

type Node struct {
	cfg        Config
	conn       *network.ConnHandler
	resolver   nodetopo.NodeMapper
	store      *batchstore.Store
	manager    *window.Manager
	bootstrap  map[int64][]model.FinalizedBlockReceipt
	chainTip   merkle.Hash
	pending    []merkle.Hash
	dispatched map[merkle.Hash]struct{}
	builder    batch.Builder
	metrics    *metrics.Publisher
	stopped    bool
}

func New(
	cfg Config,
	conn *network.ConnHandler,
	resolver nodetopo.NodeMapper,
	store *batchstore.Store,
) (*Node, error) {
	if cfg.ShardCount <= 0 || cfg.BatchSize <= 0 || cfg.MaxWindowDuration <= 0 || cfg.TickInterval <= 0 {
		return nil, ErrInvalidConfig
	}
	if conn == nil || resolver == nil || store == nil {
		return nil, fmt.Errorf("%w: missing dependency", ErrInvalidConfig)
	}
	node := &Node{
		cfg:        cfg,
		conn:       conn,
		resolver:   resolver,
		store:      store,
		bootstrap:  make(map[int64][]model.FinalizedBlockReceipt),
		dispatched: make(map[merkle.Hash]struct{}),
		builder:    batch.Builder{MatcherMode: cfg.MatcherMode},
		metrics:    metrics.NewPublisher(cfg.MetricsEnabled, 0, conn, resolver),
	}
	state, found, err := store.LoadState()
	if err != nil {
		return nil, err
	}
	if found {
		if err = node.restore(state); err != nil {
			return nil, fmt.Errorf("restore solver: %w", err)
		}
	}

	return node, nil
}

func (n *Node) Start(ctx context.Context) error {
	ticker := time.NewTicker(n.cfg.TickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := n.Step(ctx); err != nil {
				if errors.Is(err, errStopped) {
					return nil
				}
				slog.ErrorContext(ctx, "solver step failed", "err", err)
			}
		}
	}
}

func (n *Node) Step(ctx context.Context) error {
	for _, wrapped := range n.conn.DrainMsgBuffer() {
		if err := n.HandleMessage(ctx, wrapped); err != nil {
			return err
		}
	}
	if n.stopped {
		return errStopped
	}
	if n.manager != nil {
		n.manager.TryClose()
		if err := n.drainFrozen(ctx); err != nil {
			return err
		}
	}

	return n.dispatchPending(ctx)
}

func (n *Node) HandleMessage(ctx context.Context, wrapped *rpcserver.WrappedMsg) error {
	switch wrapped.GetMsgType() {
	case message.StopConsensusMessageType:
		n.stopped = true
		return nil
	case message.FinalizedBlockReceiptMessageType:
		var msg message.FinalizedBlockReceiptMsg
		if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(&msg); err != nil {
			return fmt.Errorf("decode finalized receipt: %w", err)
		}
		return n.HandleReceipt(msg)
	case message.MatchRootFinalizedMessageType:
		var msg message.MatchRootFinalizedMsg
		if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(&msg); err != nil {
			return fmt.Errorf("decode finalized MatchRoot: %w", err)
		}
		return n.HandleFinalized(ctx, msg)
	default:
		return nil
	}
}

func (n *Node) HandleReceipt(msg message.FinalizedBlockReceiptMsg) error {
	if msg.NodeID != 0 {
		return fmt.Errorf("%w: node %d", ErrInvalidReceiptNode, msg.NodeID)
	}
	if msg.Receipt.ShardID < 0 || msg.Receipt.ShardID >= n.cfg.ShardCount {
		return fmt.Errorf("%w: %d", ErrInvalidReceiptShard, msg.Receipt.ShardID)
	}
	if n.manager == nil {
		n.bootstrap[msg.Receipt.ShardID] = append(
			n.bootstrap[msg.Receipt.ShardID], msg.Receipt.Clone(),
		)
		if len(n.bootstrap) == int(n.cfg.ShardCount) {
			if err := n.initializeManager(); err != nil {
				return err
			}
		}
	} else if _, err := n.manager.AddReceipt(msg.Receipt); err != nil {
		return fmt.Errorf("add finalized receipt: %w", err)
	}
	if err := n.drainFrozen(context.Background()); err != nil {
		return err
	}

	return n.store.SaveState(n.snapshot())
}

func (n *Node) PendingBatchIDs() []merkle.Hash {
	return append([]merkle.Hash(nil), n.pending...)
}

func (n *Node) HandleFinalized(ctx context.Context, msg message.MatchRootFinalizedMsg) error {
	if msg.NodeID != 0 {
		return fmt.Errorf("finalized MatchRoot is not from Beacon leader: node %d", msg.NodeID)
	}
	if len(n.pending) == 0 || n.pending[0] != msg.Header.BatchID {
		return fmt.Errorf("unexpected finalized batch %s", msg.Header.BatchID.String())
	}
	proposal, err := n.store.GetProposal(msg.Header.BatchID)
	if err != nil {
		return err
	}
	if proposal.Header.BatchID != msg.Header.BatchID || proposal.Header.MatchRoot != msg.Header.MatchRoot {
		return fmt.Errorf("finalized header differs from submitted batch %s", msg.Header.BatchID.String())
	}
	if err = n.sendSettlementPackages(ctx, proposal); err != nil {
		return err
	}
	n.pending = n.pending[1:]
	delete(n.dispatched, msg.Header.BatchID)

	return n.store.SaveState(n.snapshot())
}

func (n *Node) sendSettlementPackages(ctx context.Context, proposal model.BatchProposal) error {
	packages, err := batch.BuildSettlementPackages(proposal)
	if err != nil {
		return fmt.Errorf("build settlement packages: %w", err)
	}
	for _, pack := range packages {
		leader, resolveErr := n.resolver.GetLeader(pack.Settlement.ShardID)
		if resolveErr != nil {
			return fmt.Errorf("resolve shard %d leader: %w", pack.Settlement.ShardID, resolveErr)
		}
		wrapped, wrapErr := message.WrapMsg(&message.SettlementPackageMsg{NodeID: 0, Package: pack})
		if wrapErr != nil {
			return fmt.Errorf("wrap shard %d settlement: %w", pack.Settlement.ShardID, wrapErr)
		}
		n.conn.SendMsg2Dest(ctx, leader, wrapped)
	}

	return nil
}

func (n *Node) initializeManager() error {
	checkpoints := make(map[int64]model.ShardCheckpoint, n.cfg.ShardCount)
	all := make([]model.FinalizedBlockReceipt, 0)
	for shardID := range n.cfg.ShardCount {
		receipts := n.bootstrap[shardID]
		sort.Slice(receipts, func(i, j int) bool { return receipts[i].Height < receipts[j].Height })
		first := receipts[0]
		if first.Height == 0 {
			return fmt.Errorf("bootstrap shard %d at height zero", shardID)
		}
		checkpoints[shardID] = model.ShardCheckpoint{
			Height: first.Height - 1, BlockHash: first.ParentHash,
		}
		all = append(all, receipts...)
	}
	manager, err := window.New(window.Config{
		ShardCount: n.cfg.ShardCount, BatchSize: n.cfg.BatchSize,
		MaxWindowDuration: n.cfg.MaxWindowDuration,
	}, nil, checkpoints)
	if err != nil {
		return fmt.Errorf("create window manager: %w", err)
	}
	n.manager = manager
	n.bootstrap = make(map[int64][]model.FinalizedBlockReceipt)
	sort.Slice(all, func(i, j int) bool {
		if all[i].Height != all[j].Height {
			return all[i].Height < all[j].Height
		}
		return all[i].ShardID < all[j].ShardID
	})
	for _, receipt := range all {
		if _, err = n.manager.AddReceipt(receipt); err != nil {
			return fmt.Errorf("add bootstrap receipt: %w", err)
		}
	}

	return nil
}

func (n *Node) drainFrozen(ctx context.Context) error {
	if n.manager == nil {
		return nil
	}
	for {
		frozen, ok := n.manager.PeekFrozen()
		if !ok {
			return nil
		}
		proposal, metric, err := n.builder.BuildMeasured(*frozen, n.chainTip)
		if err != nil {
			return fmt.Errorf("build window %d: %w", frozen.WindowID, err)
		}
		if err = n.manager.AcknowledgeFrozen(frozen.WindowID); err != nil {
			return err
		}
		n.chainTip = proposal.Header.BatchID
		n.pending = append(n.pending, proposal.Header.BatchID)
		if err = n.store.PutProposalAndState(proposal, n.snapshot()); err != nil {
			return err
		}
		metric.FrozenWindowQueueLength = n.manager.FrozenWindowCount()
		metric.BatchProposalBytes, err = encodedSize(message.BatchProposalMsg{NodeID: 0, Proposal: proposal})
		if err != nil {
			return fmt.Errorf("measure batch proposal bytes: %w", err)
		}
		metric.SidecarBytes, err = encodedSize(proposal.Sidecar)
		if err != nil {
			return fmt.Errorf("measure sidecar bytes: %w", err)
		}
		if err = n.metrics.PublishBatch(ctx, metric); err != nil {
			slog.WarnContext(ctx, "publish batch metrics failed", "err", err)
		}
	}
}

func encodedSize(value any) (int, error) {
	var out bytes.Buffer
	if err := gob.NewEncoder(&out).Encode(value); err != nil {
		return 0, err
	}

	return out.Len(), nil
}

func (n *Node) dispatchPending(ctx context.Context) error {
	if len(n.pending) == 0 {
		return nil
	}
	batchID := n.pending[0]
	if _, ok := n.dispatched[batchID]; ok {
		return nil
	}
	proposal, err := n.store.GetProposal(batchID)
	if err != nil {
		return err
	}
	wrapped, err := message.WrapMsg(&message.BatchProposalMsg{NodeID: 0, Proposal: proposal})
	if err != nil {
		return fmt.Errorf("wrap batch proposal: %w", err)
	}
	beaconLeader, err := n.resolver.GetLeader(nodetopo.BeaconShardID)
	if err != nil {
		return fmt.Errorf("resolve Beacon leader: %w", err)
	}
	n.conn.SendMsg2Dest(ctx, beaconLeader, wrapped)
	n.dispatched[batchID] = struct{}{}

	return nil
}

func (n *Node) restore(state batchstore.SolverState) error {
	if n.dispatched == nil {
		n.dispatched = make(map[merkle.Hash]struct{})
	}
	n.bootstrap = cloneBootstrap(state.BootstrapReceipts)
	n.chainTip = state.ChainTip
	n.pending = append([]merkle.Hash(nil), state.PendingBatchIDs...)
	if state.ManagerState != nil {
		manager, err := window.Restore(window.Config{
			ShardCount: n.cfg.ShardCount, BatchSize: n.cfg.BatchSize,
			MaxWindowDuration: n.cfg.MaxWindowDuration,
		}, nil, *state.ManagerState)
		if err != nil {
			return err
		}
		n.manager = manager
	}

	return nil
}

func (n *Node) snapshot() batchstore.SolverState {
	state := batchstore.SolverState{
		BootstrapReceipts: cloneBootstrap(n.bootstrap),
		ChainTip:          n.chainTip,
		PendingBatchIDs:   append([]merkle.Hash(nil), n.pending...),
	}
	if n.manager != nil {
		managerState := n.manager.Snapshot()
		state.ManagerState = &managerState
	}

	return state
}

func cloneBootstrap(input map[int64][]model.FinalizedBlockReceipt) map[int64][]model.FinalizedBlockReceipt {
	cloned := make(map[int64][]model.FinalizedBlockReceipt, len(input))
	for shardID, receipts := range input {
		cloned[shardID] = make([]model.FinalizedBlockReceipt, len(receipts))
		for idx := range receipts {
			cloned[shardID][idx] = receipts[idx].Clone()
		}
	}

	return cloned
}
