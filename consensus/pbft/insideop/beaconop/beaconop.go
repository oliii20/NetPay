// Package beaconop integrates receipt aggregation with the shared PBFT state machine.
package beaconop

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/config"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/batch"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/beacon"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/metrics"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/window"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

var (
	ErrInvalidReceiptSender = errors.New("finalized receipt is not from a shard leader")
	ErrInvalidProposal      = errors.New("invalid Beacon aggregation proposal")
	ErrMissingReceiptData   = errors.New("aggregation receipt data is unavailable")
	ErrAggregationHeader    = errors.New("reconstructed aggregation header mismatch")
	ErrConflictingReceipt   = errors.New("conflicting finalized receipt")
)

type Op struct {
	conn             *network.ConnHandler
	resolver         nodetopo.NodeMapper
	store            *beacon.Store
	validator        *beacon.Validator
	shardCount       int64
	nodeID           int64
	cfg              config.AggregationCfg
	manager          *window.Manager
	bootstrap        map[int64][]model.FinalizedBlockReceipt
	builder          batch.Builder
	metrics          *metrics.Publisher
	receivedAt       map[merkle.Hash]time.Time
	receiptHandledAt map[merkle.Hash]time.Time
	receipts         map[int64]map[uint64]model.FinalizedBlockReceipt
	prepared         map[merkle.Hash]model.AggregationBatch
}

func New(
	conn *network.ConnHandler,
	resolver nodetopo.NodeMapper,
	store *beacon.Store,
	shardCount, chainID, nodeID int64,
	cfg config.AggregationCfg,
) *Op {
	if cfg.AggregationOnly {
		cfg.CreditChunkSize = 1
	}
	return &Op{
		conn: conn, resolver: resolver, store: store,
		validator:  beacon.NewValidator(store, uint64(chainID), shardCount),
		shardCount: shardCount, nodeID: nodeID, cfg: cfg,
		bootstrap:        make(map[int64][]model.FinalizedBlockReceipt),
		builder:          batch.Builder{CreditChunkSize: cfg.CreditChunkSize, BatchOnly: cfg.BatchOnly},
		metrics:          metrics.NewPublisher(false, nodeID, conn, resolver),
		receivedAt:       make(map[merkle.Hash]time.Time),
		receiptHandledAt: make(map[merkle.Hash]time.Time),
		receipts:         make(map[int64]map[uint64]model.FinalizedBlockReceipt),
		prepared:         make(map[merkle.Hash]model.AggregationBatch),
	}
}

func (o *Op) EnableMetrics() {
	o.metrics = metrics.NewPublisher(true, o.nodeID, o.conn, o.resolver)
}

func (o *Op) HandleMsgOutsideShard(_ context.Context, wrapped *rpcserver.WrappedMsg) error {
	if wrapped.GetMsgType() != message.FinalizedBlockReceiptMessageType {
		return fmt.Errorf("unknown Beacon message type: %s", wrapped.GetMsgType())
	}

	handlerStarted := time.Now()
	var msg message.FinalizedBlockReceiptMsg
	if err := gob.NewDecoder(bytes.NewReader(wrapped.GetPayload())).Decode(&msg); err != nil {
		return fmt.Errorf("decode finalized receipt: %w", err)
	}

	if msg.NodeID != 0 {
		return fmt.Errorf("%w: node %d", ErrInvalidReceiptSender, msg.NodeID)
	}

	if msg.Receipt.ShardID < 0 || msg.Receipt.ShardID >= o.shardCount {
		return fmt.Errorf("invalid receipt shard: %d", msg.Receipt.ShardID)
	}
	if err := o.validator.ValidateReceipt(msg.Receipt); err != nil {
		return fmt.Errorf("validate finalized receipt: %w", err)
	}
	if err := o.recordReceipt(msg.Receipt); err != nil {
		return err
	}

	if _, exists := o.receiptHandledAt[msg.Receipt.BlockHash]; !exists {
		o.receiptHandledAt[msg.Receipt.BlockHash] = handlerStarted
	}
	if o.manager == nil {
		o.bootstrap[msg.Receipt.ShardID] = append(o.bootstrap[msg.Receipt.ShardID], msg.Receipt.Clone())
		if len(o.bootstrap) == int(o.shardCount) {
			return o.initializeManager()
		}

		return nil
	}

	if _, err := o.manager.AddReceipt(msg.Receipt); err != nil {
		return fmt.Errorf("add finalized receipt: %w", err)
	}

	return nil
}

