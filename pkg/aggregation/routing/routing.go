// Package routing controls post-commit aggregation using online source-block statistics.
// It is used only from the node's serial message loop.
package routing

import (
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/HuangLab-SYSU/block-emulator-x/pkg/aggregation/model"
)

type Config struct {
	Enabled               bool  `json:"enabled"                 yaml:"enabled"`
	FreezeHighMS          int64 `json:"freeze_high_ms"          yaml:"freeze_high_ms"`
	FreezeLowMS           int64 `json:"freeze_low_ms"           yaml:"freeze_low_ms"`
	ObservationWindowMS   int64 `json:"observation_window_ms"   yaml:"observation_window_ms"`
	CooldownMS            int64 `json:"cooldown_ms"             yaml:"cooldown_ms"`
	ReductionPercent      int   `json:"reduction_percent"       yaml:"reduction_percent"`
	MinDestinationPercent int   `json:"min_destination_percent" yaml:"min_destination_percent"`
}

func (c Config) WithDefaults() Config {
	if c.FreezeHighMS == 0 {
		c.FreezeHighMS = 2000
	}

	if c.FreezeLowMS == 0 {
		c.FreezeLowMS = 500
	}

	if c.ObservationWindowMS == 0 {
		c.ObservationWindowMS = 10000
	}

	if c.CooldownMS == 0 {
		c.CooldownMS = 10000
	}

	if c.ReductionPercent == 0 {
		c.ReductionPercent = 25
	}

	if c.MinDestinationPercent == 0 {
		c.MinDestinationPercent = 25
	}

	return c
}

func (c Config) Validate() error {
	if c.FreezeLowMS < 0 || c.FreezeHighMS <= c.FreezeLowMS ||
		c.ObservationWindowMS <= 0 || c.CooldownMS <= 0 ||
		c.ReductionPercent < 1 || c.ReductionPercent > 100 ||
		c.MinDestinationPercent < 1 || c.MinDestinationPercent > 100 {
		return fmt.Errorf("invalid adaptive routing thresholds, durations or percentages")
	}

	return nil
}

type Policy struct {
	Version        uint64
	Destinations   []int64
	IssuedAt       time.Time
	FrozenWait     time.Duration
	Reason         string
	PaymentCount   []int64
	RecipientCount []int64
}

func (p Policy) Clone() Policy {
	p.Destinations = slices.Clone(p.Destinations)
	p.PaymentCount = slices.Clone(p.PaymentCount)
	p.RecipientCount = slices.Clone(p.RecipientCount)

	return p
}

type Table struct {
	shardCount int64
	policy     Policy
}

func NewTable(shardCount int64) *Table {
	destinations := make([]int64, shardCount)
	for i := range destinations {
		destinations[i] = int64(i)
	}

	return &Table{shardCount: shardCount, policy: Policy{Destinations: destinations}}
}

func (t *Table) Snapshot() Policy { return t.policy.Clone() }

func (p Policy) Aggregates(destination int64) bool {
	return slices.Contains(p.Destinations, destination)
}

// Apply tolerates stale/replayed broadcasts and refuses a conflicting version.
func (t *Table) Apply(p Policy) error {
	if p.Version < t.policy.Version {
		return nil
	}

	if len(p.Destinations) == 0 ||
		!sort.SliceIsSorted(p.Destinations, func(i, j int) bool { return p.Destinations[i] < p.Destinations[j] }) {
		return fmt.Errorf("routing destinations must be nonempty and sorted")
	}

	for i, id := range p.Destinations {
		if id < 0 || id >= t.shardCount || (i > 0 && id == p.Destinations[i-1]) {
			return fmt.Errorf("invalid routing destination: %d", id)
		}
	}

	if p.Version == t.policy.Version {
		if !slices.Equal(p.Destinations, t.policy.Destinations) {
			return fmt.Errorf("conflicting routing policy version %d", p.Version)
		}

		return nil
	}

	t.policy = p.Clone()

	return nil
}

type observation struct {
	at    time.Time
	stats []model.DestinationStat
}

type Controller struct {
	cfg         Config
	table       *Table
	lastHeight  map[int64]uint64
	samples     []observation
	lastChange  time.Time
	lastHealthy time.Time
	healthy     int
}

func NewController(shardCount int64, cfg Config) *Controller {
	return &Controller{cfg: cfg.WithDefaults(), table: NewTable(shardCount), lastHeight: make(map[int64]uint64)}
}

func (c *Controller) Current() Policy { return c.table.Snapshot() }

