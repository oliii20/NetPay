// Package payment defines finalized cross-shard payments and their stable identifiers.
package payment

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"

	"github.com/ethereum/go-ethereum/rlp"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
)

const (
	CurrentVersion   uint8 = 1
	idDomain               = "BLOCKEMULATOR_AGGREGATION_PAYMENT_V1"
	abstractIDDomain       = "BLOCKEMULATOR_SOURCE_ABSTRACT_V1"
)

var (
	// NativeAssetID is the zero asset identifier reserved for the chain's native asset.
	NativeAssetID AssetID

	ErrUnsupportedVersion      = errors.New("unsupported payment version")
	ErrWrongChain              = errors.New("payment belongs to another chain")
	ErrInvalidAmount           = errors.New("payment amount must be positive")
	ErrSameParticipant         = errors.New("payment sender and recipient must differ")
	ErrInvalidSourceShard      = errors.New("invalid source shard")
	ErrInvalidDestinationShard = errors.New("invalid destination shard")
	ErrWrongSourceShard        = errors.New("payment submitted to the wrong source shard")
	ErrSameShard               = errors.New("payment must cross shards")
	ErrUnsupportedAsset        = errors.New("unsupported payment asset")
	ErrExpired                 = errors.New("payment has expired")
	ErrSourceAbstract          = errors.New("source-aggregated abstract is not a transaction")
	ErrInvalidAbstract         = errors.New("invalid source-aggregated abstract")
)

type ID [sha256.Size]byte

type AssetID [sha256.Size]byte

type CrossShardPayment struct {
	Version          uint8
	ChainID          uint64
	Sender           account.Address
	Recipient        account.Address
	SourceShard      int64
	DestinationShard int64
	AssetID          AssetID
	Amount           *big.Int
	Nonce            uint64
	ExpiryEpoch      uint64
	Signature        []byte
	SourceAggregated bool
	OriginalIDs      []ID
}

type ValidationContext struct {
	ChainID      uint64
	ShardID      int64
	ShardCount   int64
	CurrentEpoch uint64
}

func (p CrossShardPayment) Clone() CrossShardPayment {
	cloned := p
	if p.Amount != nil {
		cloned.Amount = new(big.Int).Set(p.Amount)
	}

	cloned.Signature = bytes.Clone(p.Signature)
	cloned.OriginalIDs = append([]ID(nil), p.OriginalIDs...)

	return cloned
}

// Validate checks the protocol rules that are independent from account state.
func (p CrossShardPayment) Validate(ctx ValidationContext) error {
	if p.SourceAggregated {
		return ErrSourceAbstract
	}

	if p.Version != CurrentVersion {
		return fmt.Errorf("%w: got %d, want %d", ErrUnsupportedVersion, p.Version, CurrentVersion)
	}

	if p.ChainID != ctx.ChainID {
		return fmt.Errorf("%w: got %d, want %d", ErrWrongChain, p.ChainID, ctx.ChainID)
	}

	if p.Amount == nil || p.Amount.Sign() <= 0 {
		return ErrInvalidAmount
	}

	if p.Sender == p.Recipient {
		return ErrSameParticipant
	}

	if p.SourceShard < 0 || p.SourceShard >= ctx.ShardCount {
		return fmt.Errorf("%w: %d", ErrInvalidSourceShard, p.SourceShard)
	}

	if p.DestinationShard < 0 || p.DestinationShard >= ctx.ShardCount {
		return fmt.Errorf("%w: %d", ErrInvalidDestinationShard, p.DestinationShard)
	}

	if p.SourceShard != ctx.ShardID {
		return fmt.Errorf("%w: got %d, want %d", ErrWrongSourceShard, p.SourceShard, ctx.ShardID)
	}

	if p.SourceShard == p.DestinationShard {
		return ErrSameShard
	}

	if p.AssetID != NativeAssetID {
		return fmt.Errorf("%w: %x", ErrUnsupportedAsset, p.AssetID)
	}

	if p.ExpiryEpoch <= ctx.CurrentEpoch {
		return fmt.Errorf("%w: expiry %d, current %d", ErrExpired, p.ExpiryEpoch, ctx.CurrentEpoch)
	}

	return nil
}

// ValidateSourceAbstract validates a compact source-shard attestation. The
// source block has already executed and authenticated the original payments,
// so the abstract intentionally omits sender, nonce, expiry, and signature.
func (p CrossShardPayment) ValidateSourceAbstract(ctx ValidationContext) error {
	if !p.SourceAggregated || p.Version != CurrentVersion || p.ChainID != ctx.ChainID {
		return ErrInvalidAbstract
	}

	if p.Amount == nil || p.Amount.Sign() <= 0 || p.SourceShard != ctx.ShardID ||
		p.SourceShard < 0 || p.SourceShard >= ctx.ShardCount ||
		p.DestinationShard < 0 || p.DestinationShard >= ctx.ShardCount ||
		p.SourceShard == p.DestinationShard || p.AssetID != NativeAssetID || len(p.OriginalIDs) == 0 {
		return ErrInvalidAbstract
	}

	for idx := range p.OriginalIDs {
		if idx > 0 && bytes.Compare(p.OriginalIDs[idx-1][:], p.OriginalIDs[idx][:]) >= 0 {
			return ErrInvalidAbstract
		}
	}

	return nil
}