func (o *Op) BuildProposal(context.Context) (*message.Proposal, error) {
	if o.manager == nil {
		return nil, nil
	}

	o.manager.TryClose()

	frozen, ok := o.manager.PeekFrozen()
	if !ok {
		return nil, nil
	}

	previous := merkle.Hash{}

	if tip, found, err := o.store.Tip(); err != nil {
		return nil, err
	} else if found {
		previous = tip.Body.BatchID
	}

	buildStarted := time.Now()
	proposal, metric, err := o.builder.BuildMeasured(*frozen, previous)
	if err != nil {
		return nil, fmt.Errorf("build aggregation window %d: %w", frozen.WindowID, err)
	}

	metric.BuildTime = time.Since(buildStarted)
	var ingressTotal, pendingTotal int64
	var measuredCount int64
	for _, receipt := range frozen.Receipts {
		handledAt, found := o.receiptHandledAt[receipt.BlockHash]
		if !found || receipt.CommitTime.IsZero() {
			continue
		}
		for _, payment := range receipt.Payments {
			ids, idErr := payment.LogicalIDs()
			if idErr != nil {
				return nil, idErr
			}
			count := int64(len(ids))
			ingressTotal += handledAt.Sub(receipt.CommitTime).Nanoseconds() * count
			pendingTotal += buildStarted.Sub(handledAt).Nanoseconds() * count
			measuredCount += count
		}
	}
	metric.ReceiptTimingCount = measuredCount
	if measuredCount > 0 {
		metric.SourceToHandlerMean = time.Duration(ingressTotal / measuredCount)
		metric.HandlerToBuildMean = time.Duration(pendingTotal / measuredCount)
	}
	metric.WindowOpenedAt = frozen.OpenedAt
	metric.WindowSealedAt = frozen.SealedAt
	metric.BuildStartedAt = buildStarted
	metric.FrozenQueueWait = buildStarted.Sub(frozen.SealedAt)
	validationStarted := time.Now()
	if err = o.validator.Validate(proposal); err != nil {
		return nil, fmt.Errorf("validate local aggregation batch: %w", err)
	}

	metric.LocalValidationTime = time.Since(validationStarted)
	metric.FrozenWindowQueueLength = o.manager.FrozenWindowCount()
	metric.BatchProposalBytes, _ = encodedSize(message.WrapAggregationHeaderProposal(proposal.Header))

	metric.SidecarBytes, _ = encodedSize(proposal.Sidecar)
	metric.ProposalReadyAt = time.Now()
	if err = o.metrics.PublishBatch(context.Background(), metric); err != nil {
		slog.Warn("publish aggregation batch metric failed", "err", err)
	}

	o.receivedAt[proposal.Header.BatchID] = time.Now()
	o.prepared[proposal.Header.BatchID] = proposal.Clone()

	return message.WrapAggregationHeaderProposal(proposal.Header), nil
}

func (o *Op) ValidateProposal(_ context.Context, proposal *message.Proposal) error {
	if proposal == nil || proposal.Block != nil || proposal.AggregationHeader == nil || proposal.AggregationBatch != nil {
		return ErrInvalidProposal
	}

	header := *proposal.AggregationHeader
	reconstructed, ok := o.prepared[header.BatchID]
	if !ok || !equalAggregationHeaders(reconstructed.Header, header) {
		var err error
		reconstructed, err = o.reconstructBatch(header)
		if err != nil {
			return err
		}
	}
	if err := o.validator.Validate(reconstructed); err != nil {
		return fmt.Errorf("validate Beacon aggregation proposal: %w", err)
	}
	o.prepared[header.BatchID] = reconstructed.Clone()

	return nil
}

