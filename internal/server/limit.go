package server

import "time"

// keyBudget limits how many keys a client may send: a token bucket. The bucket
// holds up to burst tokens and gains rate tokens every second; each key spends
// one. A client that sends less than rate keys a second never notices it, and
// a flood is cut down to rate a second (after an initial burst) instead of
// being passed on to the lobby, which handles every player's keys on one
// goroutine.
//
// It is pure: the caller says what time it is. Each session has its own, used
// only by its reader goroutine, so it needs no locking.
type keyBudget struct {
	rate   float64   // tokens gained per second
	burst  float64   // most tokens the bucket holds
	tokens float64   // tokens now
	last   time.Time // when tokens was last brought up to date; zero before the first call
}

// newKeyBudget returns a full bucket.
func newKeyBudget(rate float64, burst int) *keyBudget {
	return &keyBudget{rate: rate, burst: float64(burst), tokens: float64(burst)}
}

// allow says how many of n keys arriving at time now may pass, and spends
// that many tokens. The rest are the caller's to drop.
func (b *keyBudget) allow(now time.Time, n int) int {
	if !b.last.IsZero() {
		// A clock that moves backwards gives nothing back (and takes nothing).
		if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
			b.tokens = min(b.burst, b.tokens+elapsed*b.rate)
		}
	}
	if now.After(b.last) {
		b.last = now
	}

	got := min(n, int(b.tokens))
	b.tokens -= float64(got)
	return got
}
