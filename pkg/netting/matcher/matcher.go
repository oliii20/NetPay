// Package matcher implements deterministic cross-shard payment netting.
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

type assetKey struct {
	AssetID intent.AssetID
}

type directedPairKey struct {
	From    int64
	To      int64
	AssetID intent.AssetID
}

type circulationEdge struct {
	from     int
	to       int
	capacity *big.Int
	flow     *big.Int
	cost     int
	pair     directedPairKey
	order    int
}

type residualArc struct {
	edge    *circulationEdge
	from    int
	to      int
	cost    int
	reverse bool
	order   int
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
	if mode == FullMode {
		allocations, matchErr := matchMultilateral(items)
		if matchErr != nil {
			return Output{}, matchErr
		}
		if invariantErr := validateMultilateral(items); invariantErr != nil {
			return Output{}, invariantErr
		}
		results, resultErr := buildResults(items)
		if resultErr != nil {
			return Output{}, resultErr
		}

		return Output{Results: results, Allocations: allocations}, nil
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
	if allowSplit {
		return matchCountGreedySplit(group)
	}

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
	sortItemsByAmountThenID(active)

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
	}

	return allocations, nil
}

func matchMultilateral(items []*workItem) ([]Allocation, error) {
	itemsByPair := make(map[directedPairKey][]*workItem)
	assets := make(map[assetKey][]*workItem)
	for _, item := range items {
		key := directedPairKey{
			From: item.payment.SourceShard, To: item.payment.DestinationShard, AssetID: item.payment.AssetID,
		}
		itemsByPair[key] = append(itemsByPair[key], item)
		assets[assetKey{AssetID: item.payment.AssetID}] = append(assets[assetKey{AssetID: item.payment.AssetID}], item)
	}
	for key := range itemsByPair {
		sortItemsByAmountThenID(itemsByPair[key])
	}

	assetKeys := make([]assetKey, 0, len(assets))
	for key := range assets {
		assetKeys = append(assetKeys, key)
	}
	sort.Slice(assetKeys, func(i, j int) bool {
		return bytes.Compare(assetKeys[i].AssetID[:], assetKeys[j].AssetID[:]) < 0
	})

	edgesByPair := make(map[directedPairKey][]*circulationEdge)
	order := 0
	for _, key := range assetKeys {
		edges, err := buildAssetCirculationEdges(key.AssetID, assets[key], &order)
		if err != nil {
			return nil, err
		}
		if err = maximizeCirculation(edges); err != nil {
			return nil, err
		}
		for _, edge := range edges {
			if edge.flow.Sign() > 0 {
				edgesByPair[edge.pair] = append(edgesByPair[edge.pair], edge)
			}
		}
	}

	for pair, edges := range edgesByPair {
		totalFlow := new(big.Int)
		rewardedFlow := new(big.Int)
		for _, edge := range edges {
			totalFlow.Add(totalFlow, edge.flow)
			if edge.cost > 0 {
				rewardedFlow.Add(rewardedFlow, edge.flow)
			}
		}
		if err := assignPairFlow(itemsByPair[pair], rewardedFlow, totalFlow); err != nil {
			return nil, fmt.Errorf("assign pair %d -> %d: %w", pair.From, pair.To, err)
		}
	}

	allocations := make([]Allocation, 0)
	for _, item := range items {
		if item.matched.Sign() > 0 {
			allocations = append(allocations, allocationForMatchedItem(item))
		}
	}
	sort.Slice(allocations, func(i, j int) bool {
		left, right := allocationPrimaryID(allocations[i]), allocationPrimaryID(allocations[j])
		return compareID(left, right) < 0
	})

	return allocations, nil
}

