package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/account"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/core/intent"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/matcher"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/netting/model"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/partition"
	"github.com/HuangLab-SYSU/block-emulator-x/pkg/utils"
)

const maxUint64 = ^uint64(0)
const fifoSplitMode = "fifo_split"

var bitmapFont = map[rune][]string{
	'?': {"11111", "00001", "00010", "00100", "00100", "00000", "00100"},
	'0': {"01110", "10001", "10011", "10101", "11001", "10001", "01110"},
	'1': {"00100", "01100", "00100", "00100", "00100", "00100", "01110"},
	'2': {"01110", "10001", "00001", "00010", "00100", "01000", "11111"},
	'3': {"11110", "00001", "00001", "01110", "00001", "00001", "11110"},
	'4': {"00010", "00110", "01010", "10010", "11111", "00010", "00010"},
	'5': {"11111", "10000", "10000", "11110", "00001", "00001", "11110"},
	'6': {"01110", "10000", "10000", "11110", "10001", "10001", "01110"},
	'7': {"11111", "00001", "00010", "00100", "01000", "01000", "01000"},
	'8': {"01110", "10001", "10001", "01110", "10001", "10001", "01110"},
	'9': {"01110", "10001", "10001", "01111", "00001", "00001", "01110"},
	'A': {"01110", "10001", "10001", "11111", "10001", "10001", "10001"},
	'B': {"11110", "10001", "10001", "11110", "10001", "10001", "11110"},
	'C': {"01110", "10001", "10000", "10000", "10000", "10001", "01110"},
	'D': {"11110", "10001", "10001", "10001", "10001", "10001", "11110"},
	'E': {"11111", "10000", "10000", "11110", "10000", "10000", "11111"},
	'F': {"11111", "10000", "10000", "11110", "10000", "10000", "10000"},
	'G': {"01110", "10001", "10000", "10111", "10001", "10001", "01110"},
	'H': {"10001", "10001", "10001", "11111", "10001", "10001", "10001"},
	'I': {"01110", "00100", "00100", "00100", "00100", "00100", "01110"},
	'J': {"00111", "00010", "00010", "00010", "00010", "10010", "01100"},
	'K': {"10001", "10010", "10100", "11000", "10100", "10010", "10001"},
	'L': {"10000", "10000", "10000", "10000", "10000", "10000", "11111"},
	'M': {"10001", "11011", "10101", "10101", "10001", "10001", "10001"},
	'N': {"10001", "11001", "10101", "10011", "10001", "10001", "10001"},
	'O': {"01110", "10001", "10001", "10001", "10001", "10001", "01110"},
	'P': {"11110", "10001", "10001", "11110", "10000", "10000", "10000"},
	'Q': {"01110", "10001", "10001", "10001", "10101", "10010", "01101"},
	'R': {"11110", "10001", "10001", "11110", "10100", "10010", "10001"},
	'S': {"01111", "10000", "10000", "01110", "00001", "00001", "11110"},
	'T': {"11111", "00100", "00100", "00100", "00100", "00100", "00100"},
	'U': {"10001", "10001", "10001", "10001", "10001", "10001", "01110"},
	'V': {"10001", "10001", "10001", "10001", "10001", "01010", "00100"},
	'W': {"10001", "10001", "10001", "10101", "10101", "10101", "01010"},
	'X': {"10001", "10001", "01010", "00100", "01010", "10001", "10001"},
	'Y': {"10001", "10001", "01010", "00100", "00100", "00100", "00100"},
	'Z': {"11111", "00001", "00010", "00100", "01000", "10000", "11111"},
	'.': {"00000", "00000", "00000", "00000", "00000", "01100", "01100"},
	',': {"00000", "00000", "00000", "00000", "01100", "00100", "01000"},
	'-': {"00000", "00000", "00000", "11111", "00000", "00000", "00000"},
	'_': {"00000", "00000", "00000", "00000", "00000", "00000", "11111"},
	'/': {"00001", "00010", "00010", "00100", "01000", "01000", "10000"},
	'=': {"00000", "11111", "00000", "00000", "11111", "00000", "00000"},
	':': {"00000", "01100", "01100", "00000", "01100", "01100", "00000"},
	'(': {"00010", "00100", "01000", "01000", "01000", "00100", "00010"},
	')': {"01000", "00100", "00010", "00010", "00010", "00100", "01000"},
	'%': {"11001", "11010", "00010", "00100", "01000", "01011", "10011"},
}

type config struct {
	inputPath     string
	outputDir     string
	shardCount    int64
	injectSize    int
	blockSize     int
	maxRows       int
	chainID       uint64
	matcherMode   string
	includeAllTxs bool
}

type queuedTx struct {
	source int64
	order  int
	intent *intent.PaymentIntent
}

type orderedIntent struct {
	order  int
	intent intent.PaymentIntent
}

type pairKey struct {
	lower  int64
	higher int64
}

type directionKey struct {
	source      int64
	destination int64
}

type countStats struct {
	totalCTX       int
	fullyMatched   int
	totalValue     *big.Int
	matchedValue   *big.Int
	partialMatched int
}

type directionStats struct {
	totalCTX   int
	totalValue *big.Int
}

