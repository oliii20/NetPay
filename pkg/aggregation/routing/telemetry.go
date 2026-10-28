package routing

import "time"

// expire subtracts each observation once. Live samples are not rescanned or
// copied on each decision; occasional compaction bounds retained queue storage.
func (c *Controller) expire(now time.Time) {
	cutoff := now.Add(-time.Duration(c.cfg.ObservationWindowMS) * time.Millisecond)
	for c.sampleHead < len(c.samples) && c.samples[c.sampleHead].at.Before(cutoff) {
		for _, stat := range c.samples[c.sampleHead].stats {
			c.counts[stat.ShardID] -= stat.PaymentCount
			c.recipients[stat.ShardID] -= stat.RecipientCount
			c.total -= stat.PaymentCount
		}

		c.samples[c.sampleHead] = observation{}
		c.sampleHead++
	}

	if c.sampleHead == len(c.samples) {
		c.samples = c.samples[:0]
		c.sampleHead = 0
	} else if c.sampleHead >= 1024 && c.sampleHead >= len(c.samples)/2 {
		remaining := copy(c.samples, c.samples[c.sampleHead:])
		clear(c.samples[remaining:])
		c.samples = c.samples[:remaining]
		c.sampleHead = 0
	}
}
