// Package matcher implements deterministic bilateral cross-shard payment netting.
package matcher

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"sort"

	"github.com/google/btree"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
)

const btreeDegree = 8

var (
	ErrDuplicateIntent   = errors.New("duplicate intent")
	ErrInvalidIntent     = errors.New("invalid intent for matching")
	ErrMatchingInvariant = errors.New("matching invariant violated")
)

type Phase uint8

const (
	ExactPhase Phase = iota + 1
	BestFitPhase
	SplitPhase
)

type Mode string

const (
	FullMode      Mode = "full"
	BestFitMode   Mode = "best_fit"
	ExactOnlyMode Mode = "exact_only"
)

type GroupKey struct {
	LowerShard  int64
	HigherShard int64
	AssetID     intent.AssetID
}

type Allocation struct {
	Group                 GroupKey
	LowerToHigherIntentID intent.ID
	HigherToLowerIntentID intent.ID
	Amount                *big.Int
	Phase                 Phase
}

type Output struct {
	Results     []model.IntentResult
	Allocations []Allocation
}

type workItem struct {
	payment       intent.PaymentIntent
	id            intent.ID
	remaining     *big.Int
	matched       *big.Int
	lowerToHigher bool
}

type matchGroup struct {
	key           GroupKey
	lowerToHigher []*workItem
	higherToLower []*workItem
}

func Match(payments []intent.PaymentIntent) (Output, error) {
	return MatchWithMode(payments, FullMode)
}

func MatchWithMode(payments []intent.PaymentIntent, mode Mode) (Output, error) {
	mode = NormalizeMode(mode)
	items, groups, err := prepare(payments)
	if err != nil {
		return Output{}, err
	}

	keys := sortedGroupKeys(groups)
	allocations := make([]Allocation, 0)
	for _, key := range keys {
		group := groups[key]
		allocations = append(allocations, matchExact(group)...)

		if mode != ExactOnlyMode {
			remainingAllocations, matchErr := matchRemaining(group, mode == FullMode)
			if matchErr != nil {
				return Output{}, fmt.Errorf("match group %+v: %w", key, matchErr)
			}
			allocations = append(allocations, remainingAllocations...)
		}

		if invariantErr := validateGroup(group); invariantErr != nil {
			return Output{}, fmt.Errorf("validate group %+v: %w", key, invariantErr)
		}
	}

	results, err := buildResults(items)
	if err != nil {
		return Output{}, err
	}

	return Output{Results: results, Allocations: allocations}, nil
}

func NormalizeMode(mode Mode) Mode {
	switch mode {
	case "", FullMode:
		return FullMode
	case BestFitMode, ExactOnlyMode:
		return mode
	default:
		return FullMode
	}
}

func prepare(payments []intent.PaymentIntent) ([]*workItem, map[GroupKey]*matchGroup, error) {
	items := make([]*workItem, 0, len(payments))
	groups := make(map[GroupKey]*matchGroup)
	seen := make(map[intent.ID]struct{}, len(payments))

	var chainID uint64
	if len(payments) > 0 {
		chainID = payments[0].ChainID
	}

	for idx, payment := range payments {
		if err := validatePaymentShape(payment, chainID); err != nil {
			return nil, nil, fmt.Errorf("%w at index %d: %w", ErrInvalidIntent, idx, err)
		}

		id, err := payment.ID()
		if err != nil {
			return nil, nil, fmt.Errorf("%w at index %d: calculate ID: %w", ErrInvalidIntent, idx, err)
		}
		if _, exists := seen[id]; exists {
			return nil, nil, fmt.Errorf("%w: %x", ErrDuplicateIntent, id)
		}
		seen[id] = struct{}{}

		lower, higher := payment.SourceShard, payment.DestinationShard
		lowerToHigher := true
		if lower > higher {
			lower, higher = higher, lower
			lowerToHigher = false
		}

		key := GroupKey{LowerShard: lower, HigherShard: higher, AssetID: payment.AssetID}
		group, exists := groups[key]
		if !exists {
			group = &matchGroup{key: key}
			groups[key] = group
		}

		item := &workItem{
			payment:       model.CloneIntent(payment),
			id:            id,
			remaining:     new(big.Int).Set(payment.Amount),
			matched:       new(big.Int),
			lowerToHigher: lowerToHigher,
		}
		items = append(items, item)
		if lowerToHigher {
			group.lowerToHigher = append(group.lowerToHigher, item)
		} else {
			group.higherToLower = append(group.higherToLower, item)
		}
	}

	return items, groups, nil
}

