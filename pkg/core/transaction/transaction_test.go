package transaction

import (
	"bytes"
	"compress/flate"
	"encoding/gob"
	"io"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/merkle"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
)

func TestTxTypeKeepsLegacyInference(t *testing.T) {
	t.Parallel()

	var recipient account.Address
	recipient[0] = 1

	tests := []struct {
		name string
		tx   Transaction
		want byte
	}{
		{name: "normal", tx: Transaction{}, want: NormalTxType},
		{name: "relay", tx: Transaction{RelayTxOpt: RelayTxOpt{ROriginalHash: []byte{1}}}, want: RelayTxType},
		{name: "broker", tx: Transaction{BrokerTxOpt: BrokerTxOpt{BOriginalHash: []byte{1}}}, want: BrokerTxType},
		{name: "create contract", tx: Transaction{Data: []byte{1}}, want: CreateContractTxType},
		{name: "call contract", tx: Transaction{Recipient: recipient, Data: []byte{1}}, want: CallContractTxType},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.tx.TxType())
		})
	}
}

func TestTxTypeIntentTakesPrecedence(t *testing.T) {
	t.Parallel()

	tx := Transaction{
		Data:        []byte{1},
		RelayTxOpt:  RelayTxOpt{ROriginalHash: []byte{1}},
		BrokerTxOpt: BrokerTxOpt{BOriginalHash: []byte{1}},
		IntentTxOpt: IntentTxOpt{Intent: transactionTestIntent([]byte("signature"))},
	}

	require.Equal(t, IntentSubmitTxType, tx.TxType())
	require.Equal(t, byte(5), IntentSubmitTxType)
	require.Equal(t, byte(6), SettlementTxType)
}

func TestIntentTransactionEncodingCommitsSignature(t *testing.T) {
	t.Parallel()

	createdAt := time.Unix(123, 456)
	left := NewTransaction(account.Address{}, account.Address{}, big.NewInt(1), big.NewInt(0), 0, createdAt)
	left.Intent = transactionTestIntent([]byte("signature-a"))

	right := *left
	right.Intent = transactionTestIntent([]byte("signature-b"))

	leftHash, err := left.Hash()
	require.NoError(t, err)
	rightHash, err := right.Hash()
	require.NoError(t, err)

	require.NotEqual(t, leftHash, rightHash)
}

func TestNewIntentTransactionCopiesEnvelopeAndIntent(t *testing.T) {
	t.Parallel()

	payment := transactionTestIntent([]byte("signature"))
	payment.Sender[0] = 2
	payment.Nonce = 3
	createdAt := time.Unix(123, 456)
	tx := NewIntentTransaction(*payment, createdAt)

	require.Equal(t, payment.Sender, tx.Sender)
	require.Equal(t, payment.Recipient, tx.Recipient)
	require.Equal(t, payment.Amount, tx.Value)
	require.Equal(t, payment.Nonce, tx.Nonce)
	require.Equal(t, createdAt, tx.CreateTime)
	require.Equal(t, IntentSubmitTxType, tx.TxType())

	payment.Amount.SetInt64(99)
	payment.Signature[0] = 'X'
	require.Equal(t, int64(5), tx.Intent.Amount.Int64())
	require.Equal(t, []byte("signature"), tx.Intent.Signature)
}

func TestCanonicalIntentUsesCompactCommitment(t *testing.T) {
	t.Parallel()
	payment := *transactionTestIntent([]byte("signed"))
	tx := NewIntentTransaction(payment, time.Unix(123, 456))
	compact, err := tx.Encode()
	require.NoError(t, err)

	legacy := *tx
	legacy.GasLimit++ // An extra envelope field must remain committed.
	full, err := legacy.Encode()
	require.NoError(t, err)
	require.Less(t, len(compact), len(full))
	require.Greater(t, len(full)-len(compact), 70)

	var wire bytes.Buffer
	require.NoError(t, gob.NewEncoder(&wire).Encode(tx))
	var received Transaction
	require.NoError(t, gob.NewDecoder(&wire).Decode(&received))
	reencoded, err := received.Encode()
	require.NoError(t, err)
	require.Equal(t, compact, reencoded)

	mutated := *tx
	cloned := tx.Intent.Clone()
	mutated.Intent = &cloned
	mutated.Intent.Signature = []byte("different")
	changed, err := mutated.Encode()
	require.NoError(t, err)
	require.NotEqual(t, compact, changed)
}

func TestSettlementTransactionTypeAndHashCommitPackage(t *testing.T) {
	t.Parallel()

	pack := model.SettlementPackage{
		Header: model.MatchRootBlockBody{BatchID: merkle.Hash{1}},
		Settlement: model.ShardSettlement{
			BatchID: merkle.Hash{1}, ShardID: 1, ChunkCount: 1,
		},
	}
	left := NewSettlementTransaction(pack, time.Unix(1, 0))
	require.Equal(t, SettlementTxType, left.TxType())
	leftHash, err := left.Hash()
	require.NoError(t, err)

	pack.Settlement.ChunkIndex = 1
	right := NewSettlementTransaction(pack, time.Unix(1, 0))
	rightHash, err := right.Hash()
	require.NoError(t, err)
	require.NotEqual(t, leftHash, rightHash)
}

