package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		addr    string
		want    string
		wantErr bool
	}{
		{name: "default version", version: "dev", addr: "127.0.0.1:0", want: "arena dev\n"},
		{name: "injected version", version: "v0.0.1", addr: "127.0.0.1:0", want: "arena v0.0.1\n"},
		{name: "bad address", version: "dev", addr: "not-an-address", want: "arena dev\n", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// A context that is already cancelled makes a healthy server
			// start, find it should stop, and return at once.
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			var out bytes.Buffer
			log := slog.New(slog.NewTextHandler(io.Discard, nil))

			err := run(ctx, &out, tt.version, tt.addr, log)
			if (err != nil) != tt.wantErr {
				t.Fatalf("run() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got := out.String(); got != tt.want {
				t.Errorf("run() output = %q, want %q", got, tt.want)
			}
		})
	}
}
