// Package server accepts TCP connections and runs one Session (a reader and a
// writer goroutine) per client.
package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
)

const (
	// outBuffer is how many frames may wait for a slow client before Send
	// starts dropping.
	outBuffer = 16
	// readBuffer is the size of each session's read buffer.
	readBuffer = 512
	// greeting is sent to every player on connect.
	greeting = "Welcome to Arena. Type anything and it is echoed back; q quits.\r\n"
)

// Server accepts connections from a listener and runs a session for each.
type Server struct {
	ln      net.Listener
	log     *slog.Logger
	wg      sync.WaitGroup // tracks one serve goroutine per connection
	nextID  atomic.Uint64
	dropped atomic.Uint64 // frames dropped across all sessions
}

// New returns a Server that will accept connections from ln. The caller
// creates the listener so it controls the address (tests use 127.0.0.1:0).
func New(ln net.Listener, log *slog.Logger) *Server {
	return &Server{ln: ln, log: log}
}

// Dropped returns the number of frames dropped so far because a client was
// too slow to read them.
func (s *Server) Dropped() uint64 {
	return s.dropped.Load()
}

// Run accepts connections until ctx is cancelled, then closes every session
// and returns after all goroutines have exited. It returns nil when stopped
// by ctx, or a wrapped error if the listener fails for another reason.
func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)

	// Accept does not watch ctx, so closing the listener is what wakes it up.
	stop := context.AfterFunc(ctx, func() { s.ln.Close() })

	err := s.acceptLoop(ctx)

	// Whatever ended the loop, stop everything: cancelling ctx closes every
	// session's connection, and wg.Wait guarantees no goroutine outlives Run.
	stop()
	cancel()
	s.ln.Close()
	s.wg.Wait()

	return err
}

// acceptLoop starts a serve goroutine per connection. It returns nil once ctx
// is cancelled, or an error if Accept fails while ctx is still live.
func (s *Server) acceptLoop(ctx context.Context) error {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}

		s.wg.Add(1) // before go, never inside the goroutine
		go func() {
			defer s.wg.Done()
			s.serve(ctx, conn)
		}()
	}
}

// serve runs one session on conn: it is the reader, and it starts and owns
// the writer goroutine. It returns when the client disconnects, the player
// quits, the writer fails, or ctx is cancelled, and it does not return until
// the writer has exited.
func (s *Server) serve(ctx context.Context, conn net.Conn) {
	sess := newSession(s.nextID.Add(1), conn, outBuffer, &s.dropped)
	log := s.log.With("session", sess.id)
	log.Debug("connected", "remote", conn.RemoteAddr())

	sctx, cancel := context.WithCancel(ctx)

	// A blocked Read or Write cannot see ctx, but closing the conn makes it
	// fail. This closes the conn whenever sctx ends, for any reason.
	stop := context.AfterFunc(sctx, func() { conn.Close() })

	var writer sync.WaitGroup
	writer.Add(1)
	go func() { // owned by serve; exits when sctx is cancelled or a write fails
		defer writer.Done()
		sess.writeLoop(sctx)
		cancel() // a dead writer ends the whole session
	}()

	defer func() {
		stop()
		cancel()
		conn.Close()
		writer.Wait()
		log.Debug("disconnected")
	}()

	sess.Send([]byte(greeting))

	buf := make([]byte, readBuffer)
	for {
		n, err := conn.Read(buf)
		if n > 0 && handleInput(sess, buf[:n]) {
			return
		}
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				log.Debug("read failed", "err", err)
			}
			return
		}
	}
}

// handleInput is TEMPORARY Stage 1 behavior: echo the bytes back and report
// whether the player asked to quit. Stage 2 replaces it with a key decoder.
// p is only valid during the call, so the echo is a copy.
func handleInput(sess *Session, p []byte) (quit bool) {
	if i := bytes.IndexByte(p, 'q'); i >= 0 {
		p = p[:i]
		quit = true
	}
	if len(p) > 0 {
		sess.Send(bytes.Clone(p))
	}
	return quit
}
