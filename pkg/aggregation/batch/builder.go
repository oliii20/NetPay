// Package batch deterministically aggregates finalized source payments into
// compact, target-shard account credits.
package batch

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/commitment"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/window"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	paymentmodel "github.com/HuangLab-SYSU/block-emulator-x/pkg/core/payment"
)

const (
	ChunkLeafDomain         = "BLOCKEMULATOR_AGGREGATION_CHUNK_V1"
	CreditReceiptLeafDomain = "BLOCKEMULATOR_CREDIT_RECEIPT_V1"
)

var (
	ErrEmptyWindow        = errors.New("cannot build an empty aggregation window")
	ErrDuplicateShardCut  = errors.New("duplicate shard cut")
	ErrInvalidShardCut    = errors.New("invalid shard cut")
	ErrMissingShardCut    = errors.New("payment references a shard outside the vector cut")
	ErrInvalidAggregation = errors.New("invalid shard aggregation collection")
	ErrInvalidCommitment  = errors.New("invalid aggregation commitment")
	ErrDuplicatePayment   = errors.New("duplicate payment in aggregation batch")
	ErrInvalidCredit      = errors.New("invalid aggregated credit")
)

type Builder struct {
	BatchOnly       bool
	CreditChunkSize int
}

func (b Builder) Build(frozen window.FrozenWindow, previousBatchID merkle.Hash) (model.AggregationBatch, error) {
	proposal, _, err := b.BuildMeasured(frozen, previousBatchID)

	return proposal, err
}

func (b Builder) BuildMeasured(
	frozen window.FrozenWindow,
	previousBatchID merkle.Hash,
) (model.AggregationBatch, model.AggregationBatchMetric, error) {
	if len(frozen.Payments) == 0 {
		return model.AggregationBatch{}, model.AggregationBatchMetric{}, ErrEmptyWindow
	}

	cuts, cutShards, err := canonicalCuts(frozen.Cuts)
	if err != nil {
		return model.AggregationBatch{}, model.AggregationBatchMetric{}, err
	}

	started := time.Now()

	aggregations, err := buildAggregations(frozen.WindowID, cutShards, frozen.Payments, b.CreditChunkSize, b.BatchOnly)
	if err != nil {
		return model.AggregationBatch{}, model.AggregationBatchMetric{}, err
	}

	aggregationTime := time.Since(started)

	merkleStarted := time.Now()

	_, _, shardLeaves, err := buildShardAggregationCommitments(aggregations)
	if err != nil {
		return model.AggregationBatch{}, model.AggregationBatchMetric{}, err
	}

	receiptLeaves, err := paymentLeaves(frozen.Payments)
	if err != nil {
		return model.AggregationBatch{}, model.AggregationBatchMetric{}, err
	}

	roots, err := commitment.BuildRoots(cutLeaves(cuts), receiptLeaves, shardLeaves)
	if err != nil {
		return model.AggregationBatch{}, model.AggregationBatchMetric{}, err
	}

	root := commitment.AggregationRoot(roots)

	batchID := commitment.BatchID(previousBatchID, frozen.WindowID, root)
	for idx := range aggregations {
		aggregations[idx].BatchID = batchID
	}

	merkleTime := time.Since(merkleStarted)

	proposal := model.AggregationBatch{
		Header: model.AggregationBatchHeader{
			BatchID: batchID, PreviousBatchID: previousBatchID, WindowID: frozen.WindowID,
			BatchOnly: b.BatchOnly, CreditChunkSize: uint32(normalizeChunkSize(b.CreditChunkSize)),
			CutRoot: roots.CutRoot, ReceiptRoot: roots.ReceiptRoot,
			ShardAggregationRoot: roots.ShardAggregationRoot, AggregationRoot: root, Cuts: cuts,
		},
		Sidecar: model.AggregationSidecar{
			Receipts: cloneReceipts(frozen.Receipts), ShardAggregations: cloneAggregations(aggregations),
		},
	}
	metric := summarizeMetric(frozen, proposal, aggregationTime, merkleTime)

	return proposal, metric, nil
}

