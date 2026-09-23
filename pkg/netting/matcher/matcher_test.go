package matcher_test

import (
	"bytes"
	"math/big"
	"math/rand"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/matcher"
)

func TestMatchFullModeMaximizesPositiveIntentCount(t *testing.T) {
	t.Parallel()

	payments := []intent.PaymentIntent{
		matcherTestIntent(1, 0, 1, 5),
		matcherTestIntent(2, 0, 1, 6),
		matcherTestIntent(3, 1, 0, 5),
		matcherTestIntent(4, 1, 0, 7),
	}

	output, err := matcher.Match(payments)
	require.NoError(t, err)
	require.Len(t, output.Allocations, 4)
	require.Equal(t, []matcher.Phase{
		matcher.SplitPhase, matcher.SplitPhase, matcher.SplitPhase, matcher.SplitPhase,
	}, allocationPhases(output))
	requireResultAmounts(t, output, map[byte][2]int64{
		1: {1, 4},
		2: {1, 5},
		3: {1, 4},
		4: {1, 6},
	})
}

func TestMatchModesSupportAblation(t *testing.T) {
	t.Parallel()

	payments := []intent.PaymentIntent{
		matcherTestIntent(1, 0, 1, 5),
		matcherTestIntent(2, 0, 1, 7),
		matcherTestIntent(3, 1, 0, 5),
		matcherTestIntent(4, 1, 0, 8),
	}

	exact, err := matcher.MatchWithMode(payments, matcher.ExactOnlyMode)
	require.NoError(t, err)
	require.Equal(t, []matcher.Phase{matcher.ExactPhase}, allocationPhases(exact))
	requireResultAmounts(t, exact, map[byte][2]int64{
		1: {5, 0},
		2: {0, 7},
		3: {5, 0},
		4: {0, 8},
	})

	bestFit, err := matcher.MatchWithMode(payments, matcher.BestFitMode)
	require.NoError(t, err)
	require.Equal(t, []matcher.Phase{matcher.ExactPhase, matcher.BestFitPhase}, allocationPhases(bestFit))
	requireResultAmounts(t, bestFit, map[byte][2]int64{
		1: {5, 0},
		2: {7, 0},
		3: {5, 0},
		4: {7, 1},
	})

	full, err := matcher.MatchWithMode(payments, matcher.FullMode)
	require.NoError(t, err)
	require.Equal(t, []matcher.Phase{
		matcher.SplitPhase, matcher.SplitPhase, matcher.SplitPhase, matcher.SplitPhase,
	}, allocationPhases(full))
	requireResultAmounts(t, full, map[byte][2]int64{
		1: {1, 4},
		2: {1, 6},
		3: {1, 4},
		4: {1, 7},
	})

	split, err := matcher.MatchWithMode([]intent.PaymentIntent{
		matcherTestIntent(6, 0, 1, 7),
		matcherTestIntent(7, 1, 0, 4),
		matcherTestIntent(8, 1, 0, 3),
	}, matcher.FullMode)
	require.NoError(t, err)
	require.Equal(t, []matcher.Phase{
		matcher.SplitPhase, matcher.SplitPhase, matcher.SplitPhase,
	}, allocationPhases(split))
	requireResultAmounts(t, split, map[byte][2]int64{
		6: {2, 5},
		7: {1, 3},
		8: {1, 2},
	})
}

func TestMatchBestFitChoosesSmallestSufficientCounterIntent(t *testing.T) {
	t.Parallel()

	output, err := matcher.MatchWithMode([]intent.PaymentIntent{
		matcherTestIntent(1, 0, 1, 8),
		matcherTestIntent(2, 0, 1, 7),
		matcherTestIntent(3, 1, 0, 9),
		matcherTestIntent(4, 1, 0, 20),
	}, matcher.BestFitMode)
	require.NoError(t, err)
	require.Len(t, output.Allocations, 2)
	require.Equal(t, byte(3), resultSender(t, output, output.Allocations[0].HigherToLowerIntentID))
	require.Equal(t, byte(4), resultSender(t, output, output.Allocations[1].HigherToLowerIntentID))
}

