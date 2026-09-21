// Command arena runs the Arena multiplayer terminal-game server.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/game/tictactoe"
	"github.com/n9e6y/gocade/internal/game/tron"
	"github.com/n9e6y/gocade/internal/lobby"
	"github.com/n9e6y/gocade/internal/server"
)

// version is set at build time: go build -ldflags "-X main.version=v0.1.0".
var version = "dev"

// config is everything the flags set.
type config struct {
	addr        string        // TCP address to listen on
	fillWait    time.Duration // how long a lone player waits before a bot joins; 0 turns it off
	maxConns    int           // most simultaneous connections; 0 means no limit
	idleTimeout time.Duration // a client silent this long is disconnected; 0 means never
}

// The most keys a client may send: about ten times what a fast player types
// (a held-down arrow key repeats around 30 a second), with room for a burst.
const (
	keysPerSecond = 100
	keyBurst      = 200
)

func main() {
	var cfg config
	flag.StringVar(&cfg.addr, "addr", ":9000", "TCP address to listen on")
	flag.DurationVar(&cfg.fillWait, "fill-wait", 10*time.Second, "how long a player waits alone before a bot joins (0 to turn off)")
	flag.IntVar(&cfg.maxConns, "max-conns", 1000, "most simultaneous connections (0 for no limit)")
	flag.DurationVar(&cfg.idleTimeout, "idle-timeout", 5*time.Minute, "disconnect a client that sends nothing for this long (0 to turn off)")
	flag.Parse()

	// Ctrl-C or SIGTERM cancels ctx, which is what stops the server.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := run(ctx, os.Stdout, version, cfg, log); err != nil {
		log.Error("arena failed", "err", err)
		stop() // os.Exit skips deferred calls
		os.Exit(1)
	}
	log.Info("shutdown complete")
}

// run prints the version line, then serves on cfg.addr until ctx is cancelled.
func run(ctx context.Context, out io.Writer, version string, cfg config, log *slog.Logger) error {
	if _, err := fmt.Fprintf(out, "arena %s\n", version); err != nil {
		return fmt.Errorf("write version: %w", err)
	}

	// The games on offer. Adding a game to Arena is one new package plus one
	// line here.
	reg := lobby.NewRegistry()
	games := []struct {
		name, title string
		factory     lobby.Factory
		opts        []lobby.EntryOption
	}{
		{"tictactoe", "Tic-Tac-Toe", func() game.Game { return tictactoe.New() }, []lobby.EntryOption{lobby.WithBots()}},
		{"tron", "Tron", func() game.Game { return tron.New() }, []lobby.EntryOption{lobby.WithBots()}},
	}
	for _, g := range games {
		if err := reg.Register(g.name, g.title, g.factory, g.opts...); err != nil {
			return fmt.Errorf("register games: %w", err)
		}
	}

	ln, err := net.Listen("tcp", cfg.addr)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", cfg.addr, err)
	}
	log.Info("listening", "addr", ln.Addr().String())

	handler := lobby.New(reg, log, lobby.WithFillWait(cfg.fillWait))
	srv := server.New(ln, handler, log,
		server.WithMaxConns(cfg.maxConns),
		server.WithIdleTimeout(cfg.idleTimeout),
		server.WithKeyRate(keysPerSecond, keyBurst),
	)

	// run owns the lobby goroutine: it starts it here and stops it below by
	// cancelling lobbyCtx. The lobby is stopped after the server, so it is
	// still there to hear about every player leaving during shutdown.
	lobbyCtx, stopLobby := context.WithCancel(context.Background())
	defer stopLobby()
	var wg sync.WaitGroup
	wg.Add(1) // before go, never inside the goroutine
	go func() {
		defer wg.Done()
		handler.Run(lobbyCtx)
	}()

	err = srv.Run(ctx)
	stopLobby()
	wg.Wait()
	if err != nil {
		return fmt.Errorf("run server: %w", err)
	}
	return nil
}
