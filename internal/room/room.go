// Package room owns one game and runs it.
//
// A Room has exactly one goroutine, Run, and that goroutine is the only one
// that ever touches the Game or the room's player table. Everyone else (the
// session goroutines) talks to it by sending events over a channel. That is
// the whole concurrency story: one owner, communication instead of locking.
package room

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
)

// eventBuffer is how many events may queue for the room goroutine.
const eventBuffer = 64

// ErrClosed is returned by Join when the room has stopped.
var ErrClosed = errors.New("room closed")

// Sink is where the room delivers a player's frames. It is satisfied by
// *server.Session, and by fakes in tests. Send must not block: the room
// calls it for every player, so one slow player must not stall the others.
type Sink interface {
	Send(frame []byte) bool
}

type eventKind uint8

const (
	evJoin eventKind = iota + 1
	evLeave
	evInput
)

// event is one request to the room goroutine.
type event struct {
	kind  eventKind
	id    game.PlayerID
	name  string     // evJoin: display name
	sink  Sink       // evJoin
	key   input.Key  // evInput
	reply chan error // evJoin, evLeave: buffered, so the room never waits for the caller
}

// Room runs one game.
type Room struct {
	g    game.Game
	tick <-chan time.Time // nil for a turn-based game: a nil channel never fires
	log  *slog.Logger

	events chan event
	done   chan struct{} // closed when Run returns
	over   chan struct{} // closed once, when the game becomes Over

	// Owned by the Run goroutine; no other goroutine may touch these.
	players map[game.PlayerID]Sink
	isOver  bool // the game has been seen to be Over (and over has been closed)
}

// New returns a Room for g. tick drives real-time games; pass nil for a
// turn-based game. Tests pass a channel they send on by hand, so no test
// ever waits for a real clock.
func New(g game.Game, tick <-chan time.Time, log *slog.Logger) *Room {
	return &Room{
		g:       g,
		tick:    tick,
		log:     log.With("game", g.Name()),
		events:  make(chan event, eventBuffer),
		done:    make(chan struct{}),
		over:    make(chan struct{}),
		players: make(map[game.PlayerID]Sink),
	}
}

// TickerFor returns the tick channel for g and the function that stops it.
// A turn-based game gets a nil channel and a stop that does nothing. The
// caller owns the ticker and must call stop when the room has finished.
func TickerFor(g game.Game) (tick <-chan time.Time, stop func()) {
	d := g.TickEvery()
	if d <= 0 {
		return nil, func() {}
	}
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// Run is the room's one goroutine. It handles events and ticks until ctx is
// cancelled, which is the only way it stops; whoever starts Run cancels ctx
// and waits for it to return. Even after the game is over the room stays up,
// so the players can still see the result.
func (r *Room) Run(ctx context.Context) {
	defer close(r.done)
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-r.events:
			r.handle(e)
		case <-r.tick:
			if r.g.State() == game.StateRunning {
				r.g.Tick()
				r.broadcast()
				r.noteIfOver()
			}
		}
	}
}

// Join seats a player and waits for the answer: nil on success, the game's
// error (such as game.ErrFull) if it refuses, or ErrClosed if the room has
// stopped. name is the player's display name. After a successful Join the
// room sends s every frame for id.
func (r *Room) Join(id game.PlayerID, name string, s Sink) error {
	reply := make(chan error, 1)
	if !r.send(event{kind: evJoin, id: id, name: name, sink: s, reply: reply}) {
		return ErrClosed
	}
	select {
	case err := <-reply:
		return err
	case <-r.done:
		return ErrClosed
	}
}

// Over returns a channel that is closed when the game becomes Over. The room
// only ever closes it (it never sends to whoever is watching), so a slow or
// busy watcher can never block the room.
func (r *Room) Over() <-chan struct{} {
	return r.over
}

// Leave removes a player and waits until the room has done so, or the room
// has stopped. Waiting matters: once Leave returns, the room will send that
// player no more frames, so the caller can safely draw something else (such
// as the menu) on the player's screen. It does nothing for an unknown
// player.
func (r *Room) Leave(id game.PlayerID) {
	reply := make(chan error, 1)
	if !r.send(event{kind: evLeave, id: id, reply: reply}) {
		return
	}
	select {
	case <-reply:
	case <-r.done:
	}
}

// Input passes a key from a player to the game. Keys from someone who has
// not joined are dropped. It does not wait.
func (r *Room) Input(id game.PlayerID, k input.Key) {
	r.send(event{kind: evInput, id: id, key: k})
}

// send queues e for the room goroutine. It reports false, instead of
// blocking forever, if the room has already stopped.
func (r *Room) send(e event) bool {
	select {
	case r.events <- e:
		return true
	case <-r.done:
		return false
	}
}

// handle applies one event. It runs only on the Run goroutine.
func (r *Room) handle(e event) {
	switch e.kind {
	case evJoin:
		if err := r.g.Join(e.id, e.name); err != nil {
			e.reply <- err
			return
		}
		r.players[e.id] = e.sink
		r.log.Info("player joined", "player", e.id, "players", len(r.players))
		r.broadcast()
		// Reply only after the game is updated and the frames are sent, so a
		// caller that sees Join return knows the room is done changing things.
		e.reply <- nil

	case evLeave:
		if _, ok := r.players[e.id]; ok {
			delete(r.players, e.id)
			r.g.Leave(e.id)
			r.log.Info("player left", "player", e.id, "players", len(r.players))
			r.broadcast()
		}
		r.noteIfOver()
		e.reply <- nil // after the update, so the caller knows the room is done with this player
		return

	case evInput:
		if _, ok := r.players[e.id]; !ok {
			return
		}
		r.g.Input(e.id, e.key)
		r.broadcast()
	}
	r.noteIfOver()
}

// broadcast sends every player their own view of the game. Send never
// blocks, so a slow player just misses a frame.
func (r *Room) broadcast() {
	for id, s := range r.players {
		s.Send(r.g.View(id).Frame())
	}
}

// noteIfOver, when the game first becomes Over, logs the result and closes
// the Over channel. It does nothing on later calls.
func (r *Room) noteIfOver() {
	if r.isOver || r.g.State() != game.StateOver {
		return
	}
	r.isOver = true
	out := r.g.Outcome()
	r.log.Info("game over", "winner", out.Winner, "draw", out.Draw)
	close(r.over)
}
