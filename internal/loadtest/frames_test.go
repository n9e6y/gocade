package loadtest

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// frame is what the server sends for one screen: hide the cursor, go home, draw
// the rows, clear below. Only the "go home" part matters to the counter.
const frame = "\x1b[?25l\x1b[H" + "row one\x1b[K\r\nrow two\x1b[K" + "\x1b[J"

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func TestFrameCounter(t *testing.T) {
	t.Parallel()

	type feed struct {
		chunk string
		at    time.Duration
	}
	tests := []struct {
		name       string
		feeds      []feed
		wantFrames uint64
		wantGaps   []time.Duration
	}{
		{"nothing", nil, 0, nil},
		{"text without a frame in it", []feed{{"hello\r\nworld", 0}}, 0, nil},
		{"other escape sequences are not frames", []feed{{"\x1b[0;31mred\x1b[0m\x1b[K\x1b[J", 0}}, 0, nil},
		{"one frame", []feed{{frame, 0}}, 1, nil},
		{"two frames in one read count twice and make one arrival", []feed{{frame + frame, 0}, {frame, 120 * time.Millisecond}}, 3, []time.Duration{120 * time.Millisecond}},
		{"the gap between arrivals is recorded", []feed{
			{frame, 0},
			{frame, 120 * time.Millisecond},
			{frame, 250 * time.Millisecond},
		}, 3, []time.Duration{120 * time.Millisecond, 130 * time.Millisecond}},
		{"a pause longer than a second is not a frame gap, and timing resumes after it", []feed{
			{frame, 0},
			{frame, 5 * time.Second},
			{frame, 5*time.Second + 120*time.Millisecond},
		}, 3, []time.Duration{120 * time.Millisecond}},
		{"a read with no frame does not disturb the timing", []feed{
			{frame, 0},
			{"partial text", 50 * time.Millisecond},
			{frame, 120 * time.Millisecond},
		}, 2, []time.Duration{120 * time.Millisecond}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var c frameCounter
			for _, f := range tt.feeds {
				c.Feed([]byte(f.chunk), t0.Add(f.at))
			}
			if c.Frames() != tt.wantFrames {
				t.Errorf("Frames() = %d, want %d", c.Frames(), tt.wantFrames)
			}
			if !slices.Equal(c.Gaps(), tt.wantGaps) {
				t.Errorf("Gaps() = %v, want %v", c.Gaps(), tt.wantGaps)
			}
		})
	}
}

// TCP may cut the stream anywhere, including inside the marker. Wherever the
// cut falls, the frame is counted once.
func TestFrameCounter_CutAnywhere(t *testing.T) {
	t.Parallel()

	for cut := 0; cut <= len(frame); cut++ {
		var c frameCounter
		c.Feed([]byte(frame[:cut]), t0)
		c.Feed([]byte(frame[cut:]), t0.Add(time.Millisecond))
		if c.Frames() != 1 {
			t.Errorf("cut at %d: Frames() = %d, want 1", cut, c.Frames())
		}
	}

	// And a stream cut into single bytes.
	var c frameCounter
	for i := 0; i < 3; i++ {
		for j := 0; j < len(frame); j++ {
			c.Feed([]byte{frame[j]}, t0)
		}
	}
	if c.Frames() != 3 {
		t.Errorf("byte at a time: Frames() = %d, want 3", c.Frames())
	}
}

// Memory stays bounded however long a run is.
func TestFrameCounter_KeepsAtMostMaxSamples(t *testing.T) {
	t.Parallel()

	var c frameCounter
	for i := 0; i < maxSamples+50; i++ {
		c.Feed([]byte(frame), t0.Add(time.Duration(i)*10*time.Millisecond))
	}
	if got := len(c.Gaps()); got != maxSamples {
		t.Errorf("len(Gaps()) = %d, want %d", got, maxSamples)
	}
	if c.Frames() != uint64(maxSamples+50) {
		t.Errorf("Frames() = %d, want every frame counted even when gaps are not kept", c.Frames())
	}
}

func TestPercentile(t *testing.T) {
	t.Parallel()

	var hundred []time.Duration
	for i := 1; i <= 100; i++ {
		hundred = append(hundred, time.Duration(i)*time.Millisecond)
	}
	ms := time.Millisecond

	tests := []struct {
		name   string
		sorted []time.Duration
		p      float64
		want   time.Duration
	}{
		{"empty", nil, 50, 0},
		{"one sample", []time.Duration{7 * ms}, 99, 7 * ms},
		{"median of a hundred", hundred, 50, 50 * ms},
		{"99th of a hundred", hundred, 99, 99 * ms},
		{"100th is the maximum", hundred, 100, 100 * ms},
		{"0th is the minimum", hundred, 0, 1 * ms},
		{"median of two takes the lower", []time.Duration{1 * ms, 2 * ms}, 50, 1 * ms},
	}
	for _, tt := range tests {
		if got := percentile(tt.sorted, tt.p); got != tt.want {
			t.Errorf("%s: percentile(p=%v) = %v, want %v", tt.name, tt.p, got, tt.want)
		}
	}
}

func TestSummarize(t *testing.T) {
	t.Parallel()

	ms := time.Millisecond
	results := []clientResult{
		{frames: 10, bytes: 1000, gaps: []time.Duration{100 * ms, 120 * ms}},
		{frames: 4, bytes: 400, gaps: []time.Duration{500 * ms}},
		{dialFailed: true},
		{frames: 6, bytes: 600, disconnected: true},
	}
	r := summarize(results, 10*time.Second)

	if r.Clients != 4 || r.DialFailures != 1 || r.Disconnects != 1 {
		t.Errorf("clients/dial failures/disconnects = %d/%d/%d, want 4/1/1", r.Clients, r.DialFailures, r.Disconnects)
	}
	if r.Frames != 20 || r.Bytes != 2000 {
		t.Errorf("frames/bytes = %d/%d, want 20/2000", r.Frames, r.Bytes)
	}
	if r.MinFrames != 4 {
		t.Errorf("MinFrames = %d, want 4 (clients that never connected do not count)", r.MinFrames)
	}
	if r.Gaps.Count != 3 || r.Gaps.P50 != 120*ms || r.Gaps.Max != 500*ms {
		t.Errorf("Gaps = %+v, want 3 samples, median 120ms, max 500ms", r.Gaps)
	}
	if r.Duration != 10*time.Second {
		t.Errorf("Duration = %v", r.Duration)
	}
}

func TestReport_StringSaysWhatHappened(t *testing.T) {
	t.Parallel()

	r := Report{Clients: 100, Frames: 2000, Duration: 10 * time.Second, DialFailures: 2, Disconnects: 1,
		Gaps: GapStats{Count: 5, P50: 121 * time.Millisecond, P99: 130 * time.Millisecond, Max: 400 * time.Millisecond}}
	s := r.String()
	for _, want := range []string{"100 clients", "2000 frames", "2 dial failures", "1 disconnects", "p50 121ms", "p99 130ms", "max 400ms"} {
		if !strings.Contains(s, want) {
			t.Errorf("report does not mention %q:\n%s", want, s)
		}
	}
}
