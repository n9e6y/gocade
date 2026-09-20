package main

import (
	"bytes"
	"testing"
)

func TestRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "default version", version: "dev", want: "arena dev\n"},
		{name: "injected version", version: "v0.0.1", want: "arena v0.0.1\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer
			if err := run(&out, tt.version); err != nil {
				t.Fatalf("run() error = %v", err)
			}
			if got := out.String(); got != tt.want {
				t.Errorf("run() output = %q, want %q", got, tt.want)
			}
		})
	}
}
