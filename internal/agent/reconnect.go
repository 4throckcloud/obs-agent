package agent

import (
	"math"
	"math/rand"
	"time"
)

const (
	baseDelay   = 1 * time.Second
	maxDelay    = 60 * time.Second
	maxAttempts = 0 // 0 = unlimited
)

// backoff calculates exponential backoff with jitter
func backoff(attempt int) time.Duration {
	// Exponential: 1s, 2s, 4s, 8s, 16s, 32s, 60s (capped)
	delay := float64(baseDelay) * math.Pow(2, float64(attempt))
	if delay > float64(maxDelay) {
		delay = float64(maxDelay)
	}

	// Add jitter: +-25%
	jitter := delay * 0.25 * (rand.Float64()*2 - 1)
	delay += jitter

	return time.Duration(delay)
}