func validatePaymentShape(payment intent.PaymentIntent, chainID uint64) error {
	switch {
	case payment.Version != intent.CurrentVersion:
		return intent.ErrUnsupportedVersion
	case payment.ChainID != chainID:
		return intent.ErrWrongChain
	case payment.Amount == nil || payment.Amount.Sign() <= 0:
		return intent.ErrInvalidAmount
	case payment.Sender == payment.Recipient:
		return intent.ErrSameParticipant
	case payment.SourceShard < 0:
		return intent.ErrInvalidSourceShard
	case payment.DestinationShard < 0:
		return intent.ErrInvalidDestinationShard
	case payment.SourceShard == payment.DestinationShard:
		return intent.ErrSameShard
	case payment.AssetID != intent.NativeAssetID:
		return intent.ErrUnsupportedAsset
	}

	return nil
}

func sortedGroupKeys(groups map[GroupKey]*matchGroup) []GroupKey {
	keys := make([]GroupKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}

	sort.Slice(keys, func(i, j int) bool {
		if keys[i].LowerShard != keys[j].LowerShard {
			return keys[i].LowerShard < keys[j].LowerShard
		}
		if keys[i].HigherShard != keys[j].HigherShard {
			return keys[i].HigherShard < keys[j].HigherShard
		}
		return bytes.Compare(keys[i].AssetID[:], keys[j].AssetID[:]) < 0
	})

	return keys
}

func matchExact(group *matchGroup) []Allocation {
	lowerBuckets := bucketByAmount(group.lowerToHigher)
	higherBuckets := bucketByAmount(group.higherToLower)
	amounts := commonAmountsDescending(lowerBuckets, higherBuckets)
	allocations := make([]Allocation, 0)

	for _, amount := range amounts {
		lowerItems := lowerBuckets[amount.String()]
		higherItems := higherBuckets[amount.String()]
		sortItemsByID(lowerItems)
		sortItemsByID(higherItems)

		for idx := 0; idx < min(len(lowerItems), len(higherItems)); idx++ {
			allocation := allocate(group.key, lowerItems[idx], higherItems[idx], amount, ExactPhase)
			allocations = append(allocations, allocation)
		}
	}

	return allocations
}

func matchRemaining(group *matchGroup, allowSplit bool) ([]Allocation, error) {
	lowerTotal := sumRemaining(group.lowerToHigher)
	higherTotal := sumRemaining(group.higherToLower)

	active := group.lowerToHigher
	counter := group.higherToLower
	activeIsLower := true
	if lowerTotal.Cmp(higherTotal) > 0 {
		active = group.higherToLower
		counter = group.lowerToHigher
		activeIsLower = false
	}

	active = positiveRemaining(active)
	counter = positiveRemaining(counter)
	sort.Slice(active, func(i, j int) bool {
		cmp := active[i].remaining.Cmp(active[j].remaining)
		if cmp != 0 {
			return cmp > 0
		}
		return compareID(active[i].id, active[j].id) < 0
	})

	tree := btree.NewG(btreeDegree, lessWorkItem)
	for _, item := range counter {
		tree.ReplaceOrInsert(item)
	}

	allocations := make([]Allocation, 0)
	for _, activeItem := range active {
		candidate := lowerBound(tree, activeItem.remaining)
		if candidate != nil {
			if err := deleteItem(tree, candidate); err != nil {
				return nil, err
			}
			allocations = append(
				allocations,
				allocateByDirection(group.key, activeItem, candidate, activeItem.remaining, BestFitPhase, activeIsLower),
			)
			if candidate.remaining.Sign() > 0 {
				tree.ReplaceOrInsert(candidate)
			}
			continue
		}

		if !allowSplit {
			continue
		}

		for activeItem.remaining.Sign() > 0 {
			candidate = largestAmountSmallestID(tree)
			if candidate == nil {
				return nil, fmt.Errorf("%w: counter tree exhausted", ErrMatchingInvariant)
			}
			if err := deleteItem(tree, candidate); err != nil {
				return nil, err
			}

			amount := minBig(activeItem.remaining, candidate.remaining)
			allocations = append(
				allocations,
				allocateByDirection(group.key, activeItem, candidate, amount, SplitPhase, activeIsLower),
			)
			if candidate.remaining.Sign() > 0 {
				tree.ReplaceOrInsert(candidate)
			}
		}
	}

	return allocations, nil
}

func allocateByDirection(
	key GroupKey,
	active, counter *workItem,
	amount *big.Int,
	phase Phase,
	activeIsLower bool,
) Allocation {
	if activeIsLower {
		return allocate(key, active, counter, amount, phase)
	}
	return allocate(key, counter, active, amount, phase)
}