func summarizeMetric(
	frozen window.FrozenWindow,
	proposal model.AggregationBatch,
	aggregationTime, merkleTime time.Duration,
) model.AggregationBatchMetric {
	receiptCount := logicalPaymentCount(frozen.Payments)
	metric := model.AggregationBatchMetric{
		BatchID: proposal.Header.BatchID, WindowID: frozen.WindowID, CloseReason: frozen.CloseReason,
		ReceiptCount: receiptCount, SourceAbstractCount: len(frozen.Payments),
		SourceCollapsedCount: receiptCount - len(frozen.Payments),
		WindowOpenDuration:   frozen.SealedAt.Sub(frozen.OpenedAt),
		AggregationTime:      aggregationTime, MerkleBuildTime: merkleTime, BuiltAt: time.Now(),
		OriginalValue: "0", AggregatedValue: "0",
	}
	accounts := make(map[creditKey]struct{})

	original := new(big.Int)
	for _, payment := range frozen.Payments {
		original.Add(original, payment.Amount)
		accounts[creditKey{ShardID: payment.DestinationShard, AssetID: payment.AssetID, Recipient: payment.Recipient}] = struct{}{}
	}

	metric.CreditCount = len(accounts)
	if proposal.Header.BatchOnly {
		metric.CreditCount = receiptCount
	}
	metric.CollapsedReceiptCount = metric.ReceiptCount - metric.CreditCount
	metric.OriginalValue = original.String()
	metric.AggregatedValue = original.String()
	metric.Shards, metric.WatermarkSkew = summarizeCuts(frozen.Cuts)

	for _, aggregation := range proposal.Sidecar.ShardAggregations {
		if len(aggregation.Credits) > 0 {
			metric.SettlementTxCount++
		}
	}

	return metric
}

func summarizeCuts(cuts []model.ShardCut) ([]model.ShardWindowMetric, uint64) {
	shards := make([]model.ShardWindowMetric, len(cuts))

	var minimum, maximum uint64

	for idx, cut := range cuts {
		shards[idx] = model.ShardWindowMetric{
			ShardID:    cut.ShardID,
			BlockCount: cut.EndHeight - cut.PreviousHeight,
			CutHeight:  cut.EndHeight,
		}
		if idx == 0 || cut.EndHeight < minimum {
			minimum = cut.EndHeight
		}

		if idx == 0 || cut.EndHeight > maximum {
			maximum = cut.EndHeight
		}
	}

	sort.Slice(shards, func(i, j int) bool { return shards[i].ShardID < shards[j].ShardID })

	return shards, maximum - minimum
}

type creditKey struct {
	PaymentID paymentmodel.ID
	ShardID   int64
	AssetID   paymentmodel.AssetID
	Recipient account.Address
}

type creditAccumulator struct {
	Amount     *big.Int
	ReceiptIDs []paymentmodel.ID
}

