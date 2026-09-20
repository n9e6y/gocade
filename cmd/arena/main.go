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
	"syscall"

	"github.com/n9e6y/gocade/internal/server"
)

// version is set at build time: go build -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	addr := flag.String("addr", ":9000", "TCP address to listen on")
	flag.Parse()

	// Ctrl-C or SIGTERM cancels ctx, which is what stops the server.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	if err := run(ctx, os.Stdout, version, *addr, log); err != nil {
		log.Error("arena failed", "err", err)
		stop() // os.Exit skips deferred calls
		os.Exit(1)
	}
	log.Info("shutdown complete")
}

// run prints the version line, then serves on addr until ctx is cancelled.
func run(ctx context.Context, out io.Writer, version, addr string, log *slog.Logger) error {
	if _, err := fmt.Fprintf(out, "arena %s\n", version); err != nil {
		return fmt.Errorf("write version: %w", err)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %q: %w", addr, err)
	}
	log.Info("listening", "addr", ln.Addr().String())

	if err := server.New(ln, log).Run(ctx); err != nil {
		return fmt.Errorf("run server: %w", err)
	}
	return nil
}