// Observe only uses received source-block summaries, including destinations currently
// following Relay2. Repeated and older receipts cannot increase observed volume.
func (c *Controller) Observe(r model.FinalizedBlockReceipt, now time.Time) error {
	if r.ShardID < 0 || r.ShardID >= c.table.shardCount {
		return fmt.Errorf("invalid telemetry source shard")
	}

	if r.Height <= c.lastHeight[r.ShardID] {
		return nil
	}

	seen := make(map[int64]bool)
	for _, s := range r.DestinationStats {
		if s.ShardID < 0 || s.ShardID >= c.table.shardCount || s.ShardID == r.ShardID || seen[s.ShardID] ||
			s.PaymentCount < 0 || s.RecipientCount < 0 || s.RecipientCount > s.PaymentCount {
			return fmt.Errorf("invalid destination telemetry for shard %d", s.ShardID)
		}

		seen[s.ShardID] = true
	}

	c.lastHeight[r.ShardID] = r.Height
	c.samples = append(c.samples, observation{at: now, stats: slices.Clone(r.DestinationStats)})

	return nil
}

// Evaluate changes only future source-block delivery decisions. Frozen receipts
// already in Beacon are never discarded or redirected by this control loop.
func (c *Controller) Evaluate(now time.Time, frozenWait time.Duration) (Policy, bool) {
	cutoff := now.Add(-time.Duration(c.cfg.ObservationWindowMS) * time.Millisecond)

	first := 0
	for first < len(c.samples) && c.samples[first].at.Before(cutoff) {
		first++
	}

	c.samples = slices.Clone(c.samples[first:])
	counts, recipients := make([]int64, c.table.shardCount), make([]int64, c.table.shardCount)

	var total int64

	for _, sample := range c.samples {
		for _, s := range sample.stats {
			counts[s.ShardID] += s.PaymentCount
			recipients[s.ShardID] += s.RecipientCount
			total += s.PaymentCount
		}
	}

	if total == 0 {
		c.healthy = 0
		return Policy{}, false
	}

	if frozenWait <= time.Duration(c.cfg.FreezeLowMS)*time.Millisecond {
		if c.lastHealthy.IsZero() || now.Sub(c.lastHealthy) >= time.Second {
			c.healthy++
			c.lastHealthy = now
		}
	} else {
		c.healthy = 0
		c.lastHealthy = time.Time{}
	}

	if !c.lastChange.IsZero() && now.Sub(c.lastChange) < time.Duration(c.cfg.CooldownMS)*time.Millisecond {
		return Policy{}, false
	}

	current := c.Current()
	step := max(1, (int(c.table.shardCount)*c.cfg.ReductionPercent+99)/100)
	minimum := max(1, (int(c.table.shardCount)*c.cfg.MinDestinationPercent+99)/100)

	size, reason := len(current.Destinations), ""
	if frozenWait > time.Duration(c.cfg.FreezeHighMS)*time.Millisecond && size > minimum {
		size, reason = max(minimum, size-step), "frozen_queue_overload"
	} else if c.healthy >= 3 && size < int(c.table.shardCount) {
		size, reason = min(int(c.table.shardCount), size+step), "frozen_queue_recovered"
	} else {
		return Policy{}, false
	}
	// Rank a conservative block-local account-update saving fraction. No unseen
	// dataset suffix or offline destination ranking is available here.
	less := func(a, b int64) bool {
		score := func(id int64) float64 {
			if counts[id] == 0 {
				return -1
			}

			return float64(counts[id]-recipients[id]) / float64(counts[id])
		}
		if score(a) != score(b) {
			return score(a) > score(b)
		}

		if counts[a] != counts[b] {
			return counts[a] > counts[b]
		}

		return a < b
	}

	var candidates []int64
	if size < len(current.Destinations) {
		candidates = slices.Clone(current.Destinations)
		sort.Slice(candidates, func(i, j int) bool { return less(candidates[i], candidates[j]) })
		candidates = candidates[:size]
	} else {
		for id := range c.table.shardCount {
			if !current.Aggregates(id) {
				candidates = append(candidates, id)
			}
		}

		sort.Slice(candidates, func(i, j int) bool { return less(candidates[i], candidates[j]) })
		candidates = append(slices.Clone(current.Destinations), candidates[:size-len(current.Destinations)]...)
	}

	slices.Sort(candidates)
	p := Policy{
		Version: current.Version + 1, Destinations: candidates, IssuedAt: now, FrozenWait: frozenWait,
		Reason: reason, PaymentCount: counts, RecipientCount: recipients,
	}
	// The generated policy is canonical by construction.
	c.table.policy = p.Clone()
	c.lastChange, c.lastHealthy, c.healthy = now, time.Time{}, 0

	return p, true
}
