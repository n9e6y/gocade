// Package server accepts TCP connections and runs one Session (a reader and a
// writer goroutine) per client. What happens to the bytes is decided by a
// Handler, so the server itself knows nothing about games.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/n9e6y/gocade/internal/input"
)

const (
	// outBuffer is how many frames may wait for a slow client before Send
	// starts discarding the oldest.
	outBuffer = 16
	// readBuffer is the size of each session's read buffer. It is also the
	// most bytes, and so the most keys, one read can hand to the handler.
	readBuffer = 512

	// goodbyeTimeout bounds the one write made to a connection that is about to
	// be closed (a refusal or an idle notice). The message is tiny and goes
	// into the socket buffer, so this only matters for a peer that has stopped
	// reading altogether.
	goodbyeTimeout = time.Second
)

// What a client is told just before it is disconnected.
const (
	fullMessage = "Server is full. Try again later.\r\n"
	idleMessage = "\r\nDisconnected: you were idle for too long.\r\n"
)

// Handler decides what a connection means. The server calls it from the
// session's reader goroutine, so for one session the calls are serial: a
// session's OnConnect finishes before its first OnKeys, and OnDisconnect
// comes last. Calls for different sessions run concurrently, so a Handler
// must be safe for that.
//
// A Handler must not block for long: while it runs, that session's reader is
// not reading.
type Handler interface {
	// OnConnect is called once when the session starts. The session is ready
	// for Send.
	OnConnect(s *Session)

	// OnKeys is called with each batch of keys decoded from the client. The
	// slice is the handler's to keep.
	OnKeys(s *Session, keys []input.Key)

	// OnDisconnect is called exactly once, after the session has stopped,
	// however it ended: the client left, the handler called Close, or the
	// server shut down.
	OnDisconnect(s *Session)
}

// Server accepts connections from a listener and runs a session for each.
type Server struct {
	ln      net.Listener
	handler Handler
	log     *slog.Logger
	wg      sync.WaitGroup // tracks one serve goroutine per connection
	nextID  atomic.Uint64
	dropped atomic.Uint64 // frames dropped across all sessions

	// Limits. Each is off unless its Option is given.
	slots       chan struct{} // one token per live connection; nil means no cap
	idleTimeout time.Duration // a client silent for this long is disconnected; 0 means never
	keyRate     float64       // keys per second each session may send; 0 means unlimited
	keyBurst    int
	now         func() time.Time // the clock the key budget reads; a test replaces it
	rejected    atomic.Uint64    // connections turned away because the server was full
	throttled   atomic.Uint64    // keys dropped because a client sent too many
}

// Option changes how a Server behaves. See WithMaxConns, WithIdleTimeout and
// WithKeyRate.
type Option func(*Server)

// WithMaxConns allows at most n connections at once. A client that arrives
// when the server is full is told so and disconnected at once. n <= 0 means
// no limit.
func WithMaxConns(n int) Option {
	return func(s *Server) {
		if n > 0 {
			s.slots = make(chan struct{}, n)
		}
	}
}

// WithIdleTimeout disconnects a client that sends nothing for d. Any bytes
// count as activity, so a player who is pressing keys is never idle. d <= 0
// means never.
func WithIdleTimeout(d time.Duration) Option {
	return func(s *Server) { s.idleTimeout = d }
}

// WithKeyRate limits each session to perSecond keys a second, with room for a
// burst of burst keys. Keys beyond that are dropped and counted (Throttled).
// perSecond <= 0 means unlimited.
func WithKeyRate(perSecond float64, burst int) Option {
	return func(s *Server) { s.keyRate, s.keyBurst = perSecond, burst }
}

// withClock replaces the clock the key budget reads, for tests.
func withClock(now func() time.Time) Option {
	return func(s *Server) { s.now = now }
}