func buildAggregations(
	windowID uint64,
	cutShards map[int64]struct{},
	payments []paymentmodel.CrossShardPayment,
	chunkSize int,
	batchOnly bool,
) ([]model.ShardAggregation, error) {
	groups := make(map[creditKey]*creditAccumulator)

	seen := make(map[paymentmodel.ID]struct{}, len(payments))
	for _, payment := range payments {
		if payment.Amount == nil || payment.Amount.Sign() <= 0 {
			return nil, ErrInvalidCredit
		}

		if _, ok := cutShards[payment.SourceShard]; !ok {
			return nil, fmt.Errorf("%w: source shard %d", ErrMissingShardCut, payment.SourceShard)
		}

		if _, ok := cutShards[payment.DestinationShard]; !ok {
			return nil, fmt.Errorf("%w: destination shard %d", ErrMissingShardCut, payment.DestinationShard)
		}

		ids, err := payment.LogicalIDs()
		if err != nil {
			return nil, err
		}

		for _, id := range ids {
			if _, exists := seen[id]; exists {
				return nil, fmt.Errorf("%w: %x", ErrDuplicatePayment, id)
			}

			seen[id] = struct{}{}
		}

		key := creditKey{ShardID: payment.DestinationShard, AssetID: payment.AssetID, Recipient: payment.Recipient}

		if batchOnly {
			if payment.SourceAggregated {
				return nil, fmt.Errorf("batch-only requires unmerged source payments")
			}
			key.PaymentID = ids[0]
		}
		group := groups[key]
		if group == nil {
			group = &creditAccumulator{Amount: new(big.Int)}
			groups[key] = group
		}

		group.Amount.Add(group.Amount, payment.Amount)
		group.ReceiptIDs = append(group.ReceiptIDs, ids...)
	}

	byShard := make(map[int64][]model.AggregatedCredit)

	for key, group := range groups {
		sort.Slice(group.ReceiptIDs, func(i, j int) bool {
			return bytes.Compare(group.ReceiptIDs[i][:], group.ReceiptIDs[j][:]) < 0
		})

		root, err := receiptIDRoot(group.ReceiptIDs)
		if err != nil {
			return nil, err
		}

		byShard[key.ShardID] = append(byShard[key.ShardID], model.AggregatedCredit{
			Recipient: key.Recipient, AssetID: key.AssetID, Amount: new(big.Int).Set(group.Amount),
			ReceiptCount: uint32(len(group.ReceiptIDs)), ReceiptRoot: root,
		})
	}

	shardIDs := make([]int64, 0, len(byShard))
	for shardID := range byShard {
		shardIDs = append(shardIDs, shardID)
	}

	sort.Slice(shardIDs, func(i, j int) bool { return shardIDs[i] < shardIDs[j] })

	chunkSize = normalizeChunkSize(chunkSize)
	result := make([]model.ShardAggregation, 0)

	for _, shardID := range shardIDs {
		credits := byShard[shardID]
		sortCredits(credits)

		count := 1
		if chunkSize > 0 {
			count = (len(credits) + chunkSize - 1) / chunkSize
		}

		for chunkIndex := 0; chunkIndex < count; chunkIndex++ {
			start, end := 0, len(credits)
			if chunkSize > 0 {
				start = chunkIndex * chunkSize
				end = min(start+chunkSize, len(credits))
			}

			result = append(result, model.ShardAggregation{
				BatchOnly: batchOnly, WindowID: windowID, ShardID: shardID, ChunkIndex: uint32(chunkIndex), ChunkCount: uint32(count),
				Credits: cloneCredits(credits[start:end]),
			})
		}
	}

	return result, nil
}

func logicalPaymentCount(payments []paymentmodel.CrossShardPayment) int {
	count := 0

	for _, payment := range payments {
		if payment.SourceAggregated {
			count += len(payment.OriginalIDs)
		} else {
			count++
		}
	}

	return count
}

func BuildAggregationPackages(proposal model.AggregationBatch) ([]model.AggregationPackage, error) {
	shardIDs, proofData, shardLeaves, err := buildShardAggregationCommitments(proposal.Sidecar.ShardAggregations)
	if err != nil {
		return nil, err
	}

	shardTree, err := merkle.Build(commitment.ShardAggregationLeafDomain, shardLeaves)
	if err != nil {
		return nil, err
	}

	if shardTree.Root() != proposal.Header.ShardAggregationRoot {
		return nil, ErrInvalidAggregation
	}

	packages := make([]model.AggregationPackage, 0, len(proposal.Sidecar.ShardAggregations))

	for _, shardID := range shardIDs {
		data := proofData[shardID]

		shardProof, proofErr := shardTree.Proof(int64Key(shardID))
		if proofErr != nil {
			return nil, proofErr
		}

		for _, aggregation := range data.aggregations {
			chunkProof, proofErr := data.tree.Proof(uint32Key(aggregation.ChunkIndex))
			if proofErr != nil {
				return nil, proofErr
			}

			packages = append(packages, model.AggregationPackage{
				Header: proposal.Header, Aggregation: aggregation.Clone(), ChunkProof: chunkProof,
				ShardCommitment: data.commitment, ShardProof: shardProof,
			})
		}
	}

	return packages, nil
}

