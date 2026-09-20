// Package lobby is the front door of Arena. A player connects, chooses a
// nickname, picks a game from a menu, and is matched with someone else who
// picked the same game. When the game ends they return to the menu.
//
// The Lobby is one goroutine that owns everything mutable: every player and
// every room. Session goroutines never touch that state; they post events to
// it over a channel. That is why the lobby needs no mutex.
package lobby

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
	"github.com/n9e6y/gocade/internal/room"
	"github.com/n9e6y/gocade/internal/server"
)

// eventBuffer is how many events may queue for the lobby goroutine.
const eventBuffer = 256

// Compile-time checks that the pieces fit together.
var (
	_ server.Handler = (*Lobby)(nil)
	_ Conn           = (*server.Session)(nil)
	_ room.Sink      = Conn(nil)
)

// Conn is one connected player, as the lobby sees it. *server.Session is the
// real implementation; tests use a fake.
type Conn interface {
	ID() uint64
	Send(frame []byte) bool // never blocks; reports false if the frame was dropped
	Close()
}

// Stats is a snapshot of what the lobby is holding.
type Stats struct {
	Players int // connected players, whatever screen they are on
	Rooms   int // open rooms, waiting or playing
}

// playerState is which screen a player is on.
type playerState uint8

const (
	stateNickname playerState = iota // typing a nickname
	stateMenu                        // choosing a game
	stateInRoom                      // in a room, waiting or playing
	stateResult                      // game over; result on screen until Enter
)

// player is the lobby's record of one connection. Only the lobby goroutine
// touches it.
type player struct {
	conn    Conn
	id      game.PlayerID
	state   playerState
	editor  nameEditor // the nickname being typed (stateNickname)
	name    string     // the finished nickname
	room    *roomEntry // set only in stateInRoom
	closing bool       // Close has been called; ignore further keys
}

// roomEntry is the lobby's record of one room. Only the lobby goroutine
// touches it (the Room inside has its own goroutine, and is used only
// through its methods).
type roomEntry struct {
	game    Entry
	room    *room.Room
	cancel  context.CancelFunc // stops the room's goroutines
	members []game.PlayerID    // in join order
	max     int                // seats; the room is full at this many members
	closed  bool
}

type eventKind uint8

const (
	evConnect eventKind = iota + 1
	evKeys
	evDisconnect
	evRoomOver
	evStats
)

// event is one message to the lobby goroutine.
type event struct {
	kind  eventKind
	conn  Conn        // evConnect, evKeys, evDisconnect
	keys  []input.Key // evKeys
	rm    *roomEntry  // evRoomOver
	stats chan Stats  // evStats: buffered, so the lobby never waits for the caller
}

// Lobby matches players into rooms. Create one with New and run it with Run.
type Lobby struct {
	reg *Registry
	log *slog.Logger

	events chan event
	done   chan struct{} // closed when Run returns

	// Everything below is owned by the Run goroutine.
	ctx     context.Context // Run's context; room contexts are children of it
	wg      sync.WaitGroup  // every room and watcher goroutine
	players map[uint64]*player
	rooms   map[*roomEntry]struct{}
	waiting map[string][]*roomEntry // per game name, oldest first: rooms that still have a free seat
}

// New returns a Lobby offering the games in reg. The registry must not be
// changed once the lobby is running.
func New(reg *Registry, log *slog.Logger) *Lobby {
	return &Lobby{
		reg:     reg,
		log:     log,
		events:  make(chan event, eventBuffer),
		done:    make(chan struct{}),
		players: make(map[uint64]*player),
		rooms:   make(map[*roomEntry]struct{}),
		waiting: make(map[string][]*roomEntry),
	}
}

// Run is the lobby goroutine. It handles events until ctx is cancelled, then
// stops every room and returns only after all of their goroutines have
// exited. The caller starts Run (wg.Add before go) and cancels ctx to stop
// it; cancel it after the server has stopped, so the lobby is still there
// to hear every player leave.
func (l *Lobby) Run(ctx context.Context) {
	defer close(l.done)
	l.ctx = ctx
	defer l.wg.Wait() // runs before close(done): rooms are children of ctx, so they are stopping

	for {
		select {
		case <-ctx.Done():
			return
		case e := <-l.events:
			l.handle(e)
		}
	}
}

// ---- the server.Handler side: runs on session goroutines --------------

// OnConnect implements server.Handler.
func (l *Lobby) OnConnect(s *server.Session) { l.connect(s) }

// OnKeys implements server.Handler.
func (l *Lobby) OnKeys(s *server.Session, keys []input.Key) { l.onKeys(s, keys) }