type heightStats struct {
	height       int
	injection    int
	rawRowsSeen  int
	totalTxs     int
	totalCTX     int
	fullyMatched int
	totalValue   *big.Int
	matchedValue *big.Int
	pairs        map[pairKey]*countStats
}

type runStats struct {
	rawRowsProcessed int
	queuedTxs        int
	ctxCreated       int
	invalidRows      int
	invalidFrom      int
	invalidTo        int
	invalidValue     int
	noRecipient      int
	zeroValue        int
	innerShard       int
	sameParticipant  int
}

func main() {
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "exp1 failed: %v\n", err)
		os.Exit(1)
	}
}

func parseFlags() config {
	cfg := config{}
	flag.StringVar(&cfg.inputPath, "input", "data/25250000to25499999_BlockTransaction.csv", "Ethereum transaction CSV path")
	flag.StringVar(&cfg.outputDir, "output", "pre_exp_scipts/outputs/exp1_real_workload", "output directory")
	flag.Int64Var(&cfg.shardCount, "shards", 8, "number of shards")
	flag.IntVar(&cfg.injectSize, "inject-size", 10000, "valid transactions injected per round")
	flag.IntVar(&cfg.blockSize, "block-size", 2000, "transactions per shard block")
	flag.IntVar(&cfg.maxRows, "max-rows", 300000, "maximum valid transactions to collect after filtering invalid rows")
	flag.Uint64Var(&cfg.chainID, "chain-id", 1, "chain ID used to derive deterministic intent IDs")
	mode := flag.String("matcher-mode", string(matcher.FullMode), "matcher mode: full, best_fit, exact_only, fifo_split")
	flag.BoolVar(&cfg.includeAllTxs, "include-all-txs-in-block-capacity", true, "count all valid sender transactions toward block capacity, even if they cannot become CTX")
	flag.Parse()
	cfg.matcherMode = normalizeMatcherMode(*mode)
	return cfg
}

func normalizeMatcherMode(mode string) string {
	if mode == fifoSplitMode {
		return fifoSplitMode
	}
	return string(matcher.NormalizeMode(matcher.Mode(mode)))
}

