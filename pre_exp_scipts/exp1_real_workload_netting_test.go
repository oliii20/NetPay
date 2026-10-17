package main

import (
	"encoding/csv"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWritePairRateRankingCSVSortsAndMarksAllowlist(t *testing.T) {
	t.Parallel()

	stats := map[pairKey]*countStats{
		{lower: 0, higher: 1}: newCountStats(10, 7, 1),
		{lower: 0, higher: 2}: newCountStats(10, 8, 1),
		{lower: 1, higher: 2}: newCountStats(10, 8, 0),
		{lower: 2, higher: 3}: newCountStats(0, 0, 0),
	}
	path := filepath.Join(t.TempDir(), "pair_matching_rate_ranking.csv")
	require.NoError(t, writePairRateRankingCSV(path, stats, 0.8))

	file, err := os.Open(path)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, file.Close()) })
	records, err := csv.NewReader(file).ReadAll()
	require.NoError(t, err)
	require.Equal(t, []string{
		"rank", "pair", "matched_intent_count", "intent_count", "matched_intent_rate",
		"fully_matched_intent_count", "partially_matched_intent_count", "whitelisted",
	}, records[0])
	require.Equal(t, []string{"1", "0-2", "9", "10", "0.900000", "8", "1", "true"}, records[1])
	require.Equal(t, []string{"2", "0-1", "8", "10", "0.800000", "7", "1", "true"}, records[2])
	require.Equal(t, []string{"3", "1-2", "8", "10", "0.800000", "8", "0", "true"}, records[3])
	require.Equal(t, "false", records[4][7])
}

func TestRunRejectsInvalidPairRateThreshold(t *testing.T) {
	t.Parallel()

	err := run(config{shardCount: 24, injectSize: 1, blockSize: 1, maxRows: 1, pairRateThreshold: 1.01})
	require.EqualError(t, err, "pair-rate-threshold must be between 0 and 1")
}

func newCountStats(total, fullyMatched, partialMatched int) *countStats {
	return &countStats{
		totalCTX:       total,
		fullyMatched:   fullyMatched,
		partialMatched: partialMatched,
		totalValue:     new(big.Int),
		matchedValue:   new(big.Int),
	}
}
