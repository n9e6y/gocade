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
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
)

// eventBuffer is how many events may queue for the room goroutine.
const eventBuffer = 64

// Errors returned by Join and AddBot.
var (
	// ErrClosed means the room has stopped.
	ErrClosed = errors.New("room closed")
	// ErrNoBot means the game cannot play a seat itself (it is not a
	// game.Advisor).
	ErrNoBot = errors.New("game has no bot")
)

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
	evAddBot
)

// event is one request to the room goroutine.
type event struct {
	kind  eventKind
	id    game.PlayerID
	name  string     // evJoin, evAddBot: display name
	sink  Sink       // evJoin
	key   input.Key  // evInput
	reply chan error // evJoin, evAddBot, evLeave: buffered, so the room never waits for the caller
}

// Room runs one game.
type Room struct {
	g    game.Game
	tick <-chan time.Time // nil for a turn-based game: a nil channel never fires
	log  *slog.Logger

	events  chan event
	done    chan struct{} // closed when Run returns
	over    chan struct{} // closed once, when the game becomes Over
	crashed atomic.Bool   // Run stopped because the game (or a bot) panicked

	// Owned by the Run goroutine; no other goroutine may touch these.
	players map[game.PlayerID]Sink
	bots    []game.PlayerID // computer players, in the order they were seated; they have no Sink
	isOver  bool            // the game has been seen to be Over (and over has been closed)
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
// cancelled, or until the game panics; whoever starts Run cancels ctx and waits
// for it to return. Even after the game is over the room stays up, so the
// players can still see the result.
//
// A panic in the game, in a bot's advice or in drawing a frame is caught here,
// logged with its stack, and ends only this room: Run returns, Done is closed
// and Crashed reports true, and the owner (the lobby) sends the players back to
// the menu. Nothing else in the server is touched.
func (r *Room) Run(ctx context.Context) {
	defer close(r.done)
	defer r.recoverPanic() // deferred last, so it runs first: Crashed is set before Done closes
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-r.events:
			r.handle(e)
		case <-r.tick:
			if r.g.State() == game.StateRunning {
				r.g.Tick()
				r.playBots()
				r.broadcast()
				r.noteIfOver()
			}
		}
	}
}

// recoverPanic turns a panic on the Run goroutine into a stopped room. It has
// to be called directly by defer for recover to work.
func (r *Room) recoverPanic() {
	if v := recover(); v != nil {
		r.crashed.Store(true)
		r.log.Error("room panicked", "panic", v, "stack", string(debug.Stack()))
	}
}

// Done returns a channel that is closed when Run has returned, whether it was
// cancelled or the game panicked (see Crashed).
func (r *Room) Done() <-chan struct{} {
	return r.done
}

// Crashed reports whether the room stopped because of a panic. It is final once
// Done is closed.
func (r *Room) Crashed() bool {
	return r.crashed.Load()
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

// AddBot seats a computer player and waits for the answer: nil on success,
// ErrNoBot if the game cannot play a seat itself, the game's own error (such as
// game.ErrFull) if it refuses, or ErrClosed if the room has stopped.
//
// A bot sits down through the same Game.Join as a human, but it has no
// connection, so it is sent no frames. Instead the room presses its keys: after
// every change it asks the game what each bot should do (see playBots). The
// lobby is responsible for keeping at least one human in a room that has a
// bot, and for closing the room when the last human leaves: a room of bots
// alone would have nothing to wake it up.
func (r *Room) AddBot(id game.PlayerID, name string) error {
	reply := make(chan error, 1)
	if !r.send(event{kind: evAddBot, id: id, name: name, reply: reply}) {
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
		r.playBots()
		r.broadcast()
		// Reply only after the game is updated and the frames are sent, so a
		// caller that sees Join return knows the room is done changing things.
		e.reply <- nil

	case evAddBot:
		if _, ok := r.g.(game.Advisor); !ok {
			e.reply <- ErrNoBot
			return
		}
		if err := r.g.Join(e.id, e.name); err != nil {
			e.reply <- err
			return
		}
		r.bots = append(r.bots, e.id)
		r.log.Info("bot joined", "player", e.id, "players", len(r.players)+len(r.bots))
		r.playBots() // the bot may have the first move
		r.broadcast()
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
		r.playBots()
		r.broadcast()
	}
	r.noteIfOver()
}

// playBots gives every bot its turn to respond to the game as it now stands:
// each is asked what to press, and the answer goes to Game.Input, the same
// call a human's key ends in. It runs on the Run goroutine after every
// change, before the frames are sent, so people see a bot's answer in the
// same frame as the move that caused it. There are no bot goroutines: nothing
// here needs stopping.
//
// Asking again when nothing relevant changed is harmless. For a game that
// moves on ticks, a bot just picks the same answer again.
func (r *Room) playBots() {
	if len(r.bots) == 0 || r.g.State() != game.StateRunning {
		return
	}
	adv, ok := r.g.(game.Advisor)
	if !ok { // AddBot only accepts advisors, so this cannot happen
		return
	}
	for _, id := range r.bots {
		if k, ok := adv.Advise(id); ok {
			r.g.Input(id, k)
		}
	}
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