func run(cfg config) error {
	if cfg.shardCount <= 1 {
		return fmt.Errorf("shards must be greater than 1")
	}
	if cfg.injectSize <= 0 {
		return fmt.Errorf("inject-size must be positive")
	}
	if cfg.blockSize <= 0 {
		return fmt.Errorf("block-size must be positive")
	}
	if cfg.maxRows <= 0 {
		return fmt.Errorf("max-rows must be positive")
	}
	if err := os.MkdirAll(cfg.outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	file, err := os.Open(cfg.inputPath)
	if err != nil {
		return fmt.Errorf("open input csv: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	if _, err := reader.Read(); err != nil {
		return fmt.Errorf("read csv header: %w", err)
	}

	queues := make([][]queuedTx, cfg.shardCount)
	nonces := make(map[account.Address]uint64)
	stats := runStats{}
	directions := allDirectionStats(cfg.shardCount)
	heights := make([]heightStats, 0, cfg.maxRows/cfg.injectSize+4)
	batch := make([]queuedTx, 0, cfg.injectSize)
	injection := 0
	rawRowsSeen := 0
	validTxSeen := 0

	for validTxSeen < cfg.maxRows {
		row, err := reader.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read csv row %d: %w", rawRowsSeen+2, err)
		}
		rawRowsSeen++
		stats.rawRowsProcessed++

		item, sourceShard, ok := parseRow(row, cfg, nonces, directions, &stats)
		if ok {
			if cfg.includeAllTxs || item.intent != nil {
				if sourceShard < 0 || sourceShard >= cfg.shardCount {
					return fmt.Errorf("source shard out of range: %d", sourceShard)
				}
				item.source = sourceShard
				item.order = validTxSeen
				batch = append(batch, item)
				stats.queuedTxs++
				validTxSeen++
			}
		}

		if len(batch) == cfg.injectSize {
			injection++
			enqueueBatch(queues, batch, cfg.shardCount)
			batch = batch[:0]
			height, err := produceHeight(cfg, queues, len(heights)+1, injection, rawRowsSeen)
			if err != nil {
				return err
			}
			heights = append(heights, height)
		}
	}

	if len(batch) > 0 {
		injection++
		enqueueBatch(queues, batch, cfg.shardCount)
		height, err := produceHeight(cfg, queues, len(heights)+1, injection, rawRowsSeen)
		if err != nil {
			return err
		}
		heights = append(heights, height)
	}

	for queuesHaveTxs(queues) {
		height, err := produceHeight(cfg, queues, len(heights)+1, injection, rawRowsSeen)
		if err != nil {
			return err
		}
		heights = append(heights, height)
	}

	aggregate := aggregatePairs(cfg.shardCount, heights)
	if err := writeHeightCSV(filepath.Join(cfg.outputDir, "height_matching_rate.csv"), heights); err != nil {
		return err
	}
	if err := writePairHeightCSV(filepath.Join(cfg.outputDir, "pair_height_matching_rate.csv"), cfg.shardCount, heights); err != nil {
		return err
	}
	if err := writePairAggregateCSV(filepath.Join(cfg.outputDir, "pair_aggregate_matching_rate.csv"), aggregate); err != nil {
		return err
	}
	if err := writeDirectionalPairCSV(filepath.Join(cfg.outputDir, "directional_pair_ctx_counts.csv"), directions); err != nil {
		return err
	}
	if err := writeBidirectionalPairCSV(filepath.Join(cfg.outputDir, "bidirectional_pair_ctx_balance.csv"), directions, cfg.shardCount); err != nil {
		return err
	}
	if err := writeRunSummary(filepath.Join(cfg.outputDir, "run_summary.csv"), cfg, stats, heights, aggregate); err != nil {
		return err
	}
	if err := writeGlobalBarChart(filepath.Join(cfg.outputDir, "global_height_matching_rate.png"), heights); err != nil {
		return err
	}
	if err := writePairBarChart(filepath.Join(cfg.outputDir, "pair_aggregate_matching_rate.png"), aggregate); err != nil {
		return err
	}
	if err := writePairHeatmap(filepath.Join(cfg.outputDir, "pair_height_matching_rate_heatmap.png"), cfg.shardCount, heights); err != nil {
		return err
	}

	fmt.Printf("exp1 completed\n")
	fmt.Printf("output_dir=%s\n", cfg.outputDir)
	fmt.Printf("raw_rows=%d valid_txs=%d ctx=%d heights=%d\n", stats.rawRowsProcessed, stats.queuedTxs, stats.ctxCreated, len(heights))
	return nil
}

func parseRow(
	row []string,
	cfg config,
	nonces map[account.Address]uint64,
	directions map[directionKey]*directionStats,
	stats *runStats,
) (queuedTx, int64, bool) {
	if len(row) <= 8 {
		stats.invalidRows++
		return queuedTx{}, 0, false
	}

	sender, err := utils.Hex2Addr(row[3])
	if err != nil {
		stats.invalidFrom++
		return queuedTx{}, 0, false
	}
	source := partition.DefaultAccountLoc(sender, cfg.shardCount)

	value := new(big.Int)
	if _, ok := value.SetString(strings.TrimSpace(row[8]), 10); !ok {
		stats.invalidValue++
		return queuedTx{}, source, false
	}

	toField := strings.TrimSpace(row[4])
	hasRecipient := toField != "" && !strings.EqualFold(toField, "none")
	if !hasRecipient {
		stats.noRecipient++
		return queuedTx{}, source, false
	}

	recipient, err := utils.Hex2Addr(toField)
	if err != nil {
		stats.invalidTo++
		return queuedTx{}, source, false
	}
	if sender == recipient {
		stats.sameParticipant++
		return queuedTx{}, source, false
	}
	if value.Sign() <= 0 {
		stats.zeroValue++
		return queuedTx{}, source, false
	}

	destination := partition.DefaultAccountLoc(recipient, cfg.shardCount)
	if source == destination {
		stats.innerShard++
		return queuedTx{source: source}, source, true
	}

	direction := directions[directionKey{source: source, destination: destination}]
	direction.totalCTX++
	direction.totalValue.Add(direction.totalValue, value)

	payment := intent.PaymentIntent{
		Version:          intent.CurrentVersion,
		ChainID:          cfg.chainID,
		Sender:           sender,
		Recipient:        recipient,
		SourceShard:      source,
		DestinationShard: destination,
		AssetID:          intent.NativeAssetID,
		Amount:           new(big.Int).Set(value),
		Nonce:            nonces[sender],
		ExpiryEpoch:      maxUint64,
	}
	nonces[sender]++
	stats.ctxCreated++
	return queuedTx{source: source, intent: &payment}, source, true
}

func enqueueBatch(queues [][]queuedTx, batch []queuedTx, shardCount int64) {
	for _, item := range batch {
		shard := item.source
		if shard >= 0 && shard < shardCount {
			queues[shard] = append(queues[shard], item)
		}
	}
}

func produceHeight(cfg config, queues [][]queuedTx, height, injection, rawRowsSeen int) (heightStats, error) {
	orderedIntents := make([]orderedIntent, 0)
	totalTxs := 0
	for shard := range queues {
		take := minInt(cfg.blockSize, len(queues[shard]))
		blockTxs := queues[shard][:take]
		queues[shard] = queues[shard][take:]
		totalTxs += len(blockTxs)
		for _, tx := range blockTxs {
			if tx.intent != nil {
				orderedIntents = append(orderedIntents, orderedIntent{
					order:  tx.order,
					intent: tx.intent.Clone(),
				})
			}
		}
	}
	sort.Slice(orderedIntents, func(i, j int) bool {
		return orderedIntents[i].order < orderedIntents[j].order
	})
	intents := make([]intent.PaymentIntent, 0, len(orderedIntents))
	for _, item := range orderedIntents {
		intents = append(intents, item.intent)
	}

	pairStats := allPairStats(cfg.shardCount)
	global := countStats{totalValue: new(big.Int), matchedValue: new(big.Int)}
	if len(intents) > 0 {
		results, err := matchIntents(intents, cfg.matcherMode)
		if err != nil {
			return heightStats{}, fmt.Errorf("match height %d: %w", height, err)
		}
		for _, result := range results {
			key := makePairKey(result.Intent.SourceShard, result.Intent.DestinationShard)
			ps := pairStats[key]
			ps.totalCTX++
			ps.totalValue.Add(ps.totalValue, result.Intent.Amount)
			global.totalCTX++
			global.totalValue.Add(global.totalValue, result.Intent.Amount)
			if result.MatchedAmount.Sign() > 0 && result.MatchedAmount.Cmp(result.Intent.Amount) < 0 {
				ps.partialMatched++
				global.partialMatched++
			}
			if result.MatchedAmount.Cmp(result.Intent.Amount) == 0 {
				ps.fullyMatched++
				global.fullyMatched++
			}
			ps.matchedValue.Add(ps.matchedValue, result.MatchedAmount)
			global.matchedValue.Add(global.matchedValue, result.MatchedAmount)
		}
	}

	return heightStats{
		height:       height,
		injection:    injection,
		rawRowsSeen:  rawRowsSeen,
		totalTxs:     totalTxs,
		totalCTX:     global.totalCTX,
		fullyMatched: global.fullyMatched,
		totalValue:   global.totalValue,
		matchedValue: global.matchedValue,
		pairs:        pairStats,
	}, nil
}

type fifoWorkItem struct {
	payment   intent.PaymentIntent
	remaining *big.Int
	matched   *big.Int
}

type fifoGroup struct {
	lowerToHigher []*fifoWorkItem
	higherToLower []*fifoWorkItem
}

func matchIntents(payments []intent.PaymentIntent, mode string) ([]model.IntentResult, error) {
	if mode != fifoSplitMode {
		output, err := matcher.MatchWithMode(payments, matcher.Mode(mode))
		if err != nil {
			return nil, err
		}
		return output.Results, nil
	}
	return matchFIFOSplit(payments)
}

func matchFIFOSplit(payments []intent.PaymentIntent) ([]model.IntentResult, error) {
	items := make([]*fifoWorkItem, 0, len(payments))
	groups := make(map[pairKey]*fifoGroup)

	for idx, payment := range payments {
		if payment.Amount == nil || payment.Amount.Sign() <= 0 {
			return nil, fmt.Errorf("invalid payment amount at index %d", idx)
		}
		if payment.SourceShard == payment.DestinationShard {
			return nil, fmt.Errorf("same-shard payment at index %d", idx)
		}

		item := &fifoWorkItem{
			payment:   payment.Clone(),
			remaining: new(big.Int).Set(payment.Amount),
			matched:   new(big.Int),
		}
		items = append(items, item)

		key := makePairKey(payment.SourceShard, payment.DestinationShard)
		group := groups[key]
		if group == nil {
			group = &fifoGroup{}
			groups[key] = group
		}

		if payment.SourceShard < payment.DestinationShard {
			matchFIFOItem(item, &group.higherToLower)
			if item.remaining.Sign() > 0 {
				group.lowerToHigher = append(group.lowerToHigher, item)
			}
			continue
		}

		matchFIFOItem(item, &group.lowerToHigher)
		if item.remaining.Sign() > 0 {
			group.higherToLower = append(group.higherToLower, item)
		}
	}

	results := make([]model.IntentResult, 0, len(items))
	for _, item := range items {
		result, err := model.NewIntentResult(item.payment, item.matched)
		if err != nil {
			return nil, fmt.Errorf("build fifo result: %w", err)
		}
		results = append(results, result)
	}
	return results, nil
}

func matchFIFOItem(item *fifoWorkItem, counters *[]*fifoWorkItem) {
	for item.remaining.Sign() > 0 && len(*counters) > 0 {
		counter := (*counters)[0]
		amount := minBig(item.remaining, counter.remaining)

		item.remaining.Sub(item.remaining, amount)
		item.matched.Add(item.matched, amount)
		counter.remaining.Sub(counter.remaining, amount)
		counter.matched.Add(counter.matched, amount)

		if counter.remaining.Sign() == 0 {
			*counters = (*counters)[1:]
		}
	}
}

func queuesHaveTxs(queues [][]queuedTx) bool {
	for _, queue := range queues {
		if len(queue) > 0 {
			return true
		}
	}
	return false
}

func allPairStats(shardCount int64) map[pairKey]*countStats {
	stats := make(map[pairKey]*countStats)
	for lower := int64(0); lower < shardCount; lower++ {
		for higher := lower + 1; higher < shardCount; higher++ {
			stats[pairKey{lower: lower, higher: higher}] = &countStats{
				totalValue:   new(big.Int),
				matchedValue: new(big.Int),
			}
		}
	}
	return stats
}

func allDirectionStats(shardCount int64) map[directionKey]*directionStats {
	stats := make(map[directionKey]*directionStats)
	for source := int64(0); source < shardCount; source++ {
		for destination := int64(0); destination < shardCount; destination++ {
			if source == destination {
				continue
			}
			stats[directionKey{source: source, destination: destination}] = &directionStats{
				totalValue: new(big.Int),
			}
		}
	}
	return stats
}

func aggregatePairs(shardCount int64, heights []heightStats) map[pairKey]*countStats {
	aggregate := allPairStats(shardCount)
	for _, height := range heights {
		for key, stat := range height.pairs {
			dst := aggregate[key]
			dst.totalCTX += stat.totalCTX
			dst.fullyMatched += stat.fullyMatched
			dst.partialMatched += stat.partialMatched
			dst.totalValue.Add(dst.totalValue, stat.totalValue)
			dst.matchedValue.Add(dst.matchedValue, stat.matchedValue)
		}
	}
	return aggregate
}

func makePairKey(source, destination int64) pairKey {
	if source < destination {
		return pairKey{lower: source, higher: destination}
	}
	return pairKey{lower: destination, higher: source}
}

func sortedPairKeys(stats map[pairKey]*countStats) []pairKey {
	keys := make([]pairKey, 0, len(stats))
	for key := range stats {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].lower != keys[j].lower {
			return keys[i].lower < keys[j].lower
		}
		return keys[i].higher < keys[j].higher
	})
	return keys
}

