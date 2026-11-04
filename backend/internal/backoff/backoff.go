// Package backoff holds the retry-delay schedules the daemon uses, so every
// retry loop shares one definition of "doubling, capped" and "fixed table".
package backoff

import "time"

// Table returns the delay for the given consecutive-failure count (1-based)
// from a fixed schedule. A count past the end repeats the last entry; a count
// below 1 gets the first. An empty table yields 0.
func Table(delays []time.Duration, failures int) time.Duration {
	if len(delays) == 0 {
		return 0
	}
	return delays[max(0, min(failures-1, len(delays)-1))]
}

// Doubling returns initial doubled once per failure after the first, capped at
// maxDelay. failures below 2 get initial (itself capped). It never overflows:
// doubling stops as soon as the cap is reached.
func Doubling(initial, maxDelay time.Duration, failures int) time.Duration {
	delay := initial
	for i := 1; i < failures && delay < maxDelay; i++ {
		delay *= 2
	}
	return min(delay, maxDelay)
}

// Double returns delay doubled, capped at maxDelay.
func Double(delay, maxDelay time.Duration) time.Duration {
	return min(delay*2, maxDelay)
}