func buildAssetCirculationEdges(
	assetID intent.AssetID,
	items []*workItem,
	order *int,
) ([]*circulationEdge, error) {
	shardIndex := make(map[int64]int)
	shards := make([]int64, 0)
	addShard := func(shardID int64) {
		if _, exists := shardIndex[shardID]; exists {
			return
		}
		shardIndex[shardID] = len(shards)
		shards = append(shards, shardID)
	}
	for _, item := range items {
		addShard(item.payment.SourceShard)
		addShard(item.payment.DestinationShard)
	}

	byPair := make(map[directedPairKey][]*workItem)
	for _, item := range items {
		key := directedPairKey{From: item.payment.SourceShard, To: item.payment.DestinationShard, AssetID: assetID}
		byPair[key] = append(byPair[key], item)
	}
	pairs := make([]directedPairKey, 0, len(byPair))
	for pair := range byPair {
		pairs = append(pairs, pair)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].From != pairs[j].From {
			return pairs[i].From < pairs[j].From
		}
		if pairs[i].To != pairs[j].To {
			return pairs[i].To < pairs[j].To
		}
		return bytes.Compare(pairs[i].AssetID[:], pairs[j].AssetID[:]) < 0
	})

	edges := make([]*circulationEdge, 0, len(pairs)*2)
	for _, pair := range pairs {
		pairItems := byPair[pair]
		countCapacity := big.NewInt(int64(len(pairItems)))
		edges = append(edges, &circulationEdge{
			from: shardIndex[pair.From], to: shardIndex[pair.To],
			capacity: countCapacity, flow: new(big.Int), cost: 1, pair: pair, order: *order,
		})
		(*order)++

		residualCapacity := new(big.Int)
		for _, item := range pairItems {
			extra := new(big.Int).Sub(item.payment.Amount, big.NewInt(1))
			if extra.Sign() > 0 {
				residualCapacity.Add(residualCapacity, extra)
			}
		}
		if residualCapacity.Sign() > 0 {
			edges = append(edges, &circulationEdge{
				from: shardIndex[pair.From], to: shardIndex[pair.To],
				capacity: residualCapacity, flow: new(big.Int), cost: 0, pair: pair, order: *order,
			})
			(*order)++
		}
	}

	return edges, nil
}

func maximizeCirculation(edges []*circulationEdge) error {
	nodeCount := 0
	for _, edge := range edges {
		nodeCount = max(nodeCount, edge.from+1, edge.to+1)
	}
	for {
		cycle := findPositiveCycle(nodeCount, edges)
		if len(cycle) == 0 {
			return nil
		}
		amount := residualCapacity(cycle[0])
		for _, arc := range cycle[1:] {
			amount = minBig(amount, residualCapacity(arc))
		}
		if amount.Sign() <= 0 {
			return ErrMatchingInvariant
		}
		for _, arc := range cycle {
			if arc.reverse {
				arc.edge.flow.Sub(arc.edge.flow, amount)
			} else {
				arc.edge.flow.Add(arc.edge.flow, amount)
			}
		}
	}
}

func findPositiveCycle(nodeCount int, edges []*circulationEdge) []residualArc {
	if nodeCount == 0 {
		return nil
	}
	arcs := residualArcs(edges)
	dist := make([]int, nodeCount)
	predecessor := make([]int, nodeCount)
	for idx := range predecessor {
		predecessor[idx] = -1
	}

	updated := -1
	for range nodeCount {
		updated = -1
		for idx, arc := range arcs {
			if dist[arc.to] < dist[arc.from]+arc.cost {
				dist[arc.to] = dist[arc.from] + arc.cost
				predecessor[arc.to] = idx
				updated = arc.to
			}
		}
	}
	if updated == -1 {
		return nil
	}

	cycleNode := updated
	for range nodeCount {
		arcIdx := predecessor[cycleNode]
		if arcIdx < 0 {
			return nil
		}
		cycleNode = arcs[arcIdx].from
	}

	cycle := make([]residualArc, 0)
	seen := make(map[int]int)
	node := cycleNode
	for {
		if idx, exists := seen[node]; exists {
			cycle = cycle[idx:]
			break
		}
		seen[node] = len(cycle)
		arcIdx := predecessor[node]
		if arcIdx < 0 {
			return nil
		}
		arc := arcs[arcIdx]
		cycle = append(cycle, arc)
		node = arc.from
	}

	totalCost := 0
	for _, arc := range cycle {
		totalCost += arc.cost
	}
	if totalCost <= 0 {
		return nil
	}

	return cycle
}