func (o *Op) ProposalCommitAndDeliver(ctx context.Context, isLeader bool, proposal *message.Proposal) error {
	if proposal == nil || proposal.Block != nil || proposal.AggregationHeader == nil || proposal.AggregationBatch != nil {
		return ErrInvalidProposal
	}
	header := *proposal.AggregationHeader
	prepared, ok := o.prepared[header.BatchID]
	if !ok || !equalAggregationHeaders(prepared.Header, header) {
		return ErrMissingReceiptData
	}

	committed, err := o.store.Commit(prepared, time.Now())
	if err != nil {
		return fmt.Errorf("commit aggregation block: %w", err)
	}

	if o.manager != nil {
		if frozen, ok := o.manager.PeekFrozen(); ok && frozen.WindowID == committed.Body.WindowID {
			if err = o.manager.AcknowledgeFrozen(frozen.WindowID); err != nil {
				return err
			}
		}
	}

	for _, receipt := range prepared.Sidecar.Receipts {
		delete(o.receiptHandledAt, receipt.BlockHash)
	}
	o.pruneReceipts(prepared.Header.Cuts)

	if !isLeader {
		delete(o.prepared, committed.Body.BatchID)
		return nil
	}

	paymentIDs := batchPaymentIDs(prepared)

	finalized, err := message.WrapMsg(&message.AggregationFinalizedMsg{
		NodeID: o.nodeID, Header: committed.Body, PaymentIDs: paymentIDs, Settlements: paymentSettlements(prepared),
	})
	if err != nil {
		return err
	}

	dispatchStarted := time.Now()
	// Only the measurement consumer needs the original transaction mapping.
	supervisor, err := o.resolver.GetSupervisor()
	if err != nil {
		return fmt.Errorf("resolve aggregation supervisor: %w", err)
	}
	o.conn.SendMsg2Dest(ctx, supervisor, finalized)
	supervisorDuration := time.Since(dispatchStarted)
	headerOnly, err := message.WrapMsg(&message.AggregationFinalizedMsg{
		NodeID: o.nodeID, Header: committed.Body,
	})
	if err != nil {
		return err
	}
	headerStarted := time.Now()
	if err = o.broadcastFinalized(ctx, headerOnly); err != nil {
		return err
	}

	headerDuration := time.Since(headerStarted)
	packageStarted := time.Now()
	if err = o.sendAggregationPackages(ctx, prepared); err != nil {
		return err
	}

	packageDuration := time.Since(packageStarted)
	metric, metricErr := o.buildMetric(prepared, committed)
	metric.SupervisorNotifyTime = supervisorDuration
	metric.HeaderBroadcastTime = headerDuration
	metric.PackageDispatchTime = packageDuration
	metric.CommitDeliveryTime = time.Since(dispatchStarted)
	if metricErr != nil {
		slog.WarnContext(ctx, "build Beacon aggregation metric failed", "err", metricErr)
	} else if metricErr = o.metrics.PublishBeacon(ctx, metric); metricErr != nil {
		slog.WarnContext(ctx, "publish Beacon aggregation metric failed", "err", metricErr)
	}

	delete(o.receivedAt, committed.Body.BatchID)
	delete(o.prepared, committed.Body.BatchID)

	return nil
}

func (o *Op) recordReceipt(receipt model.FinalizedBlockReceipt) error {
	byHeight := o.receipts[receipt.ShardID]
	if byHeight == nil {
		byHeight = make(map[uint64]model.FinalizedBlockReceipt)
		o.receipts[receipt.ShardID] = byHeight
	}
	if existing, ok := byHeight[receipt.Height]; ok {
		if !reflect.DeepEqual(existing, receipt) {
			return fmt.Errorf("%w: shard %d height %d", ErrConflictingReceipt, receipt.ShardID, receipt.Height)
		}
		return nil
	}
	byHeight[receipt.Height] = receipt.Clone()
	return nil
}

func (o *Op) reconstructBatch(header model.AggregationBatchHeader) (model.AggregationBatch, error) {
	receipts := make([]model.FinalizedBlockReceipt, 0)
	payments := make([]paymentmodel.CrossShardPayment, 0)
	for _, cut := range header.Cuts {
		if cut.ShardID < 0 || cut.ShardID >= o.shardCount || cut.EndHeight < cut.PreviousHeight {
			return model.AggregationBatch{}, ErrInvalidProposal
		}
		byHeight := o.receipts[cut.ShardID]
		for height := cut.PreviousHeight + 1; height <= cut.EndHeight; height++ {
			receipt, ok := byHeight[height]
			if !ok {
				return model.AggregationBatch{}, fmt.Errorf(
					"%w: shard %d height %d", ErrMissingReceiptData, cut.ShardID, height,
				)
			}
			receipts = append(receipts, receipt.Clone())
			for _, payment := range receipt.Payments {
				payments = append(payments, payment.Clone())
			}
		}
	}
	sort.Slice(receipts, func(i, j int) bool {
		if receipts[i].ShardID != receipts[j].ShardID {
			return receipts[i].ShardID < receipts[j].ShardID
		}
		return receipts[i].Height < receipts[j].Height
	})
	type paymentWithID struct {
		id      paymentmodel.ID
		payment paymentmodel.CrossShardPayment
	}
	ordered := make([]paymentWithID, len(payments))
	for idx, payment := range payments {
		id, err := payment.ID()
		if err != nil {
			return model.AggregationBatch{}, err
		}
		ordered[idx] = paymentWithID{id: id, payment: payment}
	}
	sort.Slice(ordered, func(i, j int) bool { return bytes.Compare(ordered[i].id[:], ordered[j].id[:]) < 0 })
	for idx := range ordered {
		payments[idx] = ordered[idx].payment
	}

	reconstructed, err := o.builder.Build(window.FrozenWindow{
		WindowID: header.WindowID, Cuts: append([]model.ShardCut(nil), header.Cuts...),
		Receipts: receipts, Payments: payments,
	}, header.PreviousBatchID)
	if err != nil {
		return model.AggregationBatch{}, fmt.Errorf("rebuild compact aggregation proposal: %w", err)
	}
	if !equalAggregationHeaders(reconstructed.Header, header) {
		return model.AggregationBatch{}, ErrAggregationHeader
	}
	return reconstructed, nil
}

