package model

import (
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"sync"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
)

const (
	settlementCodecMagic       = "SPC2"
	legacySettlementCodecMagic = "SPC1"
	settlementCodecHeaderSize  = len(settlementCodecMagic) + 4
	maxSettlementDecodedBytes  = 64 << 20
)

var ErrInvalidSettlementEncoding = errors.New("invalid settlement package encoding")

var settlementCompressors sync.Pool

// settlementPackagePlain supports decoding historical SPC1 packages.
type settlementPackagePlain SettlementPackage

// Each result is encoded once per package. The three ordered role lists hold
// only indexes into Results, including when one intent is both incoming roles.
type compactSettlementPackage struct {
	Header                MatchRootBlockBody
	WindowID              uint64
	ShardID               int64
	ChunkIndex            uint32
	ChunkCount            uint32
	Results               []compactIntentResult
	Outgoing              []uint32
	Incoming              []uint32
	FallbackIncoming      []uint32
	ChunkProof            merkle.Proof
	ShardRoot             merkle.Hash
	ShardProof            merkle.Proof
	HasBatchIDOverride    bool
	BatchIDOverride       merkle.Hash
	HasShardIDOverride    bool
	ShardIDOverride       int64
	HasChunkCountOverride bool
	ChunkCountOverride    uint32
}

type compactIntentResult struct {
	Intent        intent.PaymentIntent
	MatchMode     uint8
	MatchedAmount *big.Int
}

const (
	fullMatch uint8 = iota
	zeroMatch
	partialMatch
)

func (result compactIntentResult) matchedAmount() (*big.Int, error) {
	switch result.MatchMode {
	case fullMatch:
		return result.Intent.Amount, nil
	case zeroMatch:
		return new(big.Int), nil
	case partialMatch:
		if result.MatchedAmount != nil {
			return result.MatchedAmount, nil
		}
	}

	return nil, ErrInvalidSettlementEncoding
}

func compactSettlement(p SettlementPackage) (compactSettlementPackage, error) {
	compact := compactSettlementPackage{
		Header: p.Header, WindowID: p.Settlement.WindowID,
		ShardID: p.Settlement.ShardID, ChunkIndex: p.Settlement.ChunkIndex,
		ChunkCount: p.Settlement.ChunkCount, ChunkProof: p.ChunkProof,
		ShardRoot: p.ShardCommitment.ShardRoot, ShardProof: p.ShardProof,
	}
	if p.Header.BatchID != p.Settlement.BatchID {
		compact.HasBatchIDOverride = true
		compact.BatchIDOverride = p.Settlement.BatchID
	}

	if p.Settlement.ShardID != p.ShardCommitment.ShardID {
		compact.HasShardIDOverride = true
		compact.ShardIDOverride = p.ShardCommitment.ShardID
	}

	if p.Settlement.ChunkCount != p.ShardCommitment.ChunkCount {
		compact.HasChunkCountOverride = true
		compact.ChunkCountOverride = p.ShardCommitment.ChunkCount
	}

	indexes := make(map[intent.ID][]uint32)
	add := func(results []IntentResult) ([]uint32, error) {
		refs := make([]uint32, len(results))
		for idx, result := range results {
			if err := result.Validate(); err != nil {
				return nil, fmt.Errorf("validate settlement result: %w", err)
			}

			found := false

			for _, existing := range indexes[result.IntentID] {
				previous := compact.Results[existing]

				matched, matchErr := previous.matchedAmount()
				if matchErr != nil {
					return nil, matchErr
				}

				if reflect.DeepEqual(previous.Intent, result.Intent) &&
					matched.Cmp(result.MatchedAmount) == 0 {
					refs[idx] = existing
					found = true

					break
				}
			}

			if found {
				continue
			}

			if uint64(len(compact.Results)) >= uint64(^uint32(0)) {
				return nil, ErrInvalidSettlementEncoding
			}

			refs[idx] = uint32(len(compact.Results))
			indexes[result.IntentID] = append(indexes[result.IntentID], refs[idx])

			entry := compactIntentResult{Intent: result.Intent}
			switch {
			case result.MatchedAmount.Cmp(result.Intent.Amount) == 0:
				entry.MatchMode = fullMatch
			case result.MatchedAmount.Sign() == 0:
				entry.MatchMode = zeroMatch
			default:
				entry.MatchMode = partialMatch
				entry.MatchedAmount = result.MatchedAmount
			}

			compact.Results = append(compact.Results, entry)
		}

		return refs, nil
	}

	var err error
	if compact.Outgoing, err = add(p.Settlement.Outgoing); err != nil {
		return compactSettlementPackage{}, err
	}

	if compact.Incoming, err = add(p.Settlement.Incoming); err != nil {
		return compactSettlementPackage{}, err
	}

	if compact.FallbackIncoming, err = add(p.Settlement.FallbackIncoming); err != nil {
		return compactSettlementPackage{}, err
	}

	return compact, nil
}

