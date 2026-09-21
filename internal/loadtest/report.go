package loadtest

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// GapStats summarizes the time between frames, over every client.
type GapStats struct {
	Count         int // samples
	P50, P99, Max time.Duration
}

// Report is what a load test measured.
type Report struct {
	Clients      int
	DialFailures int           // clients that could not connect
	Disconnects  int           // clients the server hung up on before the run ended
	Frames       uint64        // frames received, over every client
	MinFrames    uint64        // the fewest frames any connected client received
	Bytes        uint64        // bytes received
	Duration     time.Duration // how long the run lasted
	Gaps         GapStats
}

// clientResult is what one client saw.
type clientResult struct {
	dialFailed   bool
	disconnected bool // the connection ended before the run did
	frames       uint64
	bytes        uint64
	gaps         []time.Duration
}

// summarize combines the clients' results into a Report.
func summarize(results []clientResult, elapsed time.Duration) Report {
	r := Report{Clients: len(results), Duration: elapsed}
	var gaps []time.Duration
	first := true
	for _, c := range results {
		if c.dialFailed {
			r.DialFailures++
			continue // it never connected, so it has no frame count to compare
		}
		if c.disconnected {
			r.Disconnects++
		}
		r.Frames += c.frames
		r.Bytes += c.bytes
		if first || c.frames < r.MinFrames {
			r.MinFrames, first = c.frames, false
		}
		gaps = append(gaps, c.gaps...)
	}

	slices.Sort(gaps)
	r.Gaps = GapStats{Count: len(gaps), P50: percentile(gaps, 50), P99: percentile(gaps, 99)}
	if len(gaps) > 0 {
		r.Gaps.Max = gaps[len(gaps)-1]
	}
	return r
}

// String formats the report for a terminal.
func (r Report) String() string {
	secs := max(r.Duration.Seconds(), 0.001)
	connected := max(r.Clients-r.DialFailures, 1)

	var b strings.Builder
	fmt.Fprintf(&b, "%d clients for %v: %d dial failures, %d disconnects\n",
		r.Clients, r.Duration.Round(time.Millisecond), r.DialFailures, r.Disconnects)
	fmt.Fprintf(&b, "received %d frames (%.1f a second per client, fewest for one client: %d) and %.1f MB\n",
		r.Frames, float64(r.Frames)/secs/float64(connected), r.MinFrames, float64(r.Bytes)/1e6)
	fmt.Fprintf(&b, "time between frames over %d samples: p50 %v  p99 %v  max %v",
		r.Gaps.Count, r.Gaps.P50.Round(time.Millisecond), r.Gaps.P99.Round(time.Millisecond), r.Gaps.Max.Round(time.Millisecond))
	return b.String()
}