func equalAggregationHeaders(left, right model.AggregationBatchHeader) bool {
	return reflect.DeepEqual(left, right)
}

func (o *Op) pruneReceipts(cuts []model.ShardCut) {
	for _, cut := range cuts {
		byHeight := o.receipts[cut.ShardID]
		for height := range byHeight {
			if height <= cut.EndHeight {
				delete(byHeight, height)
			}
		}
	}
}

func (o *Op) initializeManager() error {
	checkpoints := make(map[int64]model.ShardCheckpoint, o.shardCount)

	tip, hasTip, err := o.store.Tip()
	if err != nil {
		return fmt.Errorf("read Beacon tip while restoring receipt window: %w", err)
	}

	if hasTip {
		for _, cut := range tip.Body.Cuts {
			checkpoints[cut.ShardID] = model.ShardCheckpoint{Height: cut.EndHeight, BlockHash: cut.EndBlockHash}
		}

		if len(checkpoints) != int(o.shardCount) {
			return fmt.Errorf("beacon tip has %d shard cuts, want %d", len(checkpoints), o.shardCount)
		}
	}

	all := make([]model.FinalizedBlockReceipt, 0)

	for shardID := range o.shardCount {
		receipts := o.bootstrap[shardID]
		sort.Slice(receipts, func(i, j int) bool { return receipts[i].Height < receipts[j].Height })

		if len(receipts) == 0 {
			return fmt.Errorf("invalid bootstrap receipt for shard %d", shardID)
		}

		if !hasTip {
			if receipts[0].Height == 0 {
				return fmt.Errorf("invalid bootstrap receipt height for shard %d", shardID)
			}

			checkpoints[shardID] = model.ShardCheckpoint{
				Height:    receipts[0].Height - 1,
				BlockHash: receipts[0].ParentHash,
			}
		}

		all = append(all, receipts...)
	}

	initialWindowID := uint64(1)
	if hasTip {
		initialWindowID = tip.Body.WindowID + 1
	}

	manager, err := window.New(window.Config{
		ShardCount: o.shardCount, BatchSize: o.cfg.BatchSize,
		MaxWindowDuration: time.Duration(o.cfg.MaxWindowDurationMS) * time.Millisecond,
		InitialWindowID:   initialWindowID,
	}, nil, checkpoints)
	if err != nil {
		return err
	}

	o.manager = manager
	o.bootstrap = make(map[int64][]model.FinalizedBlockReceipt)

	sort.Slice(all, func(i, j int) bool {
		if all[i].Height != all[j].Height {
			return all[i].Height < all[j].Height
		}

		return all[i].ShardID < all[j].ShardID
	})

	for _, receipt := range all {
		if _, err = o.manager.AddReceipt(receipt); err != nil {
			return err
		}
	}

	return nil
}

func (o *Op) sendAggregationPackages(ctx context.Context, proposal model.AggregationBatch) error {
	packages, err := batch.BuildAggregationPackages(proposal)
	if err != nil {
		return err
	}

	for _, pack := range packages {
		leader, resolveErr := o.resolver.GetLeader(pack.Aggregation.ShardID)
		if resolveErr != nil {
			return resolveErr
		}

		wrapped, wrapErr := message.WrapMsg(&message.AggregationPackageMsg{NodeID: o.nodeID, Package: pack})
		if wrapErr != nil {
			return wrapErr
		}

		o.conn.SendMsg2Dest(ctx, leader, wrapped)
	}

	return nil
}