// OnDisconnect implements server.Handler.
func (l *Lobby) OnDisconnect(s *server.Session) { l.disconnect(s) }

func (l *Lobby) connect(c Conn)                  { l.post(event{kind: evConnect, conn: c}) }
func (l *Lobby) onKeys(c Conn, keys []input.Key) { l.post(event{kind: evKeys, conn: c, keys: keys}) }
func (l *Lobby) disconnect(c Conn)               { l.post(event{kind: evDisconnect, conn: c}) }

// Stats asks the lobby for a snapshot. The lobby answers after handling
// every event posted before the question, so the answer reflects them all.
// It returns the zero Stats if the lobby has stopped.
func (l *Lobby) Stats() Stats {
	reply := make(chan Stats, 1)
	if !l.post(event{kind: evStats, stats: reply}) {
		return Stats{}
	}
	select {
	case st := <-reply:
		return st
	case <-l.done:
		return Stats{}
	}
}

// post queues e for the lobby goroutine. It reports false, instead of
// blocking forever, if the lobby has already stopped.
func (l *Lobby) post(e event) bool {
	select {
	case l.events <- e:
		return true
	case <-l.done:
		return false
	}
}

// ---- the lobby goroutine ------------------------------------------------

func (l *Lobby) handle(e event) {
	switch e.kind {
	case evConnect:
		p := &player{conn: e.conn, id: game.PlayerID(e.conn.ID()), state: stateNickname}
		l.players[e.conn.ID()] = p
		l.log.Debug("player connected", "player", p.id)
		p.conn.Send(nicknameFrame("", ""))

	case evKeys:
		p := l.players[e.conn.ID()]
		if p == nil {
			return // the session is already gone
		}
		for _, k := range e.keys {
			if p.closing {
				break
			}
			l.key(p, k)
		}

	case evDisconnect:
		p := l.players[e.conn.ID()]
		if p == nil {
			return // already handled
		}
		delete(l.players, e.conn.ID())
		l.leaveRoom(p)
		l.log.Debug("player disconnected", "player", p.id)

	case evRoomOver:
		l.roomOver(e.rm)

	case evStats:
		e.stats <- Stats{Players: len(l.players), Rooms: len(l.rooms)}
	}
}

// key applies one key press from p, according to the screen they are on.
func (l *Lobby) key(p *player, k input.Key) {
	if k.Kind == input.KindCtrlC { // leave from anywhere
		p.closing = true
		p.conn.Close()
		return
	}

	switch p.state {
	case stateNickname:
		if p.editor.Feed(k) {
			p.name = p.editor.Name()
			p.state = stateMenu
			l.log.Info("player named", "player", p.id, "name", p.name)
			l.showMenu(p, "")
			return
		}
		notice := ""
		if k.Kind == input.KindEnter { // Enter was refused
			notice = "A nickname cannot be empty."
		}
		p.conn.Send(nicknameFrame(p.editor.Text(), notice))

	case stateMenu:
		if k.IsQuit() {
			p.closing = true
			p.conn.Close()
			return
		}
		games := l.reg.Entries()
		if n, ok := k.Digit(); ok && n >= 1 && n <= len(games) {
			l.join(p, games[n-1])
		}

	case stateInRoom:
		if k.IsQuit() { // q: back to the menu (forfeits a running game)
			l.leaveRoom(p)
			l.showMenu(p, "")
			return
		}
		p.room.room.Input(p.id, k)

	case stateResult:
		if k.Kind == input.KindEnter {
			p.state = stateMenu
			l.showMenu(p, "")
		}
	}
}

// showMenu puts the game menu on p's screen.
func (l *Lobby) showMenu(p *player, notice string) {
	p.state = stateMenu
	p.conn.Send(menuFrame(p.name, l.reg.Entries(), notice))
}

// join puts p in the oldest room of game g that will take them, or in a new
// room if there is none.
//
// A waiting room can turn out to be closed: a game such as Tron starts before
// it is full, so the room may have started since it was last joined. The room
// says so with an error, and the lobby then drops it from the waiting list
// (it is not offered again) and tries the next one. Every retry removes a
// room, and a fresh room always takes its first player, so the loop ends.
func (l *Lobby) join(p *player, g Entry) {
	for {
		re := l.oldestWaiting(g.Name)
		fresh := re == nil
		if fresh {
			re = l.newRoom(g)
		}

		// Join waits for the room goroutine. That cannot deadlock: the room
		// never waits for the lobby (it only closes a channel), and it never
		// blocks on a player (Send does not block).
		err := re.room.Join(p.id, p.name, p.conn)
		switch {
		case err == nil:
			p.state = stateInRoom
			p.room = re
			re.members = append(re.members, p.id)
			if len(re.members) >= re.max {
				l.removeWaiting(re) // full: nobody else can be seated
			}
			return

		case !fresh && roomIsClosedToNewcomers(err):
			l.log.Debug("room no longer takes players", "game", g.Name, "err", err)
			l.removeWaiting(re)

		default:
			l.log.Warn("could not join room", "player", p.id, "game", g.Name, "err", err)
			if len(re.members) == 0 {
				l.closeRoom(re)
			}
			l.showMenu(p, "Could not join that game. Try again.")
			return
		}
	}
}

