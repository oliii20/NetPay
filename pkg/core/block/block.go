// Definition of block

package block

import (
	"bytes"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/transaction"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/utils"
)

const (
	TxBlockType        = 1
	MigrationBlockType = 2
)

var RecordTitle = []string{
	"ParentHash",
	"BlockHash",
	"StateRoot",
	"Number",
	"CreateTime",
	"LocationRoot",
	"TxRoot",
	"TxBodyLen",
	"TxBodyBytes",
	"Relay1TxCount",
	"Relay2TxCount",
	"Broker1TxCount",
	"Broker2TxCount",
	"CrossShardPaymentTxCount",
	"AggregationSettlementTxCount",
	"RelayTxBytes",
	"BrokerTxBytes",
	"CrossShardPaymentTxBytes",
	"AggregationSettlementTxBytes",
	"MigratedAccountsRoot",
	"MigrationAccountLen",
}

type txRecordMetrics struct {
	bodyBytes        int
	relay1Count      int
	relay2Count      int
	broker1Count     int
	broker2Count     int
	paymentCount     int
	aggregationCount int
	relayBytes       int
	brokerBytes      int
	paymentBytes     int
	aggregationBytes int
}

type Block struct {
	Header
	Body
	MigrationOpt
}

// NewBlock creates a block with header, body and migrationOpt.
func NewBlock(h Header, b Body, mOpt MigrationOpt) *Block {
	return &Block{Header: h, Body: b, MigrationOpt: mOpt}
}

// Encode encodes Block.
func (b *Block) Encode() ([]byte, error) {
	var buff bytes.Buffer

	enc := gob.NewEncoder(&buff)

	err := enc.Encode(b)
	if err != nil {
		return nil, fmt.Errorf("encode block failed: %w", err)
	}

	return buff.Bytes(), nil
}

func DecodeBlock(data []byte) (*Block, error) {
	var block Block

	err := gob.NewDecoder(bytes.NewReader(data)).Decode(&block)
	if err != nil {
		return nil, fmt.Errorf("decode block failed: %w", err)
	}

	return &block, nil
}

func ConvertBlock2Line(b *Block) ([]string, error) {
	blockHash, err := b.Hash()
	if err != nil {
		return nil, fmt.Errorf("CalcHash failed: %w", err)
	}

	metrics, err := measureTransactions(b.TxList)
	if err != nil {
		return nil, err
	}

	return []string{
		hex.EncodeToString(b.ParentBlockHash), // "ParentHash"
		hex.EncodeToString(blockHash),         // "BlockHash"
		hex.EncodeToString(b.StateRoot),       // "StateRoot"
		fmt.Sprintf("%d", b.Number),           // "Number"
		utils.ConvertTime2Str(b.CreateTime),   // "CreateTime"
		hex.EncodeToString(b.LocationRoot),    // "LocationRoot"
		hex.EncodeToString(b.TxRoot),          // "TxRoot"
		fmt.Sprintf("%d", len(b.TxList)),      // "TxBodyLen"
		strconv.Itoa(metrics.bodyBytes),
		strconv.Itoa(metrics.relay1Count),
		strconv.Itoa(metrics.relay2Count),
		strconv.Itoa(metrics.broker1Count),
		strconv.Itoa(metrics.broker2Count),
		strconv.Itoa(metrics.paymentCount),
		strconv.Itoa(metrics.aggregationCount),
		strconv.Itoa(metrics.relayBytes),
		strconv.Itoa(metrics.brokerBytes),
		strconv.Itoa(metrics.paymentBytes),
		strconv.Itoa(metrics.aggregationBytes),
		hex.EncodeToString(b.MigratedAccountsRoot), // "MigratedAccountsRoot"
		fmt.Sprintf("%d", len(b.MigratedAddrs)),    // "MigrationAccountLen"
	}, nil
}

func measureTransactions(txs []transaction.Transaction) (txRecordMetrics, error) {
	var metrics txRecordMetrics

	for idx := range txs {
		tx := &txs[idx]

		encoded, err := tx.Encode()
		if err != nil {
			return txRecordMetrics{}, fmt.Errorf("encode transaction %d for block record: %w", idx, err)
		}

		size := len(encoded)
		metrics.bodyBytes += size

		switch tx.TxType() {
		case transaction.RelayTxType:
			metrics.relayBytes += size

			switch tx.RelayStage {
			case transaction.Relay1Tx:
				metrics.relay1Count++
			case transaction.Relay2Tx:
				metrics.relay2Count++
			}
		case transaction.BrokerTxType:
			metrics.brokerBytes += size

			switch tx.BrokerStage {
			case transaction.Sigma1BrokerStage:
				metrics.broker1Count++
			case transaction.Sigma2BrokerStage:
				metrics.broker2Count++
			}
		case transaction.CrossShardPaymentTxType:
			metrics.paymentCount++
			metrics.paymentBytes += size
		case transaction.AggregationSettlementTxType:
			metrics.aggregationCount++
			metrics.aggregationBytes += size
		}
	}

	return metrics, nil
}