// New returns a Server that will accept connections from ln and give each to
// h. The caller creates the listener so it controls the address (tests use
// 127.0.0.1:0).
func New(ln net.Listener, h Handler, log *slog.Logger, opts ...Option) *Server {
	s := &Server{ln: ln, handler: h, log: log, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Dropped returns the number of frames dropped so far because a client was
// too slow to read them.
func (s *Server) Dropped() uint64 {
	return s.dropped.Load()
}

// Rejected returns the number of connections turned away because the server
// was full.
func (s *Server) Rejected() uint64 {
	return s.rejected.Load()
}

// Throttled returns the number of keys dropped so far because a client sent
// them faster than its budget allows.
func (s *Server) Throttled() uint64 {
	return s.throttled.Load()
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

		if !s.acquire() {
			s.turnAway(conn)
			continue
		}

		s.wg.Add(1) // before go, never inside the goroutine
		go func() {
			defer s.wg.Done()
			defer s.release()
			s.serve(ctx, conn)
		}()
	}
}

// acquire takes one of the connection slots without waiting. It reports false
// if the server is full. With no cap there is always room.
func (s *Server) acquire() bool {
	if s.slots == nil {
		return true
	}
	select {
	case s.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

// release gives back a slot taken by acquire.
func (s *Server) release() {
	if s.slots != nil {
		<-s.slots
	}
}

// turnAway tells a client the server is full and closes its connection. It
// runs on the accept loop, so it must not wait long: the write has a deadline.
func (s *Server) turnAway(conn net.Conn) {
	s.rejected.Add(1)
	s.log.Warn("connection refused: server is full", "remote", conn.RemoteAddr())
	goodbye(conn, fullMessage)
	conn.Close()
}

// goodbye makes one bounded attempt to write msg to conn.
func goodbye(conn net.Conn, msg string) {
	if err := conn.SetWriteDeadline(time.Now().Add(goodbyeTimeout)); err != nil {
		return
	}
	conn.Write([]byte(msg)) // best effort: the peer may already be gone
}

// serve runs one session on conn: it is the reader, and it starts and owns
// the writer goroutine. It returns when the client disconnects, the handler
// closes the session, the writer fails, or ctx is cancelled, and it does not
// return until the writer has exited and the handler has been told.
func (s *Server) serve(ctx context.Context, conn net.Conn) {
	sctx, cancel := context.WithCancel(ctx)

	sess := newSession(s.nextID.Add(1), conn, outBuffer, &s.dropped)
	sess.cancel = cancel
	log := s.log.With("session", sess.id)
	log.Debug("connected", "remote", conn.RemoteAddr())

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
		s.handler.OnDisconnect(sess)
		log.Debug("disconnected")
	}()

	s.handler.OnConnect(sess)

	// Each session has its own decoder: it remembers half-received escape
	// sequences, which belong to this client alone.
	var dec input.Decoder
	buf := make([]byte, readBuffer)
	var budget *keyBudget // this session's own, so no locking
	if s.keyRate > 0 {
		budget = newKeyBudget(s.keyRate, s.keyBurst)
	}
	for {
		if s.idleTimeout > 0 {
			// A deadline is how a blocked Read learns that time is up. Moving it
			// forward on every read makes it a timeout on silence.
			conn.SetReadDeadline(time.Now().Add(s.idleTimeout))
		}
		n, err := conn.Read(buf)
		if n > 0 {
			if keys := dec.Feed(buf[:n]); len(keys) > 0 {
				if budget != nil {
					allowed := budget.allow(s.now(), len(keys))
					s.throttled.Add(uint64(len(keys) - allowed))
					keys = keys[:allowed]
				}
				if len(keys) > 0 {
					s.handler.OnKeys(sess, keys)
				}
			}
		}
		if err != nil {
			switch {
			case errors.Is(err, os.ErrDeadlineExceeded):
				log.Info("disconnecting an idle client", "idle", s.idleTimeout)
				goodbye(conn, idleMessage)
			case !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed):
				log.Debug("read failed", "err", err)
			}
			return
		}
	}
}