func (compact compactSettlementPackage) expand() (SettlementPackage, error) {
	p := SettlementPackage{
		Header: compact.Header,
		Settlement: ShardSettlement{
			BatchID: compact.Header.BatchID, WindowID: compact.WindowID,
			ShardID: compact.ShardID, ChunkIndex: compact.ChunkIndex,
			ChunkCount: compact.ChunkCount,
		},
		ChunkProof: compact.ChunkProof,
		ShardCommitment: ShardCommitment{
			ShardID: compact.ShardID, ShardRoot: compact.ShardRoot,
			ChunkCount: compact.ChunkCount,
		},
		ShardProof: compact.ShardProof,
	}
	if compact.HasBatchIDOverride {
		p.Settlement.BatchID = compact.BatchIDOverride
	}

	if compact.HasShardIDOverride {
		p.ShardCommitment.ShardID = compact.ShardIDOverride
	}

	if compact.HasChunkCountOverride {
		p.ShardCommitment.ChunkCount = compact.ChunkCountOverride
	}

	results := make([]IntentResult, len(compact.Results))
	for idx, entry := range compact.Results {
		matched, err := entry.matchedAmount()
		if err != nil {
			return SettlementPackage{}, err
		}

		result, err := NewIntentResult(entry.Intent, matched)
		if err != nil {
			return SettlementPackage{}, fmt.Errorf("expand result %d: %w", idx, err)
		}

		results[idx] = result
	}

	expandRefs := func(refs []uint32) ([]IntentResult, error) {
		if len(refs) == 0 {
			return nil, nil
		}

		output := make([]IntentResult, len(refs))
		for idx, ref := range refs {
			if uint64(ref) >= uint64(len(results)) {
				return nil, ErrInvalidSettlementEncoding
			}

			output[idx] = results[ref]
			output[idx].Intent = CloneIntent(output[idx].Intent)
			output[idx].MatchedAmount = cloneBigInt(output[idx].MatchedAmount)
			output[idx].FallbackAmount = cloneBigInt(output[idx].FallbackAmount)
		}

		return output, nil
	}

	var err error
	if p.Settlement.Outgoing, err = expandRefs(compact.Outgoing); err != nil {
		return SettlementPackage{}, err
	}

	if p.Settlement.Incoming, err = expandRefs(compact.Incoming); err != nil {
		return SettlementPackage{}, err
	}

	if p.Settlement.FallbackIncoming, err = expandRefs(compact.FallbackIncoming); err != nil {
		return SettlementPackage{}, err
	}

	return p, nil
}

