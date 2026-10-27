package message

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/rlp"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
)

const (
	PreprepareMessageType = "Preprepare"
	PrepareMessageType    = "Prepare"
	CommitMessageType     = "Commit"

	ReceiveTxsMessageType  = "ReceiveTxs"
	CatchupReqMessageType  = "CatchupReq"
	CatchupRespMessageType = "CatchupResp"

	StopConsensusMessageType = "StopConsensus"
)

type Proposal struct {
	Block             *block.Block
	AggregationBatch  *model.AggregationBatch
	AggregationHeader *model.AggregationBatchHeader
}

func (p *Proposal) Hash() ([]byte, error) {
	payloads := 0
	if p.Block != nil {
		payloads++
	}

	if p.AggregationBatch != nil {
		payloads++
	}
	if p.AggregationHeader != nil {
		payloads++
	}

	if payloads != 1 {
		return nil, errors.New("proposal must contain exactly one payload")
	}

	var encoded []byte

	if p.Block != nil {
		var err error

		encoded, err = rlp.EncodeToBytes(struct{ Block *block.Block }{Block: p.Block})
		if err != nil {
			return nil, fmt.Errorf("encode block proposal: %w", err)
		}
	} else if p.AggregationBatch != nil {
		var out bytes.Buffer
		if err := gob.NewEncoder(&out).Encode(p.AggregationBatch); err != nil {
			return nil, fmt.Errorf("encode aggregation proposal: %w", err)
		}

		encoded = out.Bytes()
	} else if p.AggregationHeader != nil {
		encoded = encodeAggregationHeader(*p.AggregationHeader)
	}

	sum := sha256.Sum256(encoded)

	return sum[:], nil
}

func encodeAggregationHeader(header model.AggregationBatchHeader) []byte {
	encoded := make([]byte, 0, 256+len(header.Cuts)*64)
	encoded = append(encoded, "BLOCKEMULATOR_PBFT_AGGREGATION_HEADER_V1"...)
	encoded = append(encoded, header.BatchID[:]...)
	encoded = append(encoded, header.PreviousBatchID[:]...)
	encoded = binary.BigEndian.AppendUint64(encoded, header.WindowID)
	if header.BatchOnly {
		encoded = append(encoded, 1)
	} else {
		encoded = append(encoded, 0)
	}
	encoded = binary.BigEndian.AppendUint32(encoded, header.CreditChunkSize)
	encoded = append(encoded, header.CutRoot[:]...)
	encoded = append(encoded, header.ReceiptRoot[:]...)
	encoded = append(encoded, header.ShardAggregationRoot[:]...)
	encoded = append(encoded, header.AggregationRoot[:]...)
	encoded = binary.BigEndian.AppendUint64(encoded, uint64(len(header.Cuts)))
	for _, cut := range header.Cuts {
		encoded = binary.BigEndian.AppendUint64(encoded, uint64(cut.ShardID))
		encoded = binary.BigEndian.AppendUint64(encoded, cut.PreviousHeight)
		encoded = binary.BigEndian.AppendUint64(encoded, cut.EndHeight)
		encoded = append(encoded, cut.EndBlockHash[:]...)
	}

	return encoded
}

// PreprepareMsg is the pre-prepare message in the PBFT consensus, and it contains a block and its digest (i.e., Hash).
type PreprepareMsg struct {
	P               Proposal
	Digest          []byte
	Seq, View       int64
	ShardID, NodeID int64
}

// PrepareMsg is the prepare message in the PBFT consensus, and it contains the digest and the agreement ack (always true).
type PrepareMsg struct {
	Digest          []byte
	Seq, View       int64
	ShardID, NodeID int64
}

// CommitMsg is the commit message in the PBFT consensus, and it contains the digest and the agreement ack (always true).
type CommitMsg struct {
	Digest          []byte
	Seq, View       int64
	ShardID, NodeID int64
}

type CatchupReqMsg struct {
	StartBlockHeight int64
	ShardID, NodeID  int64
}

type CatchupRespMsg struct {
	Proposals         []Proposal
	NextSeq, NextView int64
	ShardID, NodeID   int64
}

// ReceiveTxsMsg contains transactions.
type ReceiveTxsMsg struct {
	Txs []transaction.Transaction
}

// StopConsensusMsg is the stop-signal to nodes.
type StopConsensusMsg struct {
	StopSignal struct{}
}