func (o *Op) broadcastFinalized(ctx context.Context, wrapped *rpcserver.WrappedMsg) error {
	destinations := make([]nodetopo.NodeInfo, 0)

	for shardID := range o.shardCount {
		nodes, err := o.resolver.GetNodesInShard(shardID)
		if err != nil {
			return err
		}

		destinations = append(destinations, nodes...)
	}

	o.conn.GroupBroadcastMessage(ctx, destinations, wrapped)

	return nil
}

func (o *Op) buildMetric(
	proposal model.AggregationBatch,
	committed beacon.AggregationBlock,
) (model.AggregationBeaconMetric, error) {
	headerProposal := message.WrapAggregationHeaderProposal(proposal.Header)
	digest, err := headerProposal.Hash()
	if err != nil {
		return model.AggregationBeaconMetric{}, err
	}

	preprepareBytes, err := messageSize(
		&message.PreprepareMsg{P: *headerProposal, Digest: digest},
	)
	if err != nil {
		return model.AggregationBeaconMetric{}, err
	}

	prepareBytes, _ := messageSize(&message.PrepareMsg{Digest: digest, ShardID: nodetopo.BeaconShardID})
	commitBytes, _ := messageSize(&message.CommitMsg{Digest: digest, ShardID: nodetopo.BeaconShardID})

	nodes, err := o.resolver.GetNodesInShard(nodetopo.BeaconShardID)
	if err != nil {
		return model.AggregationBeaconMetric{}, err
	}

	storageBytes, err := encodedSize(committed)
	if err != nil {
		return model.AggregationBeaconMetric{}, err
	}

	nodeCount := len(nodes)

	latency := time.Duration(0)
	if received := o.receivedAt[committed.Body.BatchID]; !received.IsZero() {
		latency = committed.CommitTime.Sub(received)
	}

	return model.AggregationBeaconMetric{
		BatchID: committed.Body.BatchID, WindowID: committed.Body.WindowID,
		BeaconConsensusLatency: latency, PreprepareBytes: preprepareBytes * nodeCount,
		PrepareBytes: prepareBytes * nodeCount * nodeCount, CommitBytes: commitBytes * nodeCount * nodeCount,
		BatchStorageBytes: storageBytes, FinalizedAt: committed.CommitTime,
	}, nil
}

func batchPaymentIDs(proposal model.AggregationBatch) []paymentmodel.ID {
	ids := make([]paymentmodel.ID, 0)

	for _, receipt := range proposal.Sidecar.Receipts {
		for _, payment := range receipt.Payments {
			if logicalIDs, err := payment.LogicalIDs(); err == nil {
				ids = append(ids, logicalIDs...)
			}
		}
	}

	sort.Slice(ids, func(i, j int) bool { return bytes.Compare(ids[i][:], ids[j][:]) < 0 })

	return ids
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

// paymentSettlements maps each payment to its actual destination commit unit.
func paymentSettlements(proposal model.AggregationBatch) []message.PaymentSettlement {
	type key struct {
		shard     int64
		recipient [20]byte
		asset     paymentmodel.AssetID
		root      merkle.Hash
	}
	chunks := make(map[key]uint32)
	for _, chunk := range proposal.Sidecar.ShardAggregations {
		for _, credit := range chunk.Credits {
			k := key{shard: chunk.ShardID, recipient: [20]byte(credit.Recipient), asset: credit.AssetID}
			if chunk.BatchOnly {
				k.root = credit.ReceiptRoot
			}
			chunks[k] = chunk.ChunkIndex
		}
	}
	result := make([]message.PaymentSettlement, 0)
	for _, receipt := range proposal.Sidecar.Receipts {
		for _, payment := range receipt.Payments {
			ids, err := payment.LogicalIDs()
			if err != nil {
				continue
			} // Already validated by Beacon consensus.
			k := key{shard: payment.DestinationShard, recipient: [20]byte(payment.Recipient), asset: payment.AssetID}
			if proposal.Header.BatchOnly {
				tree, err := merkle.Build(batch.CreditReceiptLeafDomain, []merkle.Leaf{{Key: ids[0][:], Payload: ids[0][:]}})
				if err != nil {
					continue
				}
				k.root = tree.Root()
			}
			chunk, ok := chunks[k]
			if !ok {
				continue
			}
			for _, id := range ids {
				result = append(result, message.PaymentSettlement{PaymentID: id, ShardID: k.shard, ChunkIndex: chunk})
			}
		}
	}
	return result
}