func TestSettlementTransactionGobRoundTripKeepsHash(t *testing.T) {
	t.Parallel()
	payment := *transactionTestIntent([]byte("signed-intent"))
	payment.Sender[0] = 2
	result, err := model.NewIntentResult(payment, big.NewInt(3))
	require.NoError(t, err)
	pack := model.SettlementPackage{
		Header: model.MatchRootBlockBody{BatchID: merkle.Hash{1}},
		Settlement: model.ShardSettlement{
			BatchID: merkle.Hash{1}, ShardID: 0, ChunkCount: 1,
			Outgoing: []model.IntentResult{result},
		},
	}
	original := NewSettlementTransaction(pack, time.Unix(1, 0))
	originalHash, err := original.Hash()
	require.NoError(t, err)

	var wire bytes.Buffer
	require.NoError(t, gob.NewEncoder(&wire).Encode(original))
	var decoded Transaction
	require.NoError(t, gob.NewDecoder(&wire).Decode(&decoded))
	require.Equal(t, SettlementTxType, decoded.TxType())
	require.Equal(t, result.IntentID, decoded.Settlement.Settlement.Outgoing[0].IntentID)
	require.Zero(t, decoded.Settlement.Settlement.Outgoing[0].MatchedAmount.Cmp(result.MatchedAmount))
	decodedHash, err := decoded.Hash()
	require.NoError(t, err)
	require.Equal(t, originalHash, decodedHash)
}

func TestSettlementTransactionHashPreservesReceivedCompression(t *testing.T) {
	t.Parallel()
	payment := *transactionTestIntent([]byte("signed-intent"))
	pack := model.SettlementPackage{
		Header:     model.MatchRootBlockBody{BatchID: merkle.Hash{1}},
		Settlement: model.ShardSettlement{BatchID: merkle.Hash{1}, ShardID: 0, ChunkCount: 1},
	}
	for idx := 0; idx < 500; idx++ {
		payment.Nonce = uint64(idx)
		result, err := model.NewIntentResult(payment, big.NewInt(3))
		require.NoError(t, err)
		pack.Settlement.Outgoing = append(pack.Settlement.Outgoing, result)
	}
	fast, err := pack.GobEncode()
	require.NoError(t, err)
	require.Equal(t, "SPC2", string(fast[:4]))
	reader := flate.NewReader(bytes.NewReader(fast[8:]))
	plain, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	var alternate bytes.Buffer
	_, err = alternate.Write(fast[:8])
	require.NoError(t, err)
	writer, err := flate.NewWriter(&alternate, flate.BestCompression)
	require.NoError(t, err)
	_, err = writer.Write(plain)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.NotEqual(t, fast, alternate.Bytes())

	var received model.SettlementPackage
	require.NoError(t, received.GobDecode(alternate.Bytes()))
	tx := NewSettlementTransaction(received, time.Unix(1, 0))
	before, err := tx.Hash()
	require.NoError(t, err)
	var wire bytes.Buffer
	require.NoError(t, gob.NewEncoder(&wire).Encode(tx))
	var decoded Transaction
	require.NoError(t, gob.NewDecoder(&wire).Decode(&decoded))
	after, err := decoded.Hash()
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Equal(t, alternate.Bytes(), mustSettlementBytes(t, decoded.Settlement))
}

func mustSettlementBytes(t *testing.T, settlement *model.SettlementPackage) []byte {
	t.Helper()
	encoded, err := settlement.GobEncode()
	require.NoError(t, err)
	return encoded
}

func TestFallbackTransactionTypesAndHashesCommitPayloads(t *testing.T) {
	t.Parallel()
	require.Zero(t, NewReservedFallbackTransaction(model.ReservedFallback{}, time.Unix(1, 0)).Value.Sign())

	var intentID intent.ID
	intentID[0] = 1
	item := model.ReservedFallback{
		IntentID: intentID, BatchID: merkle.Hash{2},
		Amount: big.NewInt(3), SourceShard: 0, DestinationShard: 1,
	}
	reserved := NewReservedFallbackTransaction(item, time.Unix(1, 0))
	require.Equal(t, ReservedFallbackTxType, reserved.TxType())
	reservedHash, err := reserved.Hash()
	require.NoError(t, err)

	item.Amount = big.NewInt(4)
	changedReserved := NewReservedFallbackTransaction(item, time.Unix(1, 0))
	changedReservedHash, err := changedReserved.Hash()
	require.NoError(t, err)
	require.NotEqual(t, reservedHash, changedReservedHash)

	key := model.FallbackKey{IntentID: intentID, BatchID: merkle.Hash{2}}
	completed := NewFallbackCompletedTransaction(key, time.Unix(1, 0))
	require.Equal(t, FallbackCompletedTxType, completed.TxType())
	completedHash, err := completed.Hash()
	require.NoError(t, err)

	key.BatchID[0] = 3
	changedCompleted := NewFallbackCompletedTransaction(key, time.Unix(1, 0))
	changedCompletedHash, err := changedCompleted.Hash()
	require.NoError(t, err)
	require.NotEqual(t, completedHash, changedCompletedHash)
	require.Equal(t, byte(7), ReservedFallbackTxType)
	require.Equal(t, byte(8), FallbackCompletedTxType)
}

func transactionTestIntent(signature []byte) *intent.PaymentIntent {
	var recipient account.Address
	recipient[0] = 1

	return &intent.PaymentIntent{
		Version:          intent.CurrentVersion,
		ChainID:          11,
		Recipient:        recipient,
		SourceShard:      0,
		DestinationShard: 1,
		AssetID:          intent.NativeAssetID,
		Amount:           big.NewInt(5),
		ExpiryEpoch:      2,
		Signature:        signature,
	}
}
