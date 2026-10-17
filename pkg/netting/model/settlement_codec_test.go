package model

import (
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"io"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
)

func TestSettlementPackageGobRoundTripAndCompression(t *testing.T) {
	t.Parallel()
	packageToEncode := settlementCodecFixture(500)

	var oldEncoding bytes.Buffer
	require.NoError(t, gob.NewEncoder(&oldEncoding).Encode(settlementPackagePlain(packageToEncode)))
	compact, err := compactSettlement(packageToEncode)
	require.NoError(t, err)
	var compactEncoding bytes.Buffer
	require.NoError(t, gob.NewEncoder(&compactEncoding).Encode(compact))
	var encoded bytes.Buffer
	require.NoError(t, gob.NewEncoder(&encoded).Encode(packageToEncode))
	require.Less(t, encoded.Len(), oldEncoding.Len())
	t.Logf("settlement gob bytes: plain=%d compact=%d compact+flate=%d", oldEncoding.Len(), compactEncoding.Len(), encoded.Len())
	encodedBytes := bytes.Clone(encoded.Bytes())

	var decoded SettlementPackage
	require.NoError(t, gob.NewDecoder(bytes.NewReader(encodedBytes)).Decode(&decoded))
	var decodedPlain bytes.Buffer
	require.NoError(t, gob.NewEncoder(&decodedPlain).Encode(settlementPackagePlain(decoded)))
	require.Equal(t, oldEncoding.Bytes(), decodedPlain.Bytes())

	var repeated bytes.Buffer
	require.NoError(t, gob.NewEncoder(&repeated).Encode(packageToEncode))
	require.Equal(t, encodedBytes, repeated.Bytes())
}

func TestSettlementPackagePreservesReceivedEncodingUntilModified(t *testing.T) {
	t.Parallel()
	original := settlementCodecFixture(500)
	fast, err := original.GobEncode()
	require.NoError(t, err)
	reader := flate.NewReader(bytes.NewReader(fast[settlementCodecHeaderSize:]))
	plain, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())

	var alternate bytes.Buffer
	alternate.Write(fast[:settlementCodecHeaderSize])
	writer, err := flate.NewWriter(&alternate, flate.BestCompression)
	require.NoError(t, err)
	_, err = writer.Write(plain)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NotEqual(t, fast, alternate.Bytes())

	var received SettlementPackage
	require.NoError(t, received.GobDecode(alternate.Bytes()))
	unchanged, err := received.GobEncode()
	require.NoError(t, err)
	require.Equal(t, alternate.Bytes(), unchanged)

	cloned := received.Clone()
	cloned.Settlement.ChunkIndex++
	modified, err := cloned.GobEncode()
	require.NoError(t, err)
	require.NotEqual(t, alternate.Bytes(), modified)
	var decoded SettlementPackage
	require.NoError(t, decoded.GobDecode(modified))
	require.Equal(t, cloned.Settlement.ChunkIndex, decoded.Settlement.ChunkIndex)
}

func TestSettlementPackageGobDecodeRejectsCorruption(t *testing.T) {
	t.Parallel()
	original := settlementCodecFixture(1)
	encoded, err := original.GobEncode()
	require.NoError(t, err)

	tests := []struct {
		name string
		data []byte
	}{
		{name: "missing header", data: encoded[:3]},
		{name: "wrong version", data: append([]byte("SPC3"), encoded[4:]...)},
		{name: "truncated stream", data: encoded[:len(encoded)-3]},
		{name: "truncated declared length", data: append([]byte(nil), encoded[:8]...)},
		{name: "oversized declared length", data: func() []byte {
			changed := append([]byte(nil), encoded...)
			binary.BigEndian.PutUint32(changed[4:8], maxSettlementDecodedBytes+1)
			return changed
		}()},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var decoded SettlementPackage
			require.Error(t, decoded.GobDecode(tc.data))
		})
	}
	var decoded SettlementPackage
	require.ErrorIs(t, decoded.GobDecode(nil), ErrInvalidSettlementEncoding)
}

func TestSettlementPackageGobDeduplicatesMixedResult(t *testing.T) {
	t.Parallel()
	original := settlementCodecFixture(1)
	result := original.Settlement.Outgoing[0]
	result.Intent.Signature = []byte("signed")
	result.MatchedAmount = big.NewInt(1)
	result.Intent.Amount = big.NewInt(3)
	result.FallbackAmount = big.NewInt(2)
	result.IntentID, _ = result.Intent.ID()
	original.Settlement.Outgoing = nil
	original.Settlement.Incoming = []IntentResult{result}
	original.Settlement.FallbackIncoming = []IntentResult{result}
	compact, err := compactSettlement(original)
	require.NoError(t, err)
	require.Len(t, compact.Results, 1)
	require.Equal(t, compact.Incoming, compact.FallbackIncoming)

	encoded, err := original.GobEncode()
	require.NoError(t, err)
	var decoded SettlementPackage
	require.NoError(t, decoded.GobDecode(encoded))
	require.Equal(t, original.Header, decoded.Header)
	require.Equal(t, original.Settlement, decoded.Settlement)
	require.Equal(t, original.ChunkProof, decoded.ChunkProof)
	require.Equal(t, original.ShardCommitment, decoded.ShardCommitment)
	require.Equal(t, original.ShardProof, decoded.ShardProof)
	decoded.Settlement.Incoming[0].Intent.Signature[0] = 'X'
	require.Equal(t, []byte("signed"), decoded.Settlement.FallbackIncoming[0].Intent.Signature)
}

