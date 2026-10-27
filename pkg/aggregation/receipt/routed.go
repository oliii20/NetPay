package receipt

import (
	"fmt"
	"math/big"
	"slices"
	"sort"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/routing"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/partition"
)

// BuildRouted decides delivery for finalized ordinary Relay1 transactions. It
// never mutates their envelope or the source block. The returned Relay1 list is
// the exclusive complement of receipt.Payments and will be turned into Relay2.
func BuildRouted(shardID, epoch, chainID, shardCount int64, b *block.Block, committedAt time.Time,
	policy routing.Policy, compact bool,
) (model.FinalizedBlockReceipt, []transaction.Transaction, error) {
	finalized, err := Build(shardID, epoch, b, committedAt)
	if err != nil {
		return model.FinalizedBlockReceipt{}, nil, err
	}

	if len(finalized.Payments) != 0 {
		return model.FinalizedBlockReceipt{}, nil, fmt.Errorf("adaptive routing requires ordinary Relay1 transactions")
	}

	finalized.RoutingVersion = policy.Version
	relay := make([]transaction.Transaction, 0)
	stats := make(map[int64]*model.DestinationStat)
	recipients := make(map[int64]map[account.Address]bool)
	seen := make(map[paymentmodel.ID]bool)

	for _, tx := range b.TxList {
		if tx.TxType() != transaction.RelayTxType || tx.RelayStage != transaction.Relay1Tx {
			continue
		}

		dest := partition.DefaultAccountLoc(tx.Recipient, shardCount)
		if dest == shardID || partition.DefaultAccountLoc(tx.Sender, shardCount) != shardID ||
			len(tx.ROriginalHash) != len(paymentmodel.ID{}) || tx.Value == nil || tx.Value.Sign() <= 0 {
			return model.FinalizedBlockReceipt{}, nil, fmt.Errorf("invalid finalized Relay1 envelope")
		}

		var id paymentmodel.ID
		copy(id[:], tx.ROriginalHash)

		if seen[id] {
			return model.FinalizedBlockReceipt{}, nil, fmt.Errorf("duplicate routed original transaction")
		}

		seen[id] = true

		if stats[dest] == nil {
			stats[dest] = &model.DestinationStat{ShardID: dest}
			recipients[dest] = make(map[account.Address]bool)
		}

		stats[dest].PaymentCount++

		recipients[dest][tx.Recipient] = true
		if !policy.Aggregates(dest) {
			relay = append(relay, tx)
			continue
		}
		// A source attestation may represent one original or compact several.
		// Its logical ID is the ordinary transaction's original hash.
		finalized.Payments = append(finalized.Payments, paymentmodel.CrossShardPayment{
			Version: paymentmodel.CurrentVersion, ChainID: uint64(chainID), Recipient: tx.Recipient,
			SourceShard: shardID, DestinationShard: dest, AssetID: paymentmodel.NativeAssetID,
			Amount: new(big.Int).Set(tx.Value), SourceAggregated: true, OriginalIDs: []paymentmodel.ID{id},
		})
	}

	for dest, stat := range stats {
		stat.RecipientCount = int64(len(recipients[dest]))
		finalized.DestinationStats = append(finalized.DestinationStats, *stat)
	}

	sort.Slice(finalized.DestinationStats, func(i, j int) bool {
		return finalized.DestinationStats[i].ShardID < finalized.DestinationStats[j].ShardID
	})

	if compact {
		finalized.Payments, err = compactRouted(finalized.Payments)
		if err != nil {
			return model.FinalizedBlockReceipt{}, nil, err
		}
	}

	return finalized, relay, nil
}

func compactRouted(payments []paymentmodel.CrossShardPayment) ([]paymentmodel.CrossShardPayment, error) {
	groups := make(map[aggregateKey]*paymentmodel.CrossShardPayment)

	for _, p := range payments {
		key := aggregateKey{
			version: p.Version, chainID: p.ChainID, recipient: p.Recipient,
			sourceShard: p.SourceShard, destinationShard: p.DestinationShard, assetID: p.AssetID,
		}
		if group := groups[key]; group != nil {
			group.Amount.Add(group.Amount, p.Amount)
			group.OriginalIDs = append(group.OriginalIDs, p.OriginalIDs...)
		} else {
			cloned := p.Clone()
			groups[key] = &cloned
		}
	}

	result := make([]paymentmodel.CrossShardPayment, 0, len(groups))
	for _, p := range groups {
		sort.Slice(
			p.OriginalIDs,
			func(i, j int) bool { return slices.Compare(p.OriginalIDs[i][:], p.OriginalIDs[j][:]) < 0 },
		)
		result = append(result, p.Clone())
	}

	for i := range result {
		_, err := result[i].ID()
		if err != nil {
			return nil, fmt.Errorf("hash routed abstract: %w", err)
		}
	}
	// Sort directly by a deterministic destination / recipient key; each group
	// has a unique key and this mode supports the native asset only.
	sort.Slice(result, func(i, j int) bool {
		if result[i].DestinationShard != result[j].DestinationShard {
			return result[i].DestinationShard < result[j].DestinationShard
		}

		return slices.Compare(result[i].Recipient[:], result[j].Recipient[:]) < 0
	})

	return result, nil
}