func sortedDirectionKeys(stats map[directionKey]*directionStats) []directionKey {
	keys := make([]directionKey, 0, len(stats))
	for key := range stats {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].source != keys[j].source {
			return keys[i].source < keys[j].source
		}
		return keys[i].destination < keys[j].destination
	})
	return keys
}

func writeHeightCSV(path string, heights []heightStats) error {
	file, writer, err := createCSV(path)
	if err != nil {
		return err
	}
	defer file.Close()
	defer writer.Flush()

	if err := writer.Write([]string{
		"block_height", "injection_round", "raw_rows_seen", "total_txs_in_blocks",
		"total_ctx", "fully_matched_ctx", "matching_rate", "total_ctx_value",
		"matched_value", "value_matching_rate",
	}); err != nil {
		return fmt.Errorf("write height header: %w", err)
	}
	for _, item := range heights {
		if err := writer.Write([]string{
			strconv.Itoa(item.height),
			strconv.Itoa(item.injection),
			strconv.Itoa(item.rawRowsSeen),
			strconv.Itoa(item.totalTxs),
			strconv.Itoa(item.totalCTX),
			strconv.Itoa(item.fullyMatched),
			formatRate(item.fullyMatched, item.totalCTX),
			item.totalValue.String(),
			item.matchedValue.String(),
			formatBigRate(item.matchedValue, item.totalValue),
		}); err != nil {
			return fmt.Errorf("write height row: %w", err)
		}
	}
	return writer.Error()
}

