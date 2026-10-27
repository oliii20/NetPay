// Package receipt builds and publishes source-shard finalized block receipts.
package receipt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/message"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/nodetopo"
)

var (
	ErrNotTransactionBlock = errors.New("cannot build receipt for non-transaction block")
	ErrInvalidBlockHash    = errors.New("invalid block hash length")
	ErrInvalidParentHash   = errors.New("invalid parent hash length")
	ErrInvalidStateRoot    = errors.New("invalid state root length")
	ErrPaymentSource       = errors.New("receipt contains a payment from another source shard")
)

type Publisher struct {
	enabled                  bool
	sourceAggregateAbstracts bool
	nodeID                   int64
	sender                   network.P2PConn
	resolver                 nodetopo.NodeMapper
}

func NewPublisher(
	enabled bool,
	sourceAggregateAbstracts bool,
	nodeID int64,
	sender network.P2PConn,
	resolver nodetopo.NodeMapper,
) *Publisher {
	return &Publisher{
		enabled: enabled, sourceAggregateAbstracts: sourceAggregateAbstracts,
		nodeID: nodeID, sender: sender, resolver: resolver,
	}
}

func Build(shardID, epoch int64, b *block.Block, commitTime time.Time) (model.FinalizedBlockReceipt, error) {
	return build(shardID, epoch, b, commitTime, false)
}

func BuildWithSourceAggregation(
	shardID, epoch int64,
	b *block.Block,
	commitTime time.Time,
) (model.FinalizedBlockReceipt, error) {
	return build(shardID, epoch, b, commitTime, true)
}

func build(
	shardID, epoch int64,
	b *block.Block,
	commitTime time.Time,
	sourceAggregate bool,
) (model.FinalizedBlockReceipt, error) {
	if b.Type != block.TxBlockType {
		return model.FinalizedBlockReceipt{}, fmt.Errorf("%w: %d", ErrNotTransactionBlock, b.Type)
	}

	blockHashBytes, err := b.Hash()
	if err != nil {
		return model.FinalizedBlockReceipt{}, fmt.Errorf("calculate block hash: %w", err)
	}

	blockHash, err := toHash(blockHashBytes, ErrInvalidBlockHash)
	if err != nil {
		return model.FinalizedBlockReceipt{}, err
	}

	parentHash, err := toHash(b.ParentBlockHash, ErrInvalidParentHash)
	if err != nil {
		return model.FinalizedBlockReceipt{}, err
	}

	stateRoot, err := toHash(b.StateRoot, ErrInvalidStateRoot)
	if err != nil {
		return model.FinalizedBlockReceipt{}, err
	}

	payments := make([]paymentmodel.CrossShardPayment, 0)

	for idx := range b.TxList {
		tx := &b.TxList[idx]
		if tx.TxType() != transaction.CrossShardPaymentTxType {
			continue
		}

		if tx.Payment.SourceShard != shardID {
			return model.FinalizedBlockReceipt{}, fmt.Errorf(
				"%w: got %d, want %d",
				ErrPaymentSource,
				tx.Payment.SourceShard,
				shardID,
			)
		}

		if _, err = tx.Payment.ID(); err != nil {
			return model.FinalizedBlockReceipt{}, fmt.Errorf("calculate payment ID: %w", err)
		}

		payments = append(payments, tx.Payment.Clone())
	}

	if sourceAggregate {
		payments, err = AggregatePayments(payments)
		if err != nil {
			return model.FinalizedBlockReceipt{}, err
		}
	}

	return model.FinalizedBlockReceipt{
		ShardID:    shardID,
		Height:     b.Number,
		BlockHash:  blockHash,
		ParentHash: parentHash,
		StateRoot:  stateRoot,
		Epoch:      epoch,
		Payments:   payments,
		CommitTime: commitTime,
	}, nil
}

type aggregateKey struct {
	version          uint8
	chainID          uint64
	recipient        [20]byte
	sourceShard      int64
	destinationShard int64
	assetID          paymentmodel.AssetID
}

