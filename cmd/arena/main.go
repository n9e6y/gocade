// Command arena runs the Arena multiplayer terminal-game server.
package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
)

// version is set at build time: go build -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	if err := run(os.Stdout, version); err != nil {
		slog.Error("arena failed", "err", err)
		os.Exit(1)
	}
}

// run prints the version line to out. Later stages will start the server here.
func run(out io.Writer, version string) error {
	if _, err := fmt.Fprintf(out, "arena %s\n", version); err != nil {
		return fmt.Errorf("write version: %w", err)
	}
	return nil
}
