package main

import (
	"bytes"
	"errors"
	"flag"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestParseFlags(t *testing.T) {
	t.Parallel()

	defaults := config{
		addr:        ":9000",
		fillWait:    10 * time.Second,
		maxConns:    1000,
		idleTimeout: 5 * time.Minute,
		tick:        120 * time.Millisecond,
		logLevel:    slog.LevelInfo,
	}
	with := func(edit func(*config)) config {
		c := defaults
		edit(&c)
		return c
	}

	tests := []struct {
		name string
		args []string
		want config
	}{
		{"no flags gives the defaults", nil, defaults},
		{"every flag", []string{
			"-addr", "127.0.0.1:9100", "-fill-wait", "3s", "-max-conns", "5",
			"-idle-timeout", "30s", "-tick", "60ms", "-log-level", "debug",
		}, config{
			addr: "127.0.0.1:9100", fillWait: 3 * time.Second, maxConns: 5,
			idleTimeout: 30 * time.Second, tick: 60 * time.Millisecond, logLevel: slog.LevelDebug,
		}},
		{"zero turns off the optional limits", []string{"-fill-wait", "0", "-max-conns", "0", "-idle-timeout", "0"},
			with(func(c *config) { c.fillWait, c.maxConns, c.idleTimeout = 0, 0, 0 })},
		{"the slowest tick that is allowed is 10 ms", []string{"-tick", "10ms"},
			with(func(c *config) { c.tick = 10 * time.Millisecond })},
		{"log level debug", []string{"-log-level", "debug"}, with(func(c *config) { c.logLevel = slog.LevelDebug })},
		{"log level info", []string{"-log-level", "info"}, defaults},
		{"log level warn", []string{"-log-level", "warn"}, with(func(c *config) { c.logLevel = slog.LevelWarn })},
		{"log level error", []string{"-log-level", "error"}, with(func(c *config) { c.logLevel = slog.LevelError })},
		{"log level is not case sensitive", []string{"-log-level", "WARN"}, with(func(c *config) { c.logLevel = slog.LevelWarn })},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stderr bytes.Buffer
			got, err := parseFlags(tt.args, &stderr)
			if err != nil {
				t.Fatalf("parseFlags(%v) error = %v", tt.args, err)
			}
			if got != tt.want {
				t.Errorf("parseFlags(%v) = %+v, want %+v", tt.args, got, tt.want)
			}
		})
	}
}

func TestParseFlags_Rejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		wantMsg string // must appear in the error, so the user learns what is wrong
	}{
		{"a tick that would spin a core", []string{"-tick", "5ms"}, "tick"},
		{"a zero tick", []string{"-tick", "0"}, "tick"},
		{"a negative tick", []string{"-tick", "-1s"}, "tick"},
		{"an unknown log level", []string{"-log-level", "loud"}, "log-level"},
		{"a negative wait", []string{"-fill-wait", "-1s"}, "fill-wait"},
		{"a negative connection cap", []string{"-max-conns", "-1"}, "max-conns"},
		{"a negative idle timeout", []string{"-idle-timeout", "-1s"}, "idle-timeout"},
		{"an unknown flag", []string{"-bogus"}, "bogus"},
		{"a duration without a unit", []string{"-tick", "120"}, "tick"},
		{"an argument that is not a flag", []string{"extra"}, "extra"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stderr bytes.Buffer
			_, err := parseFlags(tt.args, &stderr)
			if err == nil {
				t.Fatalf("parseFlags(%v) succeeded, want an error", tt.args)
			}
			if !strings.Contains(err.Error()+stderr.String(), tt.wantMsg) {
				t.Errorf("error %q (output %q) does not mention %q", err, stderr.String(), tt.wantMsg)
			}
		})
	}
}

// -h is not a failure: main prints the usage and exits 0.
func TestParseFlags_Help(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	_, err := parseFlags([]string{"-h"}, &stderr)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("error = %v, want flag.ErrHelp", err)
	}
	for _, name := range []string{"-addr", "-fill-wait", "-max-conns", "-idle-timeout", "-tick", "-log-level"} {
		if !strings.Contains(stderr.String(), name) {
			t.Errorf("usage does not list %s:\n%s", name, stderr.String())
		}
	}
}

func TestNewRegistry(t *testing.T) {
	t.Parallel()

	reg, err := newRegistry(config{tick: 60 * time.Millisecond})
	if err != nil {
		t.Fatalf("newRegistry() error = %v", err)
	}

	entries := reg.Entries()
	if len(entries) != 2 || entries[0].Name != "tictactoe" || entries[1].Name != "tron" {
		t.Fatalf("games = %+v, want tictactoe then tron", entries)
	}
	for _, e := range entries {
		if !e.Bots {
			t.Errorf("%s is not offered against a bot", e.Name)
		}
	}
	if got := entries[0].New().TickEvery(); got != 0 {
		t.Errorf("Tic-Tac-Toe TickEvery() = %v, want 0 (turn-based)", got)
	}
	// The -tick flag is what Tron ticks at.
	if got := entries[1].New().TickEvery(); got != 60*time.Millisecond {
		t.Errorf("Tron TickEvery() = %v, want 60ms from the flag", got)
	}
}

// A config that never went through parseFlags (as in the tests that start run
// directly) leaves the tick unset, and Tron then keeps its own default.
func TestNewRegistry_UnsetTickMeansTheGamesDefault(t *testing.T) {
	t.Parallel()

	reg, err := newRegistry(config{})
	if err != nil {
		t.Fatal(err)
	}
	tronEntry, _ := reg.Lookup("tron")
	if got := tronEntry.New().TickEvery(); got != 120*time.Millisecond {
		t.Errorf("Tron TickEvery() = %v, want the default 120ms", got)
	}
}

func TestNewLogger_LevelFiltersRecords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		level   slog.Level
		wantDbg bool
		wantInf bool
		wantWrn bool
	}{
		{slog.LevelDebug, true, true, true},
		{slog.LevelInfo, false, true, true},
		{slog.LevelWarn, false, false, true},
		{slog.LevelError, false, false, false},
	}
	for _, tt := range tests {
		var out bytes.Buffer
		log := newLogger(tt.level, &out)
		log.Debug("dbg-record")
		log.Info("inf-record")
		log.Warn("wrn-record")

		for _, c := range []struct {
			marker string
			want   bool
		}{{"dbg-record", tt.wantDbg}, {"inf-record", tt.wantInf}, {"wrn-record", tt.wantWrn}} {
			if got := strings.Contains(out.String(), c.marker); got != c.want {
				t.Errorf("level %v: %s logged = %v, want %v", tt.level, c.marker, got, c.want)
			}
		}
	}
}
