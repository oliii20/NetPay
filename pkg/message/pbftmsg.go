package message

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"errors"
	"fmt"

	"github.com/ethereum/go-ethereum/rlp"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
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
	Block        *block.Block
	NettingBatch *model.BatchProposal
}

func (p *Proposal) Hash() ([]byte, error) {
	if (p.Block == nil) == (p.NettingBatch == nil) {
		return nil, errors.New("proposal must contain exactly one payload")
	}
	var encoded []byte
	if p.Block != nil {
		var err error
		encoded, err = rlp.EncodeToBytes(struct{ Block *block.Block }{Block: p.Block})
		if err != nil {
			return nil, fmt.Errorf("encode block proposal: %w", err)
		}
	} else {
		var out bytes.Buffer
		if err := gob.NewEncoder(&out).Encode(p.NettingBatch); err != nil {
			return nil, fmt.Errorf("encode netting proposal: %w", err)
		}
		encoded = out.Bytes()
	}

	sum := sha256.Sum256(encoded)

	return sum[:], nil
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
