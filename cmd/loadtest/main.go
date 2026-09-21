// Command loadtest drives many fake players against a running Arena server and
// prints how it held up. Start the server first (go run -race ./cmd/arena
// -addr :9100), then, for example:
//
//	go run ./cmd/loadtest -addr localhost:9100 -rooms 50 -duration 10s
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/n9e6y/gocade/internal/loadtest"
)

func main() {
	var cfg loadtest.Config
	flag.StringVar(&cfg.Addr, "addr", "localhost:9000", "address of the Arena server")
	flag.IntVar(&cfg.Rooms, "rooms", 50, "how many rooms to keep busy")
	flag.BoolVar(&cfg.Bots, "bots", false, "play each room against a bot (one client per room) instead of two clients")
	flag.StringVar(&cfg.Pick, "pick", "", "menu keys that choose the game (default: 2, Tron; with -bots: 22, Tron against a bot)")
	flag.DurationVar(&cfg.Duration, "duration", 10*time.Second, "how long to run")
	flag.DurationVar(&cfg.KeyEvery, "key-every", 100*time.Millisecond, "how often each client presses an arrow key")
	flag.Parse()

	if cfg.Pick == "" {
		cfg.Pick = "2"
		if cfg.Bots {
			cfg.Pick = "22"
		}
	}

	// Ctrl-C ends the run early and still prints what was measured.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	rep, err := loadtest.Run(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "loadtest:", err)
		stop() // os.Exit skips deferred calls
		os.Exit(2)
	}
	fmt.Println(rep)
	if rep.DialFailures > 0 || rep.Disconnects > 0 {
		stop()
		os.Exit(1)
	}
}