func TestMatchFullModePrefersMorePositiveIntentsOverLargeAmount(t *testing.T) {
	t.Parallel()

	output, err := matcher.Match([]intent.PaymentIntent{
		matcherTestIntent(1, 0, 1, 100),
		matcherTestIntent(2, 0, 1, 1),
		matcherTestIntent(3, 0, 1, 1),
		matcherTestIntent(4, 0, 1, 1),
		matcherTestIntent(5, 0, 1, 1),
		matcherTestIntent(6, 0, 1, 1),
		matcherTestIntent(7, 1, 0, 5),
	})
	require.NoError(t, err)
	requireResultAmounts(t, output, map[byte][2]int64{
		1: {0, 100},
		2: {1, 0},
		3: {1, 0},
		4: {1, 0},
		5: {1, 0},
		6: {1, 0},
		7: {5, 0},
	})
}

func TestMatchFullModeSupportsMultilateralCycles(t *testing.T) {
	t.Parallel()

	output, err := matcher.Match([]intent.PaymentIntent{
		matcherTestIntent(1, 0, 1, 1),
		matcherTestIntent(2, 0, 1, 1),
		matcherTestIntent(3, 0, 1, 1),
		matcherTestIntent(4, 0, 1, 1),
		matcherTestIntent(5, 0, 1, 1),
		matcherTestIntent(6, 0, 1, 100),
		matcherTestIntent(7, 1, 2, 5),
		matcherTestIntent(8, 2, 0, 5),
	})
	require.NoError(t, err)
	requireResultAmounts(t, output, map[byte][2]int64{
		1: {1, 0},
		2: {1, 0},
		3: {1, 0},
		4: {1, 0},
		5: {1, 0},
		6: {0, 100},
		7: {5, 0},
		8: {5, 0},
	})
}

func TestMatchSplitConsumesSmallestAmountThenSmallestIDOnTie(t *testing.T) {
	t.Parallel()

	left := matcherTestIntent(1, 0, 1, 13)
	rightA := matcherTestIntent(2, 1, 0, 5)
	rightB := matcherTestIntent(3, 1, 0, 5)
	rightC := matcherTestIntent(4, 1, 0, 4)
	rightAID, err := rightA.ID()
	require.NoError(t, err)
	rightBID, err := rightB.ID()
	require.NoError(t, err)
	firstTieID := smallerID(rightAID, rightBID)

	output, err := matcher.Match([]intent.PaymentIntent{left, rightB, rightC, rightA})
	require.NoError(t, err)
	require.Len(t, output.Allocations, 4)
	require.Equal(t, []matcher.Phase{
		matcher.SplitPhase, matcher.SplitPhase, matcher.SplitPhase, matcher.SplitPhase,
	}, allocationPhases(output))
	require.Contains(t, allocationAmounts(output), int64(3))
	require.Contains(t, []intent.ID{
		output.Allocations[0].HigherToLowerIntentID,
		output.Allocations[1].HigherToLowerIntentID,
		output.Allocations[2].HigherToLowerIntentID,
		output.Allocations[3].HigherToLowerIntentID,
	}, firstTieID)
	firstTieTag := resultSender(t, output, firstTieID)
	secondTieTag := byte(2)
	if firstTieTag == 2 {
		secondTieTag = 3
	}
	expected := map[byte][2]int64{
		1:            {3, 10},
		4:            {1, 3},
		firstTieTag:  {1, 4},
		secondTieTag: {1, 4},
	}
	requireResultAmounts(t, output, expected)
}

func TestMatchUsesSmallestIntentFirstWhenTotalsTie(t *testing.T) {
	t.Parallel()

	output, err := matcher.Match([]intent.PaymentIntent{
		matcherTestIntent(1, 0, 1, 6),
		matcherTestIntent(2, 0, 1, 4),
		matcherTestIntent(3, 1, 0, 7),
		matcherTestIntent(4, 1, 0, 3),
	})
	require.NoError(t, err)
	require.NotEmpty(t, output.Allocations)
	requireResultAmounts(t, output, map[byte][2]int64{
		1: {1, 5},
		2: {1, 3},
		3: {1, 6},
		4: {1, 2},
	})
}