func residualArcs(edges []*circulationEdge) []residualArc {
	arcs := make([]residualArc, 0, len(edges)*2)
	for _, edge := range edges {
		if remaining := new(big.Int).Sub(edge.capacity, edge.flow); remaining.Sign() > 0 {
			arcs = append(arcs, residualArc{
				edge: edge, from: edge.from, to: edge.to, cost: edge.cost, order: edge.order * 2,
			})
		}
		if edge.flow.Sign() > 0 {
			arcs = append(arcs, residualArc{
				edge: edge, from: edge.to, to: edge.from, cost: -edge.cost, reverse: true, order: edge.order*2 + 1,
			})
		}
	}
	sort.Slice(arcs, func(i, j int) bool {
		if arcs[i].from != arcs[j].from {
			return arcs[i].from < arcs[j].from
		}
		if arcs[i].to != arcs[j].to {
			return arcs[i].to < arcs[j].to
		}
		if arcs[i].cost != arcs[j].cost {
			return arcs[i].cost > arcs[j].cost
		}
		return arcs[i].order < arcs[j].order
	})

	return arcs
}

func residualCapacity(arc residualArc) *big.Int {
	if arc.reverse {
		return new(big.Int).Set(arc.edge.flow)
	}

	return new(big.Int).Sub(arc.edge.capacity, arc.edge.flow)
}

func assignPairFlow(items []*workItem, rewardedFlow, totalFlow *big.Int) error {
	if totalFlow.Sign() == 0 {
		return nil
	}
	count, err := bigToInt(rewardedFlow)
	if err != nil {
		return err
	}
	if count <= 0 || count > len(items) {
		return ErrMatchingInvariant
	}

	selected := selectItemsForFlow(items, count, totalFlow)
	remaining := new(big.Int).Set(totalFlow)
	for _, item := range selected {
		item.matched.SetInt64(1)
		item.remaining.Sub(item.remaining, big.NewInt(1))
		remaining.Sub(remaining, big.NewInt(1))
	}
	for _, item := range selected {
		if remaining.Sign() == 0 {
			break
		}
		extraCapacity := new(big.Int).Sub(item.payment.Amount, item.matched)
		extra := minBig(extraCapacity, remaining)
		item.matched.Add(item.matched, extra)
		item.remaining.Sub(item.remaining, extra)
		remaining.Sub(remaining, extra)
	}
	if remaining.Sign() != 0 {
		return ErrMatchingInvariant
	}

	return nil
}

func selectItemsForFlow(items []*workItem, count int, totalFlow *big.Int) []*workItem {
	candidates := append([]*workItem(nil), items...)
	if totalFlow.Cmp(big.NewInt(int64(count))) == 0 {
		sortItemsByAmountThenID(candidates)

		return candidates[:count]
	}

	sort.Slice(candidates, func(i, j int) bool {
		cmp := candidates[i].payment.Amount.Cmp(candidates[j].payment.Amount)
		if cmp != 0 {
			return cmp > 0
		}
		return compareID(candidates[i].id, candidates[j].id) < 0
	})
	selected := append([]*workItem(nil), candidates[:count]...)
	sort.Slice(selected, func(i, j int) bool {
		cmp := selected[i].payment.Amount.Cmp(selected[j].payment.Amount)
		if cmp != 0 {
			return cmp > 0
		}
		return compareID(selected[i].id, selected[j].id) < 0
	})

	return selected
}

func bigToInt(value *big.Int) (int, error) {
	if !value.IsInt64() {
		return 0, ErrMatchingInvariant
	}
	converted := value.Int64()
	if converted < 0 || int64(int(converted)) != converted {
		return 0, ErrMatchingInvariant
	}

	return int(converted), nil
}

