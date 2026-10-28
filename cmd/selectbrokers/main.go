package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"

	"github.com/HuangLab-SYSU/block-emulator-x/supervisor/txsource/csvsource"
)

type activity struct {
	Address      string `json:"address"`
	Transactions int64  `json:"transactions"`
	Kind         string `json:"kind"`
}

func choose(counts map[string]int64, total, hot int, seed int64) ([]activity, error) {
	if hot < 0 || total < hot || total > len(counts) {
		return nil, fmt.Errorf("invalid broker counts: total=%d hot=%d candidates=%d", total, hot, len(counts))
	}

	rows := make([]activity, 0, len(counts))
	for a, n := range counts {
		rows = append(rows, activity{a, n, "hot"})
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Transactions == rows[j].Transactions {
			return rows[i].Address < rows[j].Address
		}

		return rows[i].Transactions > rows[j].Transactions
	})
	selected := append([]activity(nil), rows[:hot]...)
	remaining := append([]activity(nil), rows[hot:]...)
	sort.Slice(remaining, func(i, j int) bool { return remaining[i].Address < remaining[j].Address })
	rand.New(rand.NewSource(seed)).
		Shuffle(len(remaining), func(i, j int) { remaining[i], remaining[j] = remaining[j], remaining[i] })

	for _, r := range remaining[:total-hot] {
		r.Kind = "random"
		selected = append(selected, r)
	}

	return selected, nil
}

func run() error {
	dataset := flag.String("dataset", "data/25250000to25499999_BlockTransaction.csv", "dataset")
	count := flag.Int64("tx-number", 300000, "accepted transaction prefix")
	total := flag.Int("count", 50, "broker count")
	hot := flag.Int("hot", 5, "top active brokers")
	seed := flag.Int64("seed", 1, "random selection seed")
	positiveOnly := flag.Bool("positive-value-only", false, "select from the positive-payment workload prefix")
	out := flag.String("output", "", "broker file")

	flag.Parse()

	if *count <= 0 || *out == "" {
		return fmt.Errorf("positive tx-number and output required")
	}

	ds, err := csvsource.NewCSVSourceWithOptions(*dataset, csvsource.Options{
		ExcludeContractTxs: true, PositiveValueOnly: *positiveOnly,
	})
	if err != nil {
		return err
	}

	activityCounts := make(map[string]int64)

	var read int64
	for read < *count {
		txs, err := ds.ReadTxs(min(10000, *count-read))
		if err != nil {
			return err
		}

		if len(txs) == 0 {
			return fmt.Errorf("dataset exhausted after %d valid transactions", read)
		}

		for _, t := range txs {
			a, b := hex.EncodeToString(t.Sender[:]), hex.EncodeToString(t.Recipient[:])

			activityCounts[a]++
			if b != a {
				activityCounts[b]++
			}
		}

		read += int64(len(txs))
	}

	selected, err := choose(activityCounts, *total, *hot, *seed)
	if err != nil {
		return err
	}

	var lines []string
	for _, r := range selected {
		lines = append(lines, r.Address)
	}

	if err = os.WriteFile(*out, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		return err
	}

	report := struct {
		Dataset           string     `json:"dataset"`
		Transactions      int64      `json:"transactions"`
		Seed              int64      `json:"seed"`
		Candidates        int        `json:"candidates"`
		Brokers           []activity `json:"brokers"`
		PositiveValueOnly bool       `json:"positive_value_only"`
	}{*dataset, read, *seed, len(activityCounts), selected, *positiveOnly}

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(*out+".json", data, 0o644)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
