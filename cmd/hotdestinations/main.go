// hotdestinations ranks exactly the same filtered CSV prefix as the supervisor.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/partition"
	"github.com/HuangLab-SYSU/block-emulator-x/supervisor/txsource/csvsource"
)

type destination struct {
	Shard int64 `json:"shard"`
	CTX   int64 `json:"ctx"`
}

func rank(counts []int64) []destination {
	rows := make([]destination, len(counts))
	for i, n := range counts {
		rows[i] = destination{int64(i), n}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CTX == rows[j].CTX {
			return rows[i].Shard < rows[j].Shard
		}

		return rows[i].CTX > rows[j].CTX
	})

	return rows
}

func run() error {
	source := flag.String(
		"dataset",
		"data/25250000to25499999_BlockTransaction.csv",
		"same dataset as previous experiments",
	)
	shards := flag.Int64("shards", 24, "ordinary shard count")
	number := flag.Int64("tx-number", 300000, "filtered transaction prefix length")
	output := flag.String("output", "", "JSON output")

	flag.Parse()

	if *shards < 1 || *number < 1 || *output == "" {
		return fmt.Errorf("positive shards, tx-number and output required")
	}

	ds, err := csvsource.NewCSVSource(*source, true)
	if err != nil {
		return err
	}

	counts := make([]int64, *shards)

	var read, total int64
	for read < *number {
		txs, err := ds.ReadTxs(min(10000, *number-read))
		if err != nil {
			return err
		}

		if len(txs) == 0 {
			return fmt.Errorf("dataset exhausted at %d, expected %d", read, *number)
		}

		for _, tx := range txs {
			src := partition.DefaultAccountLoc(tx.Sender, *shards)

			dst := partition.DefaultAccountLoc(tx.Recipient, *shards)
			if src != dst {
				counts[dst]++
				total++
			}
		}

		read += int64(len(txs))
	}

	f, err := os.Open(*source)
	if err != nil {
		return err
	}

	hash := sha256.New()

	_, copyErr := io.Copy(hash, f)
	if err = errors.Join(copyErr, f.Close()); err != nil {
		return fmt.Errorf("hash source dataset: %w", err)
	}

	report := struct {
		Dataset          string        `json:"dataset"`
		SHA256           string        `json:"sha256"`
		Transactions     int64         `json:"transactions"`
		Shards           int64         `json:"shards"`
		CrossShard       int64         `json:"cross_shard"`
		ExcludeContracts bool          `json:"exclude_contract_txs"`
		Ranking          []destination `json:"ranking"`
	}{*source, hex.EncodeToString(hash.Sum(nil)), read, *shards, total, true, rank(counts)}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(*output, append(data, '\n'), 0o644)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
