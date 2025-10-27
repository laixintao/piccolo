package randduration

import (
	"math/rand"
	"time"
)

// RandomDuration returns a random duration between 0 and max (exclusive).
func RandomDuration(max time.Duration) time.Duration {
	if max <= 0 {
		return 0
	}
	// rand.Int63n returns a random int64 in [0, n)
	n := rand.Int63n(int64(max))
	return time.Duration(n)
}