type aggregateValue struct {
	amount *big.Int
	ids    []paymentmodel.ID
}

// AggregatePayments compacts finalized payments by destination account while
// preserving every original payment ID for replay protection and metrics.
func AggregatePayments(payments []paymentmodel.CrossShardPayment) ([]paymentmodel.CrossShardPayment, error) {
	groups := make(map[aggregateKey]*aggregateValue)

	for _, payment := range payments {
		if payment.SourceAggregated {
			return nil, paymentmodel.ErrInvalidAbstract
		}

		id, err := payment.ID()
		if err != nil {
			return nil, fmt.Errorf("calculate payment ID: %w", err)
		}

		key := aggregateKey{
			version: payment.Version, chainID: payment.ChainID, recipient: payment.Recipient,
			sourceShard: payment.SourceShard, destinationShard: payment.DestinationShard,
			assetID: payment.AssetID,
		}

		group := groups[key]
		if group == nil {
			group = &aggregateValue{amount: new(big.Int)}
			groups[key] = group
		}

		group.amount.Add(group.amount, payment.Amount)
		group.ids = append(group.ids, id)
	}

	result := make([]paymentmodel.CrossShardPayment, 0, len(groups))
	for key, group := range groups {
		sort.Slice(group.ids, func(i, j int) bool {
			return bytes.Compare(group.ids[i][:], group.ids[j][:]) < 0
		})
		result = append(result, paymentmodel.CrossShardPayment{
			Version: key.version, ChainID: key.chainID, Recipient: key.recipient,
			SourceShard: key.sourceShard, DestinationShard: key.destinationShard,
			AssetID: key.assetID, Amount: group.amount, SourceAggregated: true,
			OriginalIDs: group.ids,
		})
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].DestinationShard != result[j].DestinationShard {
			return result[i].DestinationShard < result[j].DestinationShard
		}

		if cmp := bytes.Compare(result[i].Recipient[:], result[j].Recipient[:]); cmp != 0 {
			return cmp < 0
		}

		return bytes.Compare(result[i].AssetID[:], result[j].AssetID[:]) < 0
	})

	return result, nil
}

func (p *Publisher) Publish(
	ctx context.Context,
	shardID, epoch int64,
	b *block.Block,
	commitTime time.Time,
) error {
	if !p.enabled || p.nodeID != 0 {
		return nil
	}

	var (
		finalized model.FinalizedBlockReceipt
		err       error
	)

	if p.sourceAggregateAbstracts {
		finalized, err = BuildWithSourceAggregation(shardID, epoch, b, commitTime)
	} else {
		finalized, err = Build(shardID, epoch, b, commitTime)
	}

	if err != nil {
		return fmt.Errorf("build finalized block receipt: %w", err)
	}

	return p.PublishReceipt(ctx, finalized)
}

// PublishReceipt also sends empty routed receipts to retain continuous vector cuts.
func (p *Publisher) PublishReceipt(ctx context.Context, finalized model.FinalizedBlockReceipt) error {
	if !p.enabled || p.nodeID != 0 {
		return nil
	}

	wrapped, err := message.WrapMsg(&message.FinalizedBlockReceiptMsg{
		NodeID:  p.nodeID,
		Receipt: finalized,
	})
	if err != nil {
		return fmt.Errorf("wrap finalized block receipt: %w", err)
	}

	beaconNodes, err := p.resolver.GetNodesInShard(nodetopo.BeaconShardID)
	if err != nil {
		return fmt.Errorf("resolve Beacon nodes: %w", err)
	}

	for _, node := range beaconNodes {
		p.sender.SendMsg2Dest(ctx, node, wrapped)
	}

	return nil
}

func toHash(value []byte, kind error) (merkle.Hash, error) {
	if len(value) != len(merkle.Hash{}) {
		return merkle.Hash{}, fmt.Errorf("%w: got %d, want %d", kind, len(value), len(merkle.Hash{}))
	}

	var hash merkle.Hash
	copy(hash[:], value)

	return hash, nil
}