func VerifyBatch(proposal model.AggregationBatch) error {
	cuts, cutShards, err := canonicalCuts(proposal.Header.Cuts)
	if err != nil {
		return err
	}

	payments := paymentsFromReceipts(proposal.Sidecar.Receipts)

	expected, err := buildAggregations(
		proposal.Header.WindowID,
		cutShards,
		payments,
		int(proposal.Header.CreditChunkSize),
		proposal.Header.BatchOnly,
	)
	if err != nil {
		return err
	}

	for idx := range expected {
		expected[idx].BatchID = proposal.Header.BatchID
	}

	if !equalAggregations(expected, proposal.Sidecar.ShardAggregations) {
		return ErrInvalidAggregation
	}

	_, _, shardLeaves, err := buildShardAggregationCommitments(expected)
	if err != nil {
		return err
	}

	receiptLeaves, err := paymentLeaves(payments)
	if err != nil {
		return err
	}

	roots, err := commitment.BuildRoots(cutLeaves(cuts), receiptLeaves, shardLeaves)
	if err != nil {
		return err
	}

	if roots.CutRoot != proposal.Header.CutRoot || roots.ReceiptRoot != proposal.Header.ReceiptRoot ||
		roots.ShardAggregationRoot != proposal.Header.ShardAggregationRoot {
		return ErrInvalidCommitment
	}

	if commitment.AggregationRoot(roots) != proposal.Header.AggregationRoot ||
		commitment.BatchID(
			proposal.Header.PreviousBatchID,
			proposal.Header.WindowID,
			proposal.Header.AggregationRoot,
		) != proposal.Header.BatchID {
		return ErrInvalidCommitment
	}

	return nil
}

func VerifyAggregationPackage(pack model.AggregationPackage, confirmed model.AggregationBatchHeader) error {
	a := pack.Aggregation
	if a.BatchID != confirmed.BatchID || a.WindowID != confirmed.WindowID ||
		a.BatchOnly != confirmed.BatchOnly || a.ShardID != pack.ShardCommitment.ShardID || a.ChunkCount != pack.ShardCommitment.ChunkCount {
		return ErrInvalidAggregation
	}

	payload, err := AggregationPayload(a)
	if err != nil {
		return err
	}

	if !merkle.Verify(ChunkLeafDomain, payload, pack.ChunkProof, pack.ShardCommitment.ShardRoot) ||
		!merkle.Verify(
			commitment.ShardAggregationLeafDomain,
			ShardCommitmentPayload(pack.ShardCommitment),
			pack.ShardProof,
			confirmed.ShardAggregationRoot,
		) {
		return ErrInvalidAggregation
	}

	roots := commitment.Roots{
		CutRoot:              confirmed.CutRoot,
		ReceiptRoot:          confirmed.ReceiptRoot,
		ShardAggregationRoot: confirmed.ShardAggregationRoot,
	}
	if commitment.AggregationRoot(roots) != confirmed.AggregationRoot ||
		commitment.BatchID(
			confirmed.PreviousBatchID,
			confirmed.WindowID,
			confirmed.AggregationRoot,
		) != confirmed.BatchID {
		return ErrInvalidCommitment
	}

	return nil
}

type shardProofData struct {
	commitment   model.ShardAggregationCommitment
	tree         *merkle.Tree
	aggregations []model.ShardAggregation
}

func buildShardAggregationCommitments(
	input []model.ShardAggregation,
) ([]int64, map[int64]shardProofData, []merkle.Leaf, error) {
	byShard := make(map[int64][]model.ShardAggregation)
	for _, aggregation := range input {
		byShard[aggregation.ShardID] = append(byShard[aggregation.ShardID], aggregation.Clone())
	}

	shardIDs := make([]int64, 0, len(byShard))
	for shardID := range byShard {
		shardIDs = append(shardIDs, shardID)
	}

	sort.Slice(shardIDs, func(i, j int) bool { return shardIDs[i] < shardIDs[j] })
	proofData := make(map[int64]shardProofData, len(shardIDs))

	shardLeaves := make([]merkle.Leaf, 0, len(shardIDs))
	for _, shardID := range shardIDs {
		aggregations := byShard[shardID]
		sort.Slice(aggregations, func(i, j int) bool { return aggregations[i].ChunkIndex < aggregations[j].ChunkIndex })

		chunkLeaves := make([]merkle.Leaf, len(aggregations))
		for idx, aggregation := range aggregations {
			if aggregation.ChunkCount != uint32(len(aggregations)) || aggregation.ChunkIndex != uint32(idx) {
				return nil, nil, nil, ErrInvalidAggregation
			}

			payload, err := AggregationPayload(aggregation)
			if err != nil {
				return nil, nil, nil, err
			}

			chunkLeaves[idx] = merkle.Leaf{Key: uint32Key(aggregation.ChunkIndex), Payload: payload}
		}

		tree, err := merkle.Build(ChunkLeafDomain, chunkLeaves)
		if err != nil {
			return nil, nil, nil, err
		}

		entry := model.ShardAggregationCommitment{
			ShardID:    shardID,
			ShardRoot:  tree.Root(),
			ChunkCount: uint32(len(aggregations)),
		}
		proofData[shardID] = shardProofData{
			commitment:   entry,
			tree:         tree,
			aggregations: cloneAggregations(aggregations),
		}
		shardLeaves = append(shardLeaves, merkle.Leaf{Key: int64Key(shardID), Payload: ShardCommitmentPayload(entry)})
	}

	return shardIDs, proofData, shardLeaves, nil
}