func allocate(key GroupKey, lower, higher *workItem, amount *big.Int, phase Phase) Allocation {
	allocated := new(big.Int).Set(amount)
	lower.remaining.Sub(lower.remaining, allocated)
	lower.matched.Add(lower.matched, allocated)
	higher.remaining.Sub(higher.remaining, allocated)
	higher.matched.Add(higher.matched, allocated)

	return Allocation{
		Group:                 key,
		LowerToHigherIntentID: lower.id,
		HigherToLowerIntentID: higher.id,
		Amount:                allocated,
		Phase:                 phase,
	}
}

func bucketByAmount(items []*workItem) map[string][]*workItem {
	buckets := make(map[string][]*workItem)
	for _, item := range items {
		if item.remaining.Sign() > 0 {
			key := item.remaining.String()
			buckets[key] = append(buckets[key], item)
		}
	}
	return buckets
}

func commonAmountsDescending(left, right map[string][]*workItem) []*big.Int {
	amounts := make([]*big.Int, 0)
	for encoded := range left {
		if _, exists := right[encoded]; !exists {
			continue
		}
		amount, ok := new(big.Int).SetString(encoded, 10)
		if ok {
			amounts = append(amounts, amount)
		}
	}
	sort.Slice(amounts, func(i, j int) bool { return amounts[i].Cmp(amounts[j]) > 0 })

	return amounts
}

func sortItemsByID(items []*workItem) {
	sort.Slice(items, func(i, j int) bool { return compareID(items[i].id, items[j].id) < 0 })
}

func positiveRemaining(items []*workItem) []*workItem {
	result := make([]*workItem, 0, len(items))
	for _, item := range items {
		if item.remaining.Sign() > 0 {
			result = append(result, item)
		}
	}
	return result
}

func sumRemaining(items []*workItem) *big.Int {
	total := new(big.Int)
	for _, item := range items {
		total.Add(total, item.remaining)
	}
	return total
}

func lessWorkItem(left, right *workItem) bool {
	cmp := left.remaining.Cmp(right.remaining)
	if cmp != 0 {
		return cmp < 0
	}
	return compareID(left.id, right.id) < 0
}

func lowerBound(tree *btree.BTreeG[*workItem], amount *big.Int) *workItem {
	pivot := &workItem{remaining: new(big.Int).Set(amount)}
	var found *workItem
	tree.AscendGreaterOrEqual(pivot, func(item *workItem) bool {
		found = item
		return false
	})

	return found
}

func largestAmountSmallestID(tree *btree.BTreeG[*workItem]) *workItem {
	maximum, exists := tree.Max()
	if !exists {
		return nil
	}

	return lowerBound(tree, maximum.remaining)
}

func deleteItem(tree *btree.BTreeG[*workItem], item *workItem) error {
	if _, deleted := tree.Delete(item); !deleted {
		return fmt.Errorf("%w: counter item missing", ErrMatchingInvariant)
	}
	return nil
}

func minBig(left, right *big.Int) *big.Int {
	if left.Cmp(right) <= 0 {
		return new(big.Int).Set(left)
	}
	return new(big.Int).Set(right)
}

func compareID(left, right intent.ID) int {
	return bytes.Compare(left[:], right[:])
}

func validateGroup(group *matchGroup) error {
	lowerMatched := sumMatched(group.lowerToHigher)
	higherMatched := sumMatched(group.higherToLower)
	if lowerMatched.Cmp(higherMatched) != 0 {
		return fmt.Errorf("%w: directional matched totals differ", ErrMatchingInvariant)
	}

	for _, item := range append(group.lowerToHigher, group.higherToLower...) {
		if item.remaining.Sign() < 0 || item.matched.Sign() < 0 {
			return fmt.Errorf("%w: negative amount", ErrMatchingInvariant)
		}
		total := new(big.Int).Add(item.remaining, item.matched)
		if total.Cmp(item.payment.Amount) != 0 {
			return fmt.Errorf("%w: intent %x does not conserve amount", ErrMatchingInvariant, item.id)
		}
	}

	return nil
}

func sumMatched(items []*workItem) *big.Int {
	total := new(big.Int)
	for _, item := range items {
		total.Add(total, item.matched)
	}
	return total
}

func buildResults(items []*workItem) ([]model.IntentResult, error) {
	sort.Slice(items, func(i, j int) bool { return compareID(items[i].id, items[j].id) < 0 })
	results := make([]model.IntentResult, 0, len(items))
	for _, item := range items {
		result, err := model.NewIntentResult(item.payment, item.matched)
		if err != nil {
			return nil, fmt.Errorf("build result for intent %x: %w", item.id, err)
		}
		results = append(results, result)
	}

	return results, nil
}