// roomIsClosedToNewcomers reports whether err means "this room cannot take
// you, but another might": it is full, already started, or over.
func roomIsClosedToNewcomers(err error) bool {
	return errors.Is(err, game.ErrStarted) || errors.Is(err, game.ErrFull) || errors.Is(err, game.ErrOver)
}

// leaveRoom takes p out of their room, if they are in one. It waits for the
// room to process the departure, so the room sends p nothing afterwards and
// the caller can safely draw a new screen for them. A room left with nobody
// in it is closed.
func (l *Lobby) leaveRoom(p *player) {
	re := p.room
	if re == nil {
		return
	}
	p.room = nil
	re.room.Leave(p.id)
	for i, id := range re.members {
		if id == p.id {
			re.members = append(re.members[:i], re.members[i+1:]...)
			break
		}
	}
	if len(re.members) == 0 {
		l.closeRoom(re)
	}
}

// roomOver handles a finished game: the players still in it move to the
// result screen (their final frame is already there) and the room is closed.
func (l *Lobby) roomOver(re *roomEntry) {
	if re.closed {
		return // everyone had already left
	}
	for _, id := range re.members {
		p := l.players[uint64(id)]
		if p == nil || p.state != stateInRoom || p.room != re {
			continue
		}
		p.state = stateResult
		p.room = nil
		// Sent after the room's last frame: the room closes its Over channel
		// only after broadcasting, so the order on p's screen is certain.
		p.conn.Send([]byte(resultHint))
	}
	l.closeRoom(re)
}

// ---- rooms --------------------------------------------------------------

// newRoom creates a room for game g and starts its goroutines. It is put in
// the waiting list; join removes it once it is full.
func (l *Lobby) newRoom(g Entry) *roomEntry {
	gm := g.New()
	_, max := gm.Seats()
	tick, stopTicker := room.TickerFor(gm)
	rm := room.New(gm, tick, l.log)

	ctx, cancel := context.WithCancel(l.ctx)
	re := &roomEntry{game: g, room: rm, cancel: cancel, max: max}
	l.rooms[re] = struct{}{}
	l.waiting[g.Name] = append(l.waiting[g.Name], re)

	// Two goroutines per room, both owned by the lobby: they stop when ctx is
	// cancelled (closeRoom, or the lobby itself stopping), and Run waits for
	// them with wg. wg.Add comes before go.
	l.wg.Add(2)
	go func() { // the room's own goroutine, which owns its game
		defer l.wg.Done()
		defer stopTicker()
		rm.Run(ctx)
	}()
	go func() { // tells the lobby when the game ends
		defer l.wg.Done()
		select {
		case <-rm.Over():
			// The lobby may be busy or its inbox full; give up if it stops.
			select {
			case l.events <- event{kind: evRoomOver, rm: re}:
			case <-l.ctx.Done():
			}
		case <-ctx.Done():
		}
	}()

	l.log.Info("room opened", "game", g.Name)
	return re
}

// closeRoom retires a room: it leaves the tables and its goroutines are
// told to stop. It is safe to call twice.
func (l *Lobby) closeRoom(re *roomEntry) {
	if re.closed {
		return
	}
	re.closed = true
	delete(l.rooms, re)
	l.removeWaiting(re)
	re.cancel()
	l.log.Info("room closed", "game", re.game.Name)
}

// oldestWaiting returns the oldest room of game name with a free seat, or
// nil if there is none.
func (l *Lobby) oldestWaiting(name string) *roomEntry {
	if q := l.waiting[name]; len(q) > 0 {
		return q[0]
	}
	return nil
}

// removeWaiting takes re out of its game's waiting list, if it is there.
func (l *Lobby) removeWaiting(re *roomEntry) {
	q := l.waiting[re.game.Name]
	for i, w := range q {
		if w == re {
			l.waiting[re.game.Name] = append(q[:i], q[i+1:]...)
			return
		}
	}
}