// LogicalIDs returns the original user-payment IDs represented by this value.
func (p CrossShardPayment) LogicalIDs() ([]ID, error) {
	if p.SourceAggregated {
		if len(p.OriginalIDs) == 0 {
			return nil, ErrInvalidAbstract
		}

		return append([]ID(nil), p.OriginalIDs...), nil
	}

	id, err := p.ID()
	if err != nil {
		return nil, err
	}

	return []ID{id}, nil
}

// CanonicalBytes returns the protocol encoding used exclusively to derive a payment ID.
// Signature is deliberately omitted.
func (p CrossShardPayment) CanonicalBytes() ([]byte, error) {
	if p.Amount == nil || p.Amount.Sign() <= 0 {
		return nil, ErrInvalidAmount
	}

	amount := p.Amount.Bytes()
	if len(amount) > math.MaxUint32 {
		return nil, fmt.Errorf("%w: encoded amount is too large", ErrInvalidAmount)
	}

	var out bytes.Buffer

	if p.SourceAggregated {
		if len(p.OriginalIDs) == 0 {
			return nil, ErrInvalidAbstract
		}

		out.WriteString(abstractIDDomain)
		out.WriteByte(p.Version)
		writeUint64(&out, p.ChainID)
		out.Write(p.Recipient[:])
		writeUint64(&out, uint64(p.SourceShard))
		writeUint64(&out, uint64(p.DestinationShard))
		out.Write(p.AssetID[:])
		writeUint32(&out, uint32(len(amount)))
		out.Write(amount)
		writeUint32(&out, uint32(len(p.OriginalIDs)))

		for _, id := range p.OriginalIDs {
			out.Write(id[:])
		}

		return out.Bytes(), nil
	}

	out.Grow(
		len(idDomain) + 1 + 8 + len(p.Sender) + len(p.Recipient) + 8 + 8 + len(p.AssetID) + 4 + len(amount) + 8 + 8,
	)
	out.WriteString(idDomain)
	out.WriteByte(p.Version)
	writeUint64(&out, p.ChainID)
	out.Write(p.Sender[:])
	out.Write(p.Recipient[:])
	writeUint64(&out, uint64(p.SourceShard))
	writeUint64(&out, uint64(p.DestinationShard))
	out.Write(p.AssetID[:])
	writeUint32(&out, uint32(len(amount)))
	out.Write(amount)
	writeUint64(&out, p.Nonce)
	writeUint64(&out, p.ExpiryEpoch)

	return out.Bytes(), nil
}

func (p CrossShardPayment) ID() (ID, error) {
	encoded, err := p.CanonicalBytes()
	if err != nil {
		return ID{}, fmt.Errorf("encode payment ID payload: %w", err)
	}

	return sha256.Sum256(encoded), nil
}

// EncodeRLP keeps the signature in a transaction/block commitment while allowing
// the public model to retain signed shard identifiers.
func (p CrossShardPayment) EncodeRLP(w io.Writer) error {
	if p.SourceAggregated {
		return ErrSourceAbstract
	}

	if p.SourceShard < 0 {
		return fmt.Errorf("encode payment: %w: %d", ErrInvalidSourceShard, p.SourceShard)
	}

	if p.DestinationShard < 0 {
		return fmt.Errorf("encode payment: %w: %d", ErrInvalidDestinationShard, p.DestinationShard)
	}

	type rlpPayment struct {
		Version          uint8
		ChainID          uint64
		Sender           account.Address
		Recipient        account.Address
		SourceShard      uint64
		DestinationShard uint64
		AssetID          AssetID
		Amount           *big.Int
		Nonce            uint64
		ExpiryEpoch      uint64
		Signature        []byte
	}

	return rlp.Encode(w, rlpPayment{
		Version:          p.Version,
		ChainID:          p.ChainID,
		Sender:           p.Sender,
		Recipient:        p.Recipient,
		SourceShard:      uint64(p.SourceShard),
		DestinationShard: uint64(p.DestinationShard),
		AssetID:          p.AssetID,
		Amount:           p.Amount,
		Nonce:            p.Nonce,
		ExpiryEpoch:      p.ExpiryEpoch,
		Signature:        p.Signature,
	})
}

func writeUint64(out *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	out.Write(encoded[:])
}

func writeUint32(out *bytes.Buffer, value uint32) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	out.Write(encoded[:])
}