// GobEncode stores deduplicated results and derived fields in versioned form.
func (p SettlementPackage) GobEncode() ([]byte, error) {
	compact, err := compactSettlement(p)
	if err != nil {
		return nil, err
	}

	var plain bytes.Buffer
	if err = gob.NewEncoder(&plain).Encode(compact); err != nil {
		return nil, fmt.Errorf("encode settlement package: %w", err)
	}

	if plain.Len() > maxSettlementDecodedBytes {
		return nil, fmt.Errorf("%w: package exceeds %d bytes", ErrInvalidSettlementEncoding, maxSettlementDecodedBytes)
	}

	if len(p.encodedWire) != 0 && sha256.Sum256(plain.Bytes()) == p.encodedState {
		return bytes.Clone(p.encodedWire), nil
	}

	prefix := make([]byte, settlementCodecHeaderSize)
	copy(prefix, settlementCodecMagic)
	binary.BigEndian.PutUint32(prefix[len(settlementCodecMagic):], uint32(plain.Len()))
	compressed := bytes.NewBuffer(prefix)

	var writer *flate.Writer
	if reused := settlementCompressors.Get(); reused != nil {
		writer = reused.(*flate.Writer)
		writer.Reset(compressed)
	} else {
		writer, err = flate.NewWriter(compressed, flate.BestSpeed)
		if err != nil {
			return nil, fmt.Errorf("create settlement compressor: %w", err)
		}
	}

	if _, err = writer.Write(plain.Bytes()); err != nil {
		return nil, fmt.Errorf("compress settlement package: %w", err)
	}

	if err = writer.Close(); err != nil {
		return nil, fmt.Errorf("finish settlement compression: %w", err)
	}

	settlementCompressors.Put(writer)

	return compressed.Bytes(), nil
}

func (p *SettlementPackage) GobDecode(encoded []byte) error {
	if len(encoded) < settlementCodecHeaderSize {
		return ErrInvalidSettlementEncoding
	}

	magic := string(encoded[:len(settlementCodecMagic)])
	if magic != settlementCodecMagic && magic != legacySettlementCodecMagic {
		return ErrInvalidSettlementEncoding
	}

	length := binary.BigEndian.Uint32(encoded[len(settlementCodecMagic):settlementCodecHeaderSize])
	if length > maxSettlementDecodedBytes {
		return fmt.Errorf("%w: decoded length %d exceeds limit", ErrInvalidSettlementEncoding, length)
	}

	reader := flate.NewReader(bytes.NewReader(encoded[settlementCodecHeaderSize:]))
	plainBytes, err := io.ReadAll(io.LimitReader(reader, int64(length)+1))
	closeErr := reader.Close()

	if err != nil {
		return fmt.Errorf("decompress settlement package: %w", err)
	}

	if closeErr != nil {
		return fmt.Errorf("close settlement decompressor: %w", closeErr)
	}

	if len(plainBytes) != int(length) {
		return fmt.Errorf("%w: expected %d bytes, got %d", ErrInvalidSettlementEncoding, length, len(plainBytes))
	}

	if magic == legacySettlementCodecMagic {
		var plain settlementPackagePlain
		if err = gob.NewDecoder(bytes.NewReader(plainBytes)).Decode(&plain); err != nil {
			return fmt.Errorf("decode legacy settlement package: %w", err)
		}

		*p = SettlementPackage(plain)

		return nil
	}

	var compact compactSettlementPackage
	if err = gob.NewDecoder(bytes.NewReader(plainBytes)).Decode(&compact); err != nil {
		return fmt.Errorf("decode compact settlement package: %w", err)
	}

	result, err := compact.expand()
	if err != nil {
		return err
	}

	result.encodedWire = bytes.Clone(encoded)

	var canonical bytes.Buffer

	canonicalCompact, err := compactSettlement(result)
	if err != nil {
		return fmt.Errorf("canonicalize compact settlement: %w", err)
	}

	if err := gob.NewEncoder(&canonical).Encode(canonicalCompact); err != nil {
		return fmt.Errorf("encode canonical settlement: %w", err)
	}

	result.encodedState = sha256.Sum256(canonical.Bytes())
	*p = result

	return nil
}