func TestMatchIsDeterministicAcrossInputOrderAndGroups(t *testing.T) {
	t.Parallel()

	payments := []intent.PaymentIntent{
		matcherTestIntent(1, 0, 1, 9),
		matcherTestIntent(2, 1, 0, 4),
		matcherTestIntent(3, 2, 3, 6),
		matcherTestIntent(4, 3, 2, 8),
		matcherTestIntent(5, 0, 2, 7),
	}
	reversed := slices.Clone(payments)
	slices.Reverse(reversed)

	forwardOutput, err := matcher.Match(payments)
	require.NoError(t, err)
	reverseOutput, err := matcher.Match(reversed)
	require.NoError(t, err)
	require.Equal(t, forwardOutput, reverseOutput)

	requireResultAmounts(t, forwardOutput, map[byte][2]int64{
		1: {1, 8},
		2: {1, 3},
		3: {1, 5},
		4: {1, 7},
		5: {0, 7},
	})
}

func TestMatchRejectsDuplicateAndMalformedIntents(t *testing.T) {
	t.Parallel()

	valid := matcherTestIntent(1, 0, 1, 5)
	_, err := matcher.Match([]intent.PaymentIntent{valid, valid})
	require.ErrorIs(t, err, matcher.ErrDuplicateIntent)

	invalid := matcherTestIntent(2, 0, 0, 5)
	_, err = matcher.Match([]intent.PaymentIntent{invalid})
	require.ErrorIs(t, err, matcher.ErrInvalidIntent)
}

func TestMatchRejectsEveryMalformedShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*intent.PaymentIntent)
	}{
		{name: "version", mutate: func(payment *intent.PaymentIntent) { payment.Version++ }},
		{name: "amount", mutate: func(payment *intent.PaymentIntent) { payment.Amount = nil }},
		{name: "participant", mutate: func(payment *intent.PaymentIntent) { payment.Recipient = payment.Sender }},
		{name: "source", mutate: func(payment *intent.PaymentIntent) { payment.SourceShard = -1 }},
		{name: "destination", mutate: func(payment *intent.PaymentIntent) { payment.DestinationShard = -1 }},
		{name: "same shard", mutate: func(payment *intent.PaymentIntent) { payment.DestinationShard = 0 }},
		{name: "asset", mutate: func(payment *intent.PaymentIntent) { payment.AssetID[0] = 1 }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			payment := matcherTestIntent(1, 0, 1, 5)
			tc.mutate(&payment)
			_, err := matcher.Match([]intent.PaymentIntent{payment})
			require.ErrorIs(t, err, matcher.ErrInvalidIntent)
		})
	}

	first := matcherTestIntent(1, 0, 1, 5)
	second := matcherTestIntent(2, 1, 0, 5)
	second.ChainID++
	_, err := matcher.Match([]intent.PaymentIntent{first, second})
	require.ErrorIs(t, err, matcher.ErrInvalidIntent)
}

func TestMatchRandomizedConservationOptimalityAndDeterminism(t *testing.T) {
	t.Parallel()

	random := rand.New(rand.NewSource(20260903))
	for iteration := range 100 {
		lowerCount := random.Intn(8) + 1
		higherCount := random.Intn(8) + 1
		payments := make([]intent.PaymentIntent, 0, lowerCount+higherCount)
		var lowerTotal, higherTotal int64
		for idx := range lowerCount {
			amount := int64(random.Intn(30) + 1)
			lowerTotal += amount
			payments = append(payments, matcherTestIntent(byte(idx+1), 0, 1, amount))
		}
		for idx := range higherCount {
			amount := int64(random.Intn(30) + 1)
			higherTotal += amount
			payments = append(payments, matcherTestIntent(byte(lowerCount+idx+1), 1, 0, amount))
		}

		forward, err := matcher.Match(payments)
		require.NoError(t, err, "iteration %d", iteration)
		shuffled := slices.Clone(payments)
		random.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		reordered, err := matcher.Match(shuffled)
		require.NoError(t, err, "iteration %d", iteration)
		require.Equal(t, forward, reordered, "iteration %d", iteration)

		var lowerMatched, higherMatched int64
		var matchedCount int
		for _, result := range forward.Results {
			require.NoError(t, result.Validate(), "iteration %d", iteration)
			if result.MatchedAmount.Sign() > 0 {
				matchedCount++
			}
			if result.Intent.SourceShard == 0 {
				lowerMatched += result.MatchedAmount.Int64()
			} else {
				higherMatched += result.MatchedAmount.Int64()
			}
		}
		require.Equal(t, lowerMatched, higherMatched, "iteration %d", iteration)
		require.Equal(
			t,
			maxBilateralPositiveCount(payments),
			matchedCount,
			"iteration %d lower_total=%d higher_total=%d",
			iteration,
			lowerTotal,
			higherTotal,
		)
	}
}

