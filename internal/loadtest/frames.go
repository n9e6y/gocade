package loadtest

import (
	"bytes"
	"math"
	"slices"
	"time"
)

const (
	// maxGap is the longest time between two arrivals that still counts as "the
	// game is running". A longer pause is a menu, a result screen or a wait for
	// an opponent, and says nothing about how well the server keeps its tick.
	maxGap = time.Second

	// maxSamples is how many gaps one client keeps, so a very long run cannot
	// use unbounded memory.
	maxSamples = 100_000
)

// frameStart is what every frame the server draws begins with (after hiding the
// cursor): the "cursor home" escape. Counting it counts frames without
// understanding them.
var frameStart = []byte("\x1b[H")

// frameCounter counts the frames in a byte stream, and records how long passed
// between arrivals. It is used by one goroutine only (the client's reader).
type frameCounter struct {
	tail   []byte    // the last bytes of the previous chunk, in case frameStart was cut in two
	last   time.Time // when a chunk with a frame in it last arrived; zero before the first
	frames uint64
	gaps   []time.Duration
}

// Feed takes the next chunk of the stream, which arrived at now. TCP may cut the
// stream anywhere, so a frameStart split across two chunks is still found.
//
// Several frames can arrive in one read when a client is behind. They count as
// several frames but one arrival, so they add one gap, not zero-length ones.
func (c *frameCounter) Feed(chunk []byte, now time.Time) {
	data := append(c.tail, chunk...)
	// Keep the end of the data for next time: just short of a whole marker, so
	// a marker is never counted twice.
	c.tail = slices.Clone(data[max(0, len(data)-(len(frameStart)-1)):])

	n := bytes.Count(data, frameStart)
	if n == 0 {
		return
	}
	c.frames += uint64(n)

	if !c.last.IsZero() {
		if gap := now.Sub(c.last); gap <= maxGap && len(c.gaps) < maxSamples {
			c.gaps = append(c.gaps, gap)
		}
	}
	c.last = now
}

// Frames is how many frames have been seen.
func (c *frameCounter) Frames() uint64 { return c.frames }

// Gaps is the time between successive arrivals of frames, in arrival order.
func (c *frameCounter) Gaps() []time.Duration { return c.gaps }

// percentile returns the p-th percentile (0 to 100) of an ascending slice, by
// the nearest-rank method: the smallest sample that at least p percent of the
// samples are at or below. It is 0 for no samples.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	rank := int(math.Ceil(p / 100 * float64(len(sorted))))
	return sorted[min(max(rank, 1), len(sorted))-1]
}
