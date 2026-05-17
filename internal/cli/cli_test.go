package cli

import (
	"testing"

	"local-symphony/internal/core"
)

func TestServeOptionsFromArgsParsesAddr(t *testing.T) {
	opts, err := serveOptionsFromArgs([]string{"--project", ".", "--addr", "127.0.0.1:3777", "--no-open"})
	if err != nil {
		t.Fatalf("serveOptionsFromArgs returned error: %v", err)
	}
	if opts.Host != "127.0.0.1" {
		t.Fatalf("Host = %q, want 127.0.0.1", opts.Host)
	}
	if opts.Port != 3777 {
		t.Fatalf("Port = %d, want 3777", opts.Port)
	}
	if !opts.NoOpen {
		t.Fatalf("NoOpen = false, want true")
	}
}

func TestServeOptionsFromArgsKeepsHostPort(t *testing.T) {
	opts, err := serveOptionsFromArgs([]string{"--host", "localhost", "--port", "3888"})
	if err != nil {
		t.Fatalf("serveOptionsFromArgs returned error: %v", err)
	}
	if opts.Host != "localhost" || opts.Port != 3888 {
		t.Fatalf("opts = %#v, want localhost:3888", opts)
	}
}

func TestServeOptionsFromArgsRejectsNonNumericPort(t *testing.T) {
	_, err := serveOptionsFromArgs([]string{"--port", "abc"})
	if err == nil {
		t.Fatal("serveOptionsFromArgs succeeded, want invalid_request")
	}
	if got := core.AsAPIError(err).Code; got != core.ErrInvalidRequest {
		t.Fatalf("error code = %s, want %s", got, core.ErrInvalidRequest)
	}
}
