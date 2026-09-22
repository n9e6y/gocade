package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
)

// minTick is the fastest Tron tick the flag accepts. Below this a typo (say
// 12ms for 120ms) would make every room spin a core and flood every client.
const minTick = 10 * time.Millisecond

// logLevels are the values -log-level accepts.
var logLevels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

// parseFlags reads the command line (without the program name). Everything the
// user needs to hear, whether the flag package's own complaint, the usage for
// -h, or a value that is out of range, is written to stderr here, so the caller
// only has to choose an exit code: flag.ErrHelp means the usage was asked for,
// any other error means the command line was wrong.
func parseFlags(args []string, stderr io.Writer) (config, error) {
	var cfg config
	var level string

	fs := flag.NewFlagSet("arena", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&cfg.addr, "addr", ":9000", "TCP address to listen on")
	fs.DurationVar(&cfg.fillWait, "fill-wait", 10*time.Second, "how long a player waits alone before a bot joins (0 to turn off)")
	fs.IntVar(&cfg.maxConns, "max-conns", 1000, "most simultaneous connections (0 for no limit)")
	fs.DurationVar(&cfg.idleTimeout, "idle-timeout", 5*time.Minute, "disconnect a client that sends nothing for this long (0 to turn off)")
	fs.DurationVar(&cfg.tick, "tick", 120*time.Millisecond, "how often the real-time games (Tron, Snake) advance (at least "+minTick.String()+")")
	fs.StringVar(&level, "log-level", "info", "log detail: debug, info, warn or error")

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if err := validate(&cfg, level, fs.Args()); err != nil {
		fmt.Fprintln(stderr, "arena:", err)
		return config{}, err
	}
	return cfg, nil
}

// validate checks what the flag package cannot: ranges, the log level's name,
// and leftover arguments. It fills in cfg.logLevel from level.
func validate(cfg *config, level string, extra []string) error {
	lv, ok := logLevels[strings.ToLower(level)]
	switch {
	case len(extra) > 0:
		return fmt.Errorf("unexpected argument %q (arena takes flags only)", extra[0])
	case !ok:
		return fmt.Errorf("invalid -log-level %q: use debug, info, warn or error", level)
	case cfg.tick < minTick:
		return fmt.Errorf("invalid -tick %v: must be at least %v", cfg.tick, minTick)
	case cfg.fillWait < 0:
		return errors.New("invalid -fill-wait: must not be negative")
	case cfg.maxConns < 0:
		return errors.New("invalid -max-conns: must not be negative")
	case cfg.idleTimeout < 0:
		return errors.New("invalid -idle-timeout: must not be negative")
	}
	cfg.logLevel = lv
	return nil
}

// newLogger returns a text logger on w that drops records below level.
func newLogger(level slog.Level, w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}
