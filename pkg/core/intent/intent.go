// Package intent defines cross-shard payment intents and their stable identifiers.
package intent

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
	CurrentVersion uint8 = 1
	idDomain             = "BLOCKEMULATOR_NETTING_INTENT_V1"
)

var (
	// NativeAssetID is the zero asset identifier reserved for the chain's native asset.
	NativeAssetID AssetID

	ErrUnsupportedVersion      = errors.New("unsupported intent version")
	ErrWrongChain              = errors.New("intent belongs to another chain")
	ErrInvalidAmount           = errors.New("intent amount must be positive")
	ErrSameParticipant         = errors.New("intent sender and recipient must differ")
	ErrInvalidSourceShard      = errors.New("invalid source shard")
	ErrInvalidDestinationShard = errors.New("invalid destination shard")
	ErrWrongSourceShard        = errors.New("intent submitted to the wrong source shard")
	ErrSameShard               = errors.New("intent must cross shards")
	ErrUnsupportedAsset        = errors.New("unsupported intent asset")
	ErrExpired                 = errors.New("intent has expired")
)

type ID [sha256.Size]byte

type AssetID [sha256.Size]byte

type PaymentIntent struct {
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
}

type ValidationContext struct {
	ChainID      uint64
	ShardID      int64
	ShardCount   int64
	CurrentEpoch uint64
}

// Validate checks the protocol rules that are independent from account state.
func (p PaymentIntent) Validate(ctx ValidationContext) error {
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

// CanonicalBytes returns the protocol encoding used exclusively to derive an Intent ID.
// Signature is deliberately omitted.
func (p PaymentIntent) CanonicalBytes() ([]byte, error) {
	if p.Amount == nil || p.Amount.Sign() <= 0 {
		return nil, ErrInvalidAmount
	}

	amount := p.Amount.Bytes()
	if len(amount) > math.MaxUint32 {
		return nil, fmt.Errorf("%w: encoded amount is too large", ErrInvalidAmount)
	}

	var out bytes.Buffer
	out.Grow(len(idDomain) + 1 + 8 + len(p.Sender) + len(p.Recipient) + 8 + 8 + len(p.AssetID) + 4 + len(amount) + 8 + 8)
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

func (p PaymentIntent) ID() (ID, error) {
	encoded, err := p.CanonicalBytes()
	if err != nil {
		return ID{}, fmt.Errorf("encode intent ID payload: %w", err)
	}

	return sha256.Sum256(encoded), nil
}

// EncodeRLP keeps the signature in a transaction/block commitment while allowing
// the public model to retain signed shard identifiers.
func (p PaymentIntent) EncodeRLP(w io.Writer) error {
	if p.SourceShard < 0 {
		return fmt.Errorf("encode intent: %w: %d", ErrInvalidSourceShard, p.SourceShard)
	}
	if p.DestinationShard < 0 {
		return fmt.Errorf("encode intent: %w: %d", ErrInvalidDestinationShard, p.DestinationShard)
	}

	type rlpIntent struct {
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

	return rlp.Encode(w, rlpIntent{
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