func canonicalCuts(input []model.ShardCut) ([]model.ShardCut, map[int64]struct{}, error) {
	cuts := append([]model.ShardCut(nil), input...)
	sort.Slice(cuts, func(i, j int) bool { return cuts[i].ShardID < cuts[j].ShardID })

	shards := make(map[int64]struct{}, len(cuts))
	for _, cut := range cuts {
		if cut.ShardID < 0 || cut.EndHeight < cut.PreviousHeight {
			return nil, nil, ErrInvalidShardCut
		}

		if _, exists := shards[cut.ShardID]; exists {
			return nil, nil, ErrDuplicateShardCut
		}

		shards[cut.ShardID] = struct{}{}
	}

	return cuts, shards, nil
}

func paymentLeaves(payments []paymentmodel.CrossShardPayment) ([]merkle.Leaf, error) {
	ordered := append([]paymentmodel.CrossShardPayment(nil), payments...)
	sort.Slice(ordered, func(i, j int) bool {
		left, _ := ordered[i].ID()
		right, _ := ordered[j].ID()

		return bytes.Compare(left[:], right[:]) < 0
	})
	leaves := make([]merkle.Leaf, len(ordered))

	seen := make(map[paymentmodel.ID]struct{}, len(ordered))
	for idx, payment := range ordered {
		id, err := payment.ID()
		if err != nil {
			return nil, err
		}

		if _, exists := seen[id]; exists {
			return nil, ErrDuplicatePayment
		}

		seen[id] = struct{}{}

		payload, err := payment.CanonicalBytes()
		if err != nil {
			return nil, err
		}

		leaves[idx] = merkle.Leaf{Key: append([]byte(nil), id[:]...), Payload: payload}
	}

	return leaves, nil
}

func receiptIDRoot(ids []paymentmodel.ID) (merkle.Hash, error) {
	leaves := make([]merkle.Leaf, len(ids))
	for idx, id := range ids {
		leaves[idx] = merkle.Leaf{Key: append([]byte(nil), id[:]...), Payload: append([]byte(nil), id[:]...)}
	}

	tree, err := merkle.Build(CreditReceiptLeafDomain, leaves)
	if err != nil {
		return merkle.Hash{}, err
	}

	return tree.Root(), nil
}

func AggregationPayload(aggregation model.ShardAggregation) ([]byte, error) {
	if aggregation.ShardID < 0 || aggregation.ChunkCount == 0 || aggregation.ChunkIndex >= aggregation.ChunkCount {
		return nil, ErrInvalidAggregation
	}

	var out bytes.Buffer
	writeUint64(&out, aggregation.WindowID)
	writeUint64(&out, uint64(aggregation.ShardID))
	writeUint32(&out, aggregation.ChunkIndex)
	writeUint32(&out, aggregation.ChunkCount)
	if aggregation.BatchOnly {
		writeUint32(&out, 1)
	} else {
		writeUint32(&out, 0)
	}
	writeUint32(&out, uint32(len(aggregation.Credits)))

	for _, credit := range aggregation.Credits {
		if credit.Amount == nil || credit.Amount.Sign() <= 0 || credit.ReceiptCount == 0 {
			return nil, ErrInvalidCredit
		}

		out.Write(credit.Recipient[:])
		out.Write(credit.AssetID[:])
		writeBigInt(&out, credit.Amount)
		writeUint32(&out, credit.ReceiptCount)
		out.Write(credit.ReceiptRoot[:])
	}

	return out.Bytes(), nil
}

