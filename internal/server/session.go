package server

import (
	"context"
	"net"
	"sync/atomic"
)

// Session is one connected player. Its reader runs in Server.serve and its
// writer in writeLoop; the only thing they share is the buffered out channel.
type Session struct {
	id      uint64
	conn    net.Conn
	out     chan []byte
	dropped *atomic.Uint64     // server-wide drop counter, shared by all sessions
	cancel  context.CancelFunc // ends the session; set by Server.serve
}

// newSession returns a session whose out channel holds up to outBuf frames.
func newSession(id uint64, conn net.Conn, outBuf int, dropped *atomic.Uint64) *Session {
	return &Session{
		id:      id,
		conn:    conn,
		out:     make(chan []byte, outBuf),
		dropped: dropped,
	}
}

// ID returns the session's identifier, unique within its server and never
// zero.
func (s *Session) ID() uint64 { return s.id }

// Close ends the session: the connection is closed and the reader and writer
// goroutines stop. It is safe to call more than once and from any goroutine.
// Frames still queued in out may be lost, so a message sent just before
// Close is not guaranteed to arrive.
func (s *Session) Close() {
	if s.cancel != nil {
		s.cancel()
	}
}

// Send queues frame for the writer goroutine. It never blocks: if the out
// buffer is full (the client is too slow), the frame is dropped, counted, and
// Send returns false. A slow client therefore loses frames instead of
// stalling the goroutine that produced them, and the next frame is a fresh
// full picture anyway.
//
// Send is safe to call from any goroutine, including after the session has
// closed: out is never closed, so a late Send is just dropped or discarded.
func (s *Session) Send(frame []byte) bool {
	select {
	case s.out <- frame:
		return true
	default:
		s.dropped.Add(1)
		return false
	}
}

// writeLoop copies frames from out to the connection. It is started by
// Server.serve, which stops it by cancelling ctx (or by closing the conn,
// which makes a blocked Write fail). It returns on either.
func (s *Session) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case frame := <-s.out:
			if _, err := s.conn.Write(frame); err != nil {
				return
			}
		}
	}
}
