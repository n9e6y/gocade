// Package loadtest drives many fake players against a running Arena server, to
// show that it holds up and to measure how steadily it delivers frames.
//
// Each client is a real TCP connection that behaves like a person mashing keys:
// it picks a name, picks a game, and presses random arrow keys. It does not
// read the screen. Instead, every second it repeats the keys that take it back
// to the game from wherever it is (the menu, a result screen), which are
// harmless anywhere else. So rounds keep starting and ending, and rooms are
// opened and closed over and over, exercising the whole life cycle under load.
package loadtest

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"
)

const (
	dialTimeout = 5 * time.Second

	defaultKeyEvery    = 100 * time.Millisecond
	defaultRejoinEvery = time.Second
	defaultPick        = "2"
)

// arrows are the escape sequences a terminal sends for the four arrow keys.
var arrows = [...]string{"\x1b[A", "\x1b[B", "\x1b[C", "\x1b[D"}

// Config describes a load test.
type Config struct {
	Addr string // the server, as host:port

	// Rooms is how many rooms to keep busy. Two clients share each room, or one
	// if Bots is set (the other player is then a bot).
	Rooms int
	Bots  bool

	// Pick is the keys that choose the game from the lobby menus: "2" is the
	// second game online; "22" is the second game against a bot. It defaults to
	// "2", and Bots is up to the caller to match with a Pick that asks for a bot.
	Pick string

	Duration    time.Duration // how long to run
	KeyEvery    time.Duration // how often each client presses an arrow key; default 100 ms
	RejoinEvery time.Duration // how often each client repeats Pick; default 1 s
}

func (c Config) validate() error {
	switch {
	case c.Addr == "":
		return errors.New("loadtest: no address")
	case c.Rooms <= 0:
		return fmt.Errorf("loadtest: rooms must be positive, got %d", c.Rooms)
	case c.Duration <= 0:
		return fmt.Errorf("loadtest: duration must be positive, got %v", c.Duration)
	}
	return nil
}

// clients is how many connections the test opens.
func (c Config) clients() int {
	if c.Bots {
		return c.Rooms
	}
	return 2 * c.Rooms
}

// Run opens the clients, lets them play for cfg.Duration (or until ctx is
// cancelled), and reports what they saw. It returns after every goroutine it
// started has exited. The error is only for a bad Config: a client that cannot
// connect or is disconnected shows up in the Report.
func Run(ctx context.Context, cfg Config) (Report, error) {
	if err := cfg.validate(); err != nil {
		return Report{}, err
	}
	if cfg.KeyEvery <= 0 {
		cfg.KeyEvery = defaultKeyEvery
	}
	if cfg.RejoinEvery <= 0 {
		cfg.RejoinEvery = defaultRejoinEvery
	}
	if cfg.Pick == "" {
		cfg.Pick = defaultPick
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.Duration)
	defer cancel()
	start := time.Now()

	results := make([]clientResult, cfg.clients())
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1) // before go
		go func() {
			defer wg.Done()
			results[i] = runClient(ctx, cfg, i+1) // each goroutine writes only its own element
		}()
	}
	wg.Wait()

	return summarize(results, time.Since(start)), nil
}

// runClient is one fake player. It returns when ctx ends, or earlier if the
// connection fails. It owns the reader goroutine it starts, and does not return
// until that has exited.
func runClient(ctx context.Context, cfg Config, id int) clientResult {
	var res clientResult

	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", cfg.Addr)
	if err != nil {
		res.dialFailed = ctx.Err() == nil // running out of time while connecting is not a failure
		return res
	}
	// A blocked Read or Write cannot see ctx, but closing the connection makes
	// it fail. This closes it whenever ctx ends.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	defer conn.Close()

	var counter frameCounter
	var bytesRead uint64
	readerDone := make(chan struct{})
	go func() { // stops when the connection closes: ctx ends, sendKeys returns, or the server hangs up
		defer close(readerDone)
		buf := make([]byte, 32<<10)
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				bytesRead += uint64(n)
				counter.Feed(buf[:n], time.Now())
			}
			if err != nil {
				return
			}
		}
	}()

	sendKeys(ctx, conn, cfg, id, readerDone)

	conn.Close()
	<-readerDone // after this, counter and bytesRead are safe to read
	res.disconnected = ctx.Err() == nil
	res.frames, res.bytes, res.gaps = counter.Frames(), bytesRead, counter.Gaps()
	return res
}

// sendKeys plays until ctx ends, the reader stops (the server hung up), or a
// write fails. It starts with a nickname and the game choice, then presses a
// random arrow every cfg.KeyEvery and repeats the game choice every
// cfg.RejoinEvery.
func sendKeys(ctx context.Context, conn net.Conn, cfg Config, id int, readerDone <-chan struct{}) {
	// Enter finishes the nickname; the keys after it are typed on the menus. The
	// server handles one connection's keys in order, so this cannot arrive
	// scrambled.
	if _, err := fmt.Fprintf(conn, "lt%d\r%s", id, cfg.Pick); err != nil {
		return
	}

	rng := rand.New(rand.NewSource(int64(id))) // a different but repeatable sequence per client
	keys := time.NewTicker(cfg.KeyEvery)
	defer keys.Stop()
	rejoin := time.NewTicker(cfg.RejoinEvery)
	defer rejoin.Stop()

	for {
		var out string
		select {
		case <-ctx.Done():
			return
		case <-readerDone:
			return
		case <-keys.C:
			out = arrows[rng.Intn(len(arrows))]
		case <-rejoin.C:
			// Enter leaves a result screen for the menu; the digits choose the game
			// again. Both do nothing during a game and on a screen that is not
			// waiting for them.
			out = "\r" + cfg.Pick
		}
		if _, err := conn.Write([]byte(out)); err != nil {
			return
		}
	}
}