func writePairHeightCSV(path string, shardCount int64, heights []heightStats) error {
	file, writer, err := createCSV(path)
	if err != nil {
		return err
	}
	defer file.Close()
	defer writer.Flush()

	if err := writer.Write([]string{
		"block_height", "lower_shard", "higher_shard", "total_ctx",
		"fully_matched_ctx", "matching_rate", "partial_matched_ctx",
		"total_ctx_value", "matched_value", "value_matching_rate",
	}); err != nil {
		return fmt.Errorf("write pair-height header: %w", err)
	}
	pairs := sortedPairKeys(allPairStats(shardCount))
	for _, height := range heights {
		for _, key := range pairs {
			stat := height.pairs[key]
			if err := writer.Write(pairCSVRow(height.height, key, stat)); err != nil {
				return fmt.Errorf("write pair-height row: %w", err)
			}
		}
	}
	return writer.Error()
}

func writePairAggregateCSV(path string, aggregate map[pairKey]*countStats) error {
	file, writer, err := createCSV(path)
	if err != nil {
		return err
	}
	defer file.Close()
	defer writer.Flush()

	if err := writer.Write([]string{
		"lower_shard", "higher_shard", "total_ctx", "fully_matched_ctx",
		"matching_rate", "partial_matched_ctx", "total_ctx_value",
		"matched_value", "value_matching_rate",
	}); err != nil {
		return fmt.Errorf("write pair aggregate header: %w", err)
	}
	for _, key := range sortedPairKeys(aggregate) {
		stat := aggregate[key]
		if err := writer.Write([]string{
			strconv.FormatInt(key.lower, 10),
			strconv.FormatInt(key.higher, 10),
			strconv.Itoa(stat.totalCTX),
			strconv.Itoa(stat.fullyMatched),
			formatRate(stat.fullyMatched, stat.totalCTX),
			strconv.Itoa(stat.partialMatched),
			stat.totalValue.String(),
			stat.matchedValue.String(),
			formatBigRate(stat.matchedValue, stat.totalValue),
		}); err != nil {
			return fmt.Errorf("write pair aggregate row: %w", err)
		}
	}
	return writer.Error()
}

func writeDirectionalPairCSV(path string, directions map[directionKey]*directionStats) error {
	file, writer, err := createCSV(path)
	if err != nil {
		return err
	}
	defer file.Close()
	defer writer.Flush()

	if err := writer.Write([]string{
		"source_shard", "destination_shard", "direction", "total_ctx", "total_ctx_value",
	}); err != nil {
		return fmt.Errorf("write directional pair header: %w", err)
	}
	for _, key := range sortedDirectionKeys(directions) {
		stat := directions[key]
		if err := writer.Write([]string{
			strconv.FormatInt(key.source, 10),
			strconv.FormatInt(key.destination, 10),
			fmt.Sprintf("S%d->S%d", key.source, key.destination),
			strconv.Itoa(stat.totalCTX),
			stat.totalValue.String(),
		}); err != nil {
			return fmt.Errorf("write directional pair row: %w", err)
		}
	}
	return writer.Error()
}