func maxBilateralPositiveCount(payments []intent.PaymentIntent) int {
	lowerAmounts := make([]int64, 0)
	higherAmounts := make([]int64, 0)
	for _, payment := range payments {
		if payment.SourceShard == 0 {
			lowerAmounts = append(lowerAmounts, payment.Amount.Int64())
		} else {
			higherAmounts = append(higherAmounts, payment.Amount.Int64())
		}
	}
	sortDesc := func(amounts []int64) {
		slices.SortFunc(amounts, func(left, right int64) int {
			switch {
			case left > right:
				return -1
			case left < right:
				return 1
			default:
				return 0
			}
		})
	}
	sortDesc(lowerAmounts)
	sortDesc(higherAmounts)
	lowerCaps := prefixSums(lowerAmounts)
	higherCaps := prefixSums(higherAmounts)
	best := 0
	for lowerCount := range len(lowerAmounts) + 1 {
		for higherCount := range len(higherAmounts) + 1 {
			if lowerCount == 0 || higherCount == 0 {
				continue
			}
			minimumFlow := int64(max(lowerCount, higherCount))
			if lowerCaps[lowerCount] >= minimumFlow && higherCaps[higherCount] >= minimumFlow {
				best = max(best, lowerCount+higherCount)
			}
		}
	}

	return best
}

func prefixSums(amounts []int64) []int64 {
	prefix := make([]int64, len(amounts)+1)
	for idx, amount := range amounts {
		prefix[idx+1] = prefix[idx] + amount
	}

	return prefix
}

func matcherTestIntent(tag byte, source, destination int64, amount int64) intent.PaymentIntent {
	var sender, recipient account.Address
	sender[0] = tag
	recipient[0] = tag + 0x40

	return intent.PaymentIntent{
		Version:          intent.CurrentVersion,
		ChainID:          11,
		Sender:           sender,
		Recipient:        recipient,
		SourceShard:      source,
		DestinationShard: destination,
		AssetID:          intent.NativeAssetID,
		Amount:           big.NewInt(amount),
		Nonce:            uint64(tag),
		ExpiryEpoch:      2,
	}
}

func requireResultAmounts(t *testing.T, output matcher.Output, expected map[byte][2]int64) {
	t.Helper()
	require.Len(t, output.Results, len(expected))

	for _, result := range output.Results {
		amounts, ok := expected[result.Intent.Sender[0]]
		require.True(t, ok, "unexpected sender tag %d", result.Intent.Sender[0])
		require.Equal(t, amounts[0], result.MatchedAmount.Int64())
		require.Equal(t, amounts[1], result.FallbackAmount.Int64())
		require.NoError(t, result.Validate())
	}
}

func resultSender(t *testing.T, output matcher.Output, id intent.ID) byte {
	t.Helper()
	for _, result := range output.Results {
		if result.IntentID == id {
			return result.Intent.Sender[0]
		}
	}
	require.FailNow(t, "result not found", "intent ID %x", id)

	return 0
}

func allocationAmounts(output matcher.Output) []int64 {
	amounts := make([]int64, len(output.Allocations))
	for idx, allocation := range output.Allocations {
		amounts[idx] = allocation.Amount.Int64()
	}
	return amounts
}

func allocationPhases(output matcher.Output) []matcher.Phase {
	phases := make([]matcher.Phase, len(output.Allocations))
	for idx, allocation := range output.Allocations {
		phases[idx] = allocation.Phase
	}
	return phases
}

func smallerID(left, right intent.ID) intent.ID {
	if bytes.Compare(left[:], right[:]) < 0 {
		return left
	}
	return right
}