func ShardCommitmentPayload(shard model.ShardAggregationCommitment) []byte {
	var out bytes.Buffer
	writeUint64(&out, uint64(shard.ShardID))
	out.Write(shard.ShardRoot[:])
	writeUint32(&out, shard.ChunkCount)

	return out.Bytes()
}

func cutLeaves(cuts []model.ShardCut) []merkle.Leaf {
	leaves := make([]merkle.Leaf, len(cuts))
	for idx, cut := range cuts {
		var out bytes.Buffer
		writeUint64(&out, uint64(cut.ShardID))
		writeUint64(&out, cut.PreviousHeight)
		writeUint64(&out, cut.EndHeight)
		out.Write(cut.EndBlockHash[:])
		leaves[idx] = merkle.Leaf{Key: int64Key(cut.ShardID), Payload: out.Bytes()}
	}

	return leaves
}

func paymentsFromReceipts(receipts []model.FinalizedBlockReceipt) []paymentmodel.CrossShardPayment {
	payments := make([]paymentmodel.CrossShardPayment, 0)

	for _, receipt := range receipts {
		for _, payment := range receipt.Payments {
			payments = append(payments, payment.Clone())
		}
	}

	return payments
}

func equalAggregations(left, right []model.ShardAggregation) bool {
	if len(left) != len(right) {
		return false
	}

	for idx := range left {
		if left[idx].BatchID != right[idx].BatchID || left[idx].WindowID != right[idx].WindowID ||
			left[idx].BatchOnly != right[idx].BatchOnly || left[idx].ShardID != right[idx].ShardID || left[idx].ChunkIndex != right[idx].ChunkIndex ||
			left[idx].ChunkCount != right[idx].ChunkCount || len(left[idx].Credits) != len(right[idx].Credits) {
			return false
		}

		for creditIdx := range left[idx].Credits {
			a, b := left[idx].Credits[creditIdx], right[idx].Credits[creditIdx]
			if a.Recipient != b.Recipient || a.AssetID != b.AssetID || a.ReceiptCount != b.ReceiptCount ||
				a.ReceiptRoot != b.ReceiptRoot || a.Amount.Cmp(b.Amount) != 0 {
				return false
			}
		}
	}

	return true
}

func sortCredits(credits []model.AggregatedCredit) {
	sort.Slice(credits, func(i, j int) bool {
		if cmp := bytes.Compare(credits[i].AssetID[:], credits[j].AssetID[:]); cmp != 0 {
			return cmp < 0
		}

		if cmp := bytes.Compare(credits[i].Recipient[:], credits[j].Recipient[:]); cmp != 0 {
			return cmp < 0
		}
		return bytes.Compare(credits[i].ReceiptRoot[:], credits[j].ReceiptRoot[:]) < 0
	})
}

func cloneCredits(input []model.AggregatedCredit) []model.AggregatedCredit {
	result := make([]model.AggregatedCredit, len(input))
	for idx := range input {
		result[idx] = input[idx].Clone()
	}

	return result
}

func cloneAggregations(input []model.ShardAggregation) []model.ShardAggregation {
	result := make([]model.ShardAggregation, len(input))
	for idx := range input {
		result[idx] = input[idx].Clone()
	}

	return result
}

func cloneReceipts(input []model.FinalizedBlockReceipt) []model.FinalizedBlockReceipt {
	result := make([]model.FinalizedBlockReceipt, len(input))
	for idx := range input {
		result[idx] = input[idx].Clone()
	}

	return result
}

func normalizeChunkSize(size int) int {
	if size <= 0 {
		return 0
	}

	return size
}

func writeBigInt(out *bytes.Buffer, value *big.Int) {
	encoded := value.Bytes()
	writeUint32(out, uint32(len(encoded)))
	out.Write(encoded)
}

func writeUint32(out *bytes.Buffer, value uint32) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	out.Write(encoded[:])
}

func writeUint64(out *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	out.Write(encoded[:])
}

func int64Key(value int64) []byte {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], uint64(value))

	return encoded[:]
}

func uint32Key(value uint32) []byte {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)

	return encoded[:]
}