func writeBidirectionalPairCSV(path string, directions map[directionKey]*directionStats, shardCount int64) error {
	file, writer, err := createCSV(path)
	if err != nil {
		return err
	}
	defer file.Close()
	defer writer.Flush()

	if err := writer.Write([]string{
		"lower_shard", "higher_shard", "lower_to_higher_ctx", "higher_to_lower_ctx",
		"total_ctx", "min_direction_ctx", "max_direction_ctx", "count_balance_ratio",
		"lower_to_higher_value", "higher_to_lower_value", "total_value",
		"min_direction_value", "max_direction_value", "value_balance_ratio",
	}); err != nil {
		return fmt.Errorf("write bidirectional pair header: %w", err)
	}

	for lower := int64(0); lower < shardCount; lower++ {
		for higher := lower + 1; higher < shardCount; higher++ {
			forward := directions[directionKey{source: lower, destination: higher}]
			reverse := directions[directionKey{source: higher, destination: lower}]
			minCount, maxCount := minMaxInt(forward.totalCTX, reverse.totalCTX)
			minValue, maxValue := minMaxBig(forward.totalValue, reverse.totalValue)
			totalValue := new(big.Int).Add(forward.totalValue, reverse.totalValue)
			if err := writer.Write([]string{
				strconv.FormatInt(lower, 10),
				strconv.FormatInt(higher, 10),
				strconv.Itoa(forward.totalCTX),
				strconv.Itoa(reverse.totalCTX),
				strconv.Itoa(forward.totalCTX + reverse.totalCTX),
				strconv.Itoa(minCount),
				strconv.Itoa(maxCount),
				formatRate(minCount, maxCount),
				forward.totalValue.String(),
				reverse.totalValue.String(),
				totalValue.String(),
				minValue.String(),
				maxValue.String(),
				formatBigRate(minValue, maxValue),
			}); err != nil {
				return fmt.Errorf("write bidirectional pair row: %w", err)
			}
		}
	}
	return writer.Error()
}

func pairCSVRow(height int, key pairKey, stat *countStats) []string {
	return []string{
		strconv.Itoa(height),
		strconv.FormatInt(key.lower, 10),
		strconv.FormatInt(key.higher, 10),
		strconv.Itoa(stat.totalCTX),
		strconv.Itoa(stat.fullyMatched),
		formatRate(stat.fullyMatched, stat.totalCTX),
		strconv.Itoa(stat.partialMatched),
		stat.totalValue.String(),
		stat.matchedValue.String(),
		formatBigRate(stat.matchedValue, stat.totalValue),
	}
}

func writeRunSummary(path string, cfg config, stats runStats, heights []heightStats, aggregate map[pairKey]*countStats) error {
	file, writer, err := createCSV(path)
	if err != nil {
		return err
	}
	defer file.Close()
	defer writer.Flush()

	totalCTX := 0
	fullyMatched := 0
	totalValue := new(big.Int)
	matchedValue := new(big.Int)
	for _, stat := range aggregate {
		totalCTX += stat.totalCTX
		fullyMatched += stat.fullyMatched
		totalValue.Add(totalValue, stat.totalValue)
		matchedValue.Add(matchedValue, stat.matchedValue)
	}

	rows := [][]string{
		{"input", cfg.inputPath},
		{"output_dir", cfg.outputDir},
		{"shards", strconv.FormatInt(cfg.shardCount, 10)},
		{"inject_size", strconv.Itoa(cfg.injectSize)},
		{"block_size", strconv.Itoa(cfg.blockSize)},
		{"max_valid_txs", strconv.Itoa(cfg.maxRows)},
		{"matcher_mode", string(cfg.matcherMode)},
		{"raw_rows_processed", strconv.Itoa(stats.rawRowsProcessed)},
		{"valid_txs_queued", strconv.Itoa(stats.queuedTxs)},
		{"block_heights", strconv.Itoa(len(heights))},
		{"ctx_created", strconv.Itoa(stats.ctxCreated)},
		{"total_ctx_in_blocks", strconv.Itoa(totalCTX)},
		{"fully_matched_ctx", strconv.Itoa(fullyMatched)},
		{"global_matching_rate", formatRate(fullyMatched, totalCTX)},
		{"total_ctx_value", totalValue.String()},
		{"matched_value", matchedValue.String()},
		{"value_matching_rate", formatBigRate(matchedValue, totalValue)},
		{"invalid_rows", strconv.Itoa(stats.invalidRows)},
		{"invalid_from", strconv.Itoa(stats.invalidFrom)},
		{"invalid_to", strconv.Itoa(stats.invalidTo)},
		{"invalid_value", strconv.Itoa(stats.invalidValue)},
		{"no_recipient", strconv.Itoa(stats.noRecipient)},
		{"zero_value", strconv.Itoa(stats.zeroValue)},
		{"inner_shard_positive_value", strconv.Itoa(stats.innerShard)},
		{"same_participant", strconv.Itoa(stats.sameParticipant)},
	}
	if err := writer.Write([]string{"metric", "value"}); err != nil {
		return fmt.Errorf("write summary header: %w", err)
	}
	for _, row := range rows {
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("write summary row: %w", err)
		}
	}
	return writer.Error()
}