func allocationForMatchedItem(item *workItem) Allocation {
	lower, higher := item.payment.SourceShard, item.payment.DestinationShard
	lowerID, higherID := item.id, item.id
	if lower > higher {
		lower, higher = higher, lower
	}

	return Allocation{
		Group:                 GroupKey{LowerShard: lower, HigherShard: higher, AssetID: item.payment.AssetID},
		LowerToHigherIntentID: lowerID, HigherToLowerIntentID: higherID,
		Amount: new(big.Int).Set(item.matched), Phase: SplitPhase,
	}
}

func allocationPrimaryID(allocation Allocation) intent.ID {
	if compareID(allocation.LowerToHigherIntentID, allocation.HigherToLowerIntentID) <= 0 {
		return allocation.LowerToHigherIntentID
	}

	return allocation.HigherToLowerIntentID
}

func matchCountGreedySplit(group *matchGroup) ([]Allocation, error) {
	lower := positiveRemaining(group.lowerToHigher)
	higher := positiveRemaining(group.higherToLower)
	sortItemsByAmountThenID(lower)
	sortItemsByAmountThenID(higher)

	allocations := make([]Allocation, 0)
	lowerIdx, higherIdx := 0, 0
	for lowerIdx < len(lower) && higherIdx < len(higher) {
		lowerItem := lower[lowerIdx]
		higherItem := higher[higherIdx]
		if lowerItem.remaining.Sign() == 0 {
			lowerIdx++
			continue
		}
		if higherItem.remaining.Sign() == 0 {
			higherIdx++
			continue
		}

		amount := minBig(lowerItem.remaining, higherItem.remaining)
		allocations = append(allocations, allocate(group.key, lowerItem, higherItem, amount, SplitPhase))
		if lowerItem.remaining.Sign() == 0 {
			lowerIdx++
		}
		if higherItem.remaining.Sign() == 0 {
			higherIdx++
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

func sortItemsByAmountThenID(items []*workItem) {
	sort.Slice(items, func(i, j int) bool {
		cmp := items[i].remaining.Cmp(items[j].remaining)
		if cmp != 0 {
			return cmp < 0
		}
		return compareID(items[i].id, items[j].id) < 0
	})
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

type shardAssetKey struct {
	ShardID int64
	AssetID intent.AssetID
}

type shardAssetBalance struct {
	outgoing *big.Int
	incoming *big.Int
}

func validateMultilateral(items []*workItem) error {
	balances := make(map[shardAssetKey]*shardAssetBalance)
	for _, item := range items {
		if item.remaining.Sign() < 0 || item.matched.Sign() < 0 {
			return fmt.Errorf("%w: negative amount", ErrMatchingInvariant)
		}
		total := new(big.Int).Add(item.remaining, item.matched)
		if total.Cmp(item.payment.Amount) != 0 {
			return fmt.Errorf("%w: intent %x does not conserve amount", ErrMatchingInvariant, item.id)
		}
		sourceKey := shardAssetKey{ShardID: item.payment.SourceShard, AssetID: item.payment.AssetID}
		destinationKey := shardAssetKey{ShardID: item.payment.DestinationShard, AssetID: item.payment.AssetID}
		if balances[sourceKey] == nil {
			balances[sourceKey] = &shardAssetBalance{outgoing: new(big.Int), incoming: new(big.Int)}
		}
		if balances[destinationKey] == nil {
			balances[destinationKey] = &shardAssetBalance{outgoing: new(big.Int), incoming: new(big.Int)}
		}
		balances[sourceKey].outgoing.Add(balances[sourceKey].outgoing, item.matched)
		balances[destinationKey].incoming.Add(balances[destinationKey].incoming, item.matched)
	}
	for key, balance := range balances {
		if balance.outgoing.Cmp(balance.incoming) != 0 {
			return fmt.Errorf("%w: shard %d", ErrMatchingInvariant, key.ShardID)
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
