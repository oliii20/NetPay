package message

import (
	"bytes"
	"encoding/gob"
	"fmt"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/block"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/network/rpcserver"
)

// WrapMsg encodes different types of messages.
func WrapMsg(msg any) (*rpcserver.WrappedMsg, error) {
	msgType, err := getMsgType(msg)
	if err != nil {
		return nil, fmt.Errorf("getMsgType failed: %w", err)
	}

	var buf bytes.Buffer

	encoder := gob.NewEncoder(&buf)

	if err = encoder.Encode(msg); err != nil {
		return nil, fmt.Errorf("encoder failed: %w", err)
	}

	return &rpcserver.WrappedMsg{
		MsgType: msgType,
		Payload: buf.Bytes(),
	}, nil
}

func WrapProposal(b *block.Block) *Proposal {
	return &Proposal{Block: b}
}

func WrapNettingProposal(proposal model.BatchProposal) *Proposal {
	cloned := proposal.Clone()

	return &Proposal{NettingBatch: &cloned}
}

func getMsgType(msg any) (string, error) {
	var msgType string

	switch msg.(type) {
	case *StopConsensusMsg, StopConsensusMsg:
		msgType = StopConsensusMessageType

	case *PreprepareMsg, PreprepareMsg:
		msgType = PreprepareMessageType
	case *PrepareMsg, PrepareMsg:
		msgType = PrepareMessageType
	case *CommitMsg, CommitMsg:
		msgType = CommitMessageType
	case *ReceiveTxsMsg, ReceiveTxsMsg:
		msgType = ReceiveTxsMessageType
	case *CatchupReqMsg, CatchupReqMsg:
		msgType = CatchupReqMessageType
	case *CatchupRespMsg, CatchupRespMsg:
		msgType = CatchupRespMessageType
	case *FinalizedBlockReceiptMsg, FinalizedBlockReceiptMsg:
		msgType = FinalizedBlockReceiptMessageType
	case *BatchProposalMsg, BatchProposalMsg:
		msgType = BatchProposalMessageType
	case *MatchRootFinalizedMsg, MatchRootFinalizedMsg:
		msgType = MatchRootFinalizedMessageType
	case *SettlementPackageMsg, SettlementPackageMsg:
		msgType = SettlementPackageMessageType
	case *FallbackTxMsg, FallbackTxMsg:
		msgType = FallbackTxMessageType
	case *FallbackCompletedMsg, FallbackCompletedMsg:
		msgType = FallbackCompletedMessageType
	case *NettingBatchMetricMsg, NettingBatchMetricMsg:
		msgType = NettingBatchMetricMessageType
	case *NettingBeaconMetricMsg, NettingBeaconMetricMsg:
		msgType = NettingBeaconMetricMessageType
	case *NettingExecutionMetricMsg, NettingExecutionMetricMsg:
		msgType = NettingExecutionMetricMessageType
	case *NettingProgressMsg, NettingProgressMsg:
		msgType = NettingProgressMessageType

	case *RelayBlockInfoMsg, RelayBlockInfoMsg:
		msgType = RelayBlockInfoMessageType

	case *BrokerBlockInfoMsg, BrokerBlockInfoMsg:
		msgType = BrokerBlockInfoMessageType
	case *BrokerCLPATxSendAgainMsg, BrokerCLPATxSendAgainMsg:
		msgType = BrokerCLPATxSendAgainMessageType

	case *CLPARepartitionStartMsg, CLPARepartitionStartMsg:
		msgType = CLPARepartitionStartMessageType
	case *AccountMigrationMsg, AccountMigrationMsg:
		msgType = AccountMigrationMessageType

	default:
		return "", fmt.Errorf("unknown msg type: %T", msg)
	}

	return msgType, nil
}