func createCSV(path string) (*os.File, *csv.Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, fmt.Errorf("create csv dir: %w", err)
	}
	file, err := os.Create(path)
	if err != nil {
		return nil, nil, fmt.Errorf("create csv %s: %w", path, err)
	}
	return file, csv.NewWriter(file), nil
}

func writeGlobalBarChart(path string, heights []heightStats) error {
	values := make([]barValue, 0, len(heights))
	for _, item := range heights {
		values = append(values, barValue{
			label: strconv.Itoa(item.height),
			value: rateFloat(item.fullyMatched, item.totalCTX),
		})
	}
	return writeBarChart(
		path,
		"Full-Match CTX Rate by Block Height",
		"Block height",
		"Matching rate",
		"300K valid Ethereum transfers, 8 shards, 10K injection size, 2K block size per shard",
		values,
	)
}

func writePairBarChart(path string, aggregate map[pairKey]*countStats) error {
	values := make([]barValue, 0, len(aggregate))
	for _, key := range sortedPairKeys(aggregate) {
		stat := aggregate[key]
		values = append(values, barValue{
			label: fmt.Sprintf("S%d-S%d", key.lower, key.higher),
			value: rateFloat(stat.fullyMatched, stat.totalCTX),
		})
	}
	return writeBarChart(
		path,
		"Aggregate Full-Match CTX Rate by Shard Pair",
		"Shard pair",
		"Matching rate",
		"Bidirectional shard-pair aggregates over the same 300K valid-transfer workload",
		values,
	)
}

type barValue struct {
	label string
	value float64
}

func writeBarChart(path, title, xLabel, yLabel, note string, values []barValue) error {
	width := 1500
	height := 900
	left := 130
	right := 70
	top := 125
	bottom := 165
	plotW := width - left - right
	plotH := height - top - bottom
	barSlot := float64(plotW) / float64(maxInt(1, len(values)))
	barW := barSlot * 0.72
	if barW > 26 {
		barW = 26
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	bg := color.RGBA{R: 250, G: 250, B: 248, A: 255}
	axisColor := color.RGBA{R: 31, G: 41, B: 55, A: 255}
	gridColor := color.RGBA{R: 218, G: 224, B: 231, A: 255}
	textColor := color.RGBA{R: 31, G: 41, B: 55, A: 255}
	mutedText := color.RGBA{R: 75, G: 85, B: 99, A: 255}
	barColor := color.RGBA{R: 76, G: 120, B: 168, A: 255}
	barEdge := color.RGBA{R: 43, G: 83, B: 128, A: 255}
	meanColor := color.RGBA{R: 211, G: 114, B: 58, A: 255}
	fillRect(img, 0, 0, width, height, bg)

	drawText(img, left, 35, title, 3, textColor)
	drawText(img, left, 78, note, 2, mutedText)

	for i := 0; i <= 5; i++ {
		tick := float64(i) / 5
		y := top + plotH - int(tick*float64(plotH))
		drawHLine(img, left, left+plotW, y, gridColor)
		drawTextRight(img, left-16, y-8, fmt.Sprintf("%.1f", tick), 2, mutedText)
	}
	drawHLine(img, left, left+plotW, top+plotH, axisColor)
	drawVLine(img, left, top, top+plotH, axisColor)

	mean := 0.0
	for i, item := range values {
		mean += item.value
		x := float64(left) + float64(i)*barSlot + (barSlot-barW)/2
		barH := item.value * float64(plotH)
		y := float64(top+plotH) - barH
		barX := int(x)
		barY := int(y)
		barWidth := maxInt(1, int(barW))
		barHeight := maxInt(1, int(barH))
		fillRect(img, barX, barY, barWidth, barHeight, barColor)
		drawRect(img, barX, barY, barWidth, barHeight, barEdge)
	}
	if len(values) > 0 {
		mean /= float64(len(values))
		meanY := top + plotH - int(mean*float64(plotH))
		drawDashedHLine(img, left, left+plotW, meanY, 12, 7, meanColor)
		drawText(img, left+plotW-250, meanY-28, fmt.Sprintf("MEAN = %.3f", mean), 2, meanColor)
	}

	labelStep := maxInt(1, len(values)/24)
	for i, item := range values {
		if i%labelStep != 0 && i != len(values)-1 {
			continue
		}
		x := int(float64(left) + float64(i)*barSlot + barSlot/2)
		drawTextCenter(img, x, top+plotH+18, item.label, 2, mutedText)
	}

	drawTextCenter(img, left+plotW/2, height-70, xLabel, 3, textColor)
	drawText(img, 18, top+plotH/2-40, yLabel, 3, textColor)
	drawText(img, left, height-28, "Bars show fully matched CTX / total CTX for each block height.", 2, mutedText)

	return writePNG(path, img)
}

func writePairHeatmap(path string, shardCount int64, heights []heightStats) error {
	pairs := sortedPairKeys(allPairStats(shardCount))
	cellW := 22
	cellH := 20
	left := 88
	top := 58
	width := left + maxInt(1, len(heights))*cellW + 48
	height := top + len(pairs)*cellH + 92

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	fillRect(img, 0, 0, width, height, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	for row, key := range pairs {
		y := top + row*cellH
		for col, heightStats := range heights {
			stat := heightStats.pairs[key]
			color := heatColor(rateFloat(stat.fullyMatched, stat.totalCTX))
			x := left + col*cellW
			fillRect(img, x, y, cellW-1, cellH-1, color)
		}
	}
	return writePNG(path, img)
}

func writePNG(path string, img image.Image) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create png %s: %w", path, err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		return fmt.Errorf("encode png %s: %w", path, err)
	}
	return nil
}