func TestSettlementPackageGobZeroMatch(t *testing.T) {
	t.Parallel()
	original := settlementCodecFixture(1)
	result := original.Settlement.Outgoing[0]
	result.MatchedAmount = new(big.Int)
	result.FallbackAmount = new(big.Int).Set(result.Intent.Amount)
	original.Settlement.Outgoing[0] = result
	compact, err := compactSettlement(original)
	require.NoError(t, err)
	require.Equal(t, zeroMatch, compact.Results[0].MatchMode)
	require.Nil(t, compact.Results[0].MatchedAmount)
	encoded, err := original.GobEncode()
	require.NoError(t, err)
	var decoded SettlementPackage
	require.NoError(t, decoded.GobDecode(encoded))
	require.Zero(t, decoded.Settlement.Outgoing[0].MatchedAmount.Sign())
	require.Equal(t, result.IntentID, decoded.Settlement.Outgoing[0].IntentID)
}

func TestSettlementPackageGobPreservesIncompleteMetadataAndLegacy(t *testing.T) {
	t.Parallel()
	original := settlementCodecFixture(1)
	original.ShardCommitment.ShardID = 0
	original.ShardCommitment.ChunkCount = 0
	original.Settlement.BatchID = merkle.Hash{}
	encoded, err := original.GobEncode()
	require.NoError(t, err)
	var decoded SettlementPackage
	require.NoError(t, decoded.GobDecode(encoded))
	require.Equal(t, original.Settlement.BatchID, decoded.Settlement.BatchID)
	require.Equal(t, original.ShardCommitment, decoded.ShardCommitment)
	require.Equal(t, original.Settlement.Outgoing[0].IntentID, decoded.Settlement.Outgoing[0].IntentID)
	require.Zero(t, original.Settlement.Outgoing[0].FallbackAmount.Cmp(decoded.Settlement.Outgoing[0].FallbackAmount))

	var plain bytes.Buffer
	require.NoError(t, gob.NewEncoder(&plain).Encode(settlementPackagePlain(original)))
	var legacy bytes.Buffer
	legacy.WriteString(legacySettlementCodecMagic)
	require.NoError(t, binary.Write(&legacy, binary.BigEndian, uint32(plain.Len())))
	writer, err := flate.NewWriter(&legacy, flate.BestSpeed)
	require.NoError(t, err)
	_, err = writer.Write(plain.Bytes())
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NoError(t, decoded.GobDecode(legacy.Bytes()))
	require.Equal(t, original.Settlement.BatchID, decoded.Settlement.BatchID)
	require.Equal(t, original.ShardCommitment, decoded.ShardCommitment)
}

func settlementCodecFixture(count int) SettlementPackage {
	packageToEncode := SettlementPackage{
		Header: MatchRootBlockBody{BatchID: merkle.Hash{1}},
		Settlement: ShardSettlement{
			BatchID: merkle.Hash{1}, WindowID: 7, ShardID: 1,
			ChunkCount: 1, Outgoing: make([]IntentResult, 0, count),
		},
		ShardCommitment: ShardCommitment{ShardID: 1, ChunkCount: 1},
	}
	for idx := range count {
		var sender, recipient [20]byte
		var index [8]byte
		binary.BigEndian.PutUint64(index[:], uint64(idx+1))
		senderHash := sha256.Sum256(append([]byte("sender"), index[:]...))
		recipientHash := sha256.Sum256(append([]byte("recipient"), index[:]...))
		copy(sender[:], senderHash[:])
		copy(recipient[:], recipientHash[:])
		payment := intent.PaymentIntent{
			Version: intent.CurrentVersion, ChainID: 11,
			Sender: sender, Recipient: recipient, SourceShard: 1, DestinationShard: 2,
			AssetID: intent.NativeAssetID, Amount: big.NewInt(int64(idx + 1)),
			Nonce: uint64(idx), ExpiryEpoch: 100,
		}
		result, err := NewIntentResult(payment, payment.Amount)
		if err != nil {
			panic(err)
		}
		packageToEncode.Settlement.Outgoing = append(packageToEncode.Settlement.Outgoing, result)
	}
	return packageToEncode
}

func BenchmarkSettlementPackageGobEncode(b *testing.B) {
	fixture := settlementCodecFixture(500)
	for _, tc := range []struct {
		name   string
		encode func() error
	}{
		{name: "plain", encode: func() error {
			var encoded bytes.Buffer
			return gob.NewEncoder(&encoded).Encode(settlementPackagePlain(fixture))
		}},
		{name: "spc1", encode: func() error {
			var plain, compressed bytes.Buffer
			if err := gob.NewEncoder(&plain).Encode(settlementPackagePlain(fixture)); err != nil {
				return err
			}
			writer, err := flate.NewWriter(&compressed, flate.BestSpeed)
			if err != nil {
				return err
			}
			if _, err = writer.Write(plain.Bytes()); err != nil {
				return err
			}
			return writer.Close()
		}},
		{name: "spc2", encode: func() error {
			var encoded bytes.Buffer
			return gob.NewEncoder(&encoded).Encode(fixture)
		}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				if err := tc.encode(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
