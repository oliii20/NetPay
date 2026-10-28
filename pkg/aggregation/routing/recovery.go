package routing

import "time"

// recoveryVolume freezes the first post-reduction N-window volume as a baseline.
// Later observations include every eligible destination, even those using Relay2.
type recoveryVolume struct {
	windowStart     time.Time
	pending         int64
	baseline        int64
	baselineWindows int
	recent          []int64
	recentSum       int64
	recentIndex     int
}

func (r *recoveryVolume) advance(now time.Time, cfg Config) {
	if r.windowStart.IsZero() || now.Before(r.windowStart) {
		return
	}

	width := time.Duration(cfg.RecoveryWindowMS) * time.Millisecond

	completed := int(now.Sub(r.windowStart) / width)
	if completed == 0 {
		return
	}

	r.push(r.pending, cfg.RecoveryWindows)
	// After 2N empty windows both baseline initialization and recent history
	// have converged. Bound work even after a long idle period.
	for range min(completed-1, 2*cfg.RecoveryWindows) {
		r.push(0, cfg.RecoveryWindows)
	}

	r.pending = 0
	r.windowStart = r.windowStart.Add(time.Duration(completed) * width)
}

func (r *recoveryVolume) push(count int64, n int) {
	if r.baselineWindows < n {
		r.baseline += count
		r.baselineWindows++

		return
	}

	if r.recent == nil {
		r.recent = make([]int64, 0, n)
	}

	if len(r.recent) < n {
		r.recent = append(r.recent, count)
		r.recentSum += count
	} else {
		r.recentSum += count - r.recent[r.recentIndex]
		r.recent[r.recentIndex] = count
		r.recentIndex = (r.recentIndex + 1) % n
	}
}

func (r *recoveryVolume) recentTotal() int64 {
	return r.recentSum
}

func (r *recoveryVolume) changed(cfg Config) bool {
	if r.baselineWindows < cfg.RecoveryWindows || len(r.recent) < cfg.RecoveryWindows || r.baseline == 0 {
		return false
	}

	difference := r.recentTotal() - r.baseline
	if difference < 0 {
		difference = -difference
	}
	// Exact strict percentage comparison without overflowing baseline*percent.
	percent := int64(cfg.RecoveryChangePercent)
	threshold := r.baseline/100*percent + r.baseline%100*percent/100

	return difference > threshold
}