func fillRect(img *image.RGBA, x, y, width, height int, c color.RGBA) {
	if width <= 0 || height <= 0 {
		return
	}
	bounds := img.Bounds()
	x0 := maxInt(bounds.Min.X, x)
	y0 := maxInt(bounds.Min.Y, y)
	x1 := minInt(bounds.Max.X, x+width)
	y1 := minInt(bounds.Max.Y, y+height)
	for py := y0; py < y1; py++ {
		for px := x0; px < x1; px++ {
			img.SetRGBA(px, py, c)
		}
	}
}

func drawHLine(img *image.RGBA, x0, x1, y int, c color.RGBA) {
	fillRect(img, x0, y, x1-x0+1, 1, c)
}

func drawVLine(img *image.RGBA, x, y0, y1 int, c color.RGBA) {
	fillRect(img, x, y0, 1, y1-y0+1, c)
}

func drawDashedHLine(img *image.RGBA, x0, x1, y, dash, gap int, c color.RGBA) {
	for x := x0; x <= x1; x += dash + gap {
		drawHLine(img, x, minInt(x+dash, x1), y, c)
	}
}

func drawRect(img *image.RGBA, x, y, width, height int, c color.RGBA) {
	if width <= 0 || height <= 0 {
		return
	}
	drawHLine(img, x, x+width-1, y, c)
	drawHLine(img, x, x+width-1, y+height-1, c)
	drawVLine(img, x, y, y+height-1, c)
	drawVLine(img, x+width-1, y, y+height-1, c)
}

func drawText(img *image.RGBA, x, y int, text string, scale int, c color.RGBA) {
	if scale <= 0 {
		scale = 1
	}
	cursor := x
	for _, r := range strings.ToUpper(text) {
		if r == ' ' {
			cursor += 4 * scale
			continue
		}
		pattern, ok := bitmapFont[r]
		if !ok {
			pattern = bitmapFont['?']
		}
		for row, bits := range pattern {
			for col, bit := range bits {
				if bit == '1' {
					fillRect(img, cursor+col*scale, y+row*scale, scale, scale, c)
				}
			}
		}
		cursor += 6 * scale
	}
}

func drawTextRight(img *image.RGBA, x, y int, text string, scale int, c color.RGBA) {
	drawText(img, x-textWidth(text, scale), y, text, scale, c)
}

func drawTextCenter(img *image.RGBA, x, y int, text string, scale int, c color.RGBA) {
	drawText(img, x-textWidth(text, scale)/2, y, text, scale, c)
}

func textWidth(text string, scale int) int {
	if scale <= 0 {
		scale = 1
	}
	width := 0
	for _, r := range strings.ToUpper(text) {
		if r == ' ' {
			width += 4 * scale
			continue
		}
		width += 6 * scale
	}
	if width > 0 {
		width -= scale
	}
	return width
}

func heatColor(value float64) color.RGBA {
	value = clamp01(value)
	low := [3]int{239, 246, 255}
	high := [3]int{37, 99, 235}
	r := int(float64(low[0]) + value*float64(high[0]-low[0]))
	g := int(float64(low[1]) + value*float64(high[1]-low[1]))
	b := int(float64(low[2]) + value*float64(high[2]-low[2]))
	return color.RGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 255}
}

func formatRate(numerator, denominator int) string {
	return fmt.Sprintf("%.6f", rateFloat(numerator, denominator))
}

func rateFloat(numerator, denominator int) float64 {
	if denominator == 0 {
		return 0
	}
	return clamp01(float64(numerator) / float64(denominator))
}

func formatBigRate(numerator, denominator *big.Int) string {
	if denominator == nil || denominator.Sign() == 0 {
		return "0.000000"
	}
	n := new(big.Rat).SetInt(numerator)
	d := new(big.Rat).SetInt(denominator)
	return fmt.Sprintf("%.6f", ratFloat(new(big.Rat).Quo(n, d)))
}

func ratFloat(value *big.Rat) float64 {
	out, _ := value.Float64()
	return clamp01(out)
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func minMaxInt(a, b int) (int, int) {
	if a < b {
		return a, b
	}
	return b, a
}

func minMaxBig(a, b *big.Int) (*big.Int, *big.Int) {
	if a.Cmp(b) <= 0 {
		return new(big.Int).Set(a), new(big.Int).Set(b)
	}
	return new(big.Int).Set(b), new(big.Int).Set(a)
}

func minBig(a, b *big.Int) *big.Int {
	if a.Cmp(b) <= 0 {
		return new(big.Int).Set(a)
	}
	return new(big.Int).Set(b)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
