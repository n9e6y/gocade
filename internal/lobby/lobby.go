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
	"time"

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
	Send(frame []byte) bool // never blocks; reports false if the frame could not be queued
	Close()
}

// Stats is a snapshot of what the lobby is holding.
type Stats struct {
	Players int // connected players, whatever screen they are on
	Rooms   int // open rooms, waiting or playing
	Bots    int // computer players seated in those rooms
}

// playerState is which screen a player is on.
type playerState uint8

const (
	stateNickname playerState = iota // typing a nickname
	stateMenu                        // choosing a game
	stateInRoom                      // in a room, waiting or playing
	stateResult                      // game over; result on screen until Enter
	stateMode                        // chose a game that has a bot; choosing online or vs bot
)

// botIDBase is where the ids of bots start. Session ids count up from 1, so a
// bot id can never equal a player's.
const botIDBase game.PlayerID = 1 << 62

// botName is what a bot is called on screen.
const botName = "Bot"

// crashNotice is shown on the menu to players whose game crashed.
const crashNotice = "That game crashed. Sorry! Pick another."

// player is the lobby's record of one connection. Only the lobby goroutine
// touches it.
type player struct {
	conn    Conn
	id      game.PlayerID
	state   playerState
	editor  nameEditor // the nickname being typed (stateNickname)
	name    string     // the finished nickname
	pick    Entry      // the game being set up (stateMode)
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
	members []game.PlayerID    // the humans, in join order (bots are only counted, in bots)
	bots    int                // computer players seated in the room
	min     int                // players the game needs to start
	max     int                // seats; the room is full at this many players
	private bool               // a vs-bot room: never on the waiting list
	closed  bool

	// The fill timer (see syncFill): stopFill is non-nil while one is running,
	// and fillSeq numbers them, so a late message from a timer that has since
	// been stopped can be recognized and ignored.
	stopFill func() bool
	fillSeq  int
}

type eventKind uint8

const (
	evConnect eventKind = iota + 1
	evKeys
	evDisconnect
	evRoomOver
	evStats
	evFill
	evRoomCrashed
)

// event is one message to the lobby goroutine.
type event struct {
	kind  eventKind
	conn  Conn        // evConnect, evKeys, evDisconnect
	keys  []input.Key // evKeys
	rm    *roomEntry  // evRoomOver, evFill, evRoomCrashed
	seq   int         // evFill: which of the room's timers fired
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
	lastBot game.PlayerID           // the last bot id handed out; ids count up from botIDBase

	fillWait time.Duration // how long a lone player waits before a bot joins; 0 means never
	after    afterFunc     // starts the fill timers; time.AfterFunc unless a test replaces it
}

// afterFunc runs f in its own goroutine after d, and returns a function that
// stops it (reporting whether it stopped it in time). It has the shape of
// time.AfterFunc, and is injected so tests can fire timers by hand instead of
// waiting.
type afterFunc func(d time.Duration, f func()) (stop func() bool)

func realAfter(d time.Duration, f func()) func() bool {
	return time.AfterFunc(d, f).Stop
}

// Option changes how a Lobby behaves. See WithFillWait.
type Option func(*Lobby)

// WithFillWait makes the lobby add a bot to a player who has waited alone in
// an online room of a game that has a bot for this long. Zero (the default)
// turns it off.
func WithFillWait(d time.Duration) Option {
	return func(l *Lobby) { l.fillWait = d }
}

// withAfterFunc replaces the timer, for tests.
func withAfterFunc(f afterFunc) Option {
	return func(l *Lobby) { l.after = f }
}

// New returns a Lobby offering the games in reg. The registry must not be
// changed once the lobby is running.
func New(reg *Registry, log *slog.Logger, opts ...Option) *Lobby {
	l := &Lobby{
		reg:     reg,
		log:     log,
		events:  make(chan event, eventBuffer),
		done:    make(chan struct{}),
		players: make(map[uint64]*player),
		rooms:   make(map[*roomEntry]struct{}),
		waiting: make(map[string][]*roomEntry),
		after:   realAfter,
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
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

	case evRoomCrashed:
		l.roomCrashed(e.rm)

	case evFill:
		l.fill(e.rm, e.seq)

	case evStats:
		bots := 0
		for re := range l.rooms {
			bots += re.bots
		}
		e.stats <- Stats{Players: len(l.players), Rooms: len(l.rooms), Bots: bots}
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
			l.pick(p, games[n-1])
		}

	case stateMode:
		n, isDigit := k.Digit()
		switch {
		case isDigit && n == 1:
			l.join(p, p.pick)
		case isDigit && n == 2:
			l.joinBot(p, p.pick)
		case k.IsQuit() || (k.Kind == input.KindRune && (k.Rune == 'b' || k.Rune == 'B')):
			l.showMenu(p, "") // q means "back" here; on the menu itself it quits
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

// pick handles the choice of a game on the menu. A game with a bot asks how to
// play; any other game goes straight to an online room.
func (l *Lobby) pick(p *player, g Entry) {
	if !g.Bots {
		l.join(p, g)
		return
	}
	p.pick = g
	p.state = stateMode
	p.conn.Send(modeFrame(g.Title, ""))
}

// joinBot starts a game for p against computer players. The room is private:
// it is never offered to anyone else, so p plays alone against the bot. Bots
// are seated until the game has the players it needs, so it starts at once.
func (l *Lobby) joinBot(p *player, g Entry) {
	re := l.newRoom(g, true)
	if err := re.room.Join(p.id, p.name, p.conn); err != nil {
		l.log.Warn("could not join room", "player", p.id, "game", g.Name, "err", err)
		l.closeRoom(re)
		l.showMenu(p, "Could not start that game. Try again.")
		return
	}
	p.state = stateInRoom
	p.room = re
	re.members = append(re.members, p.id)

	for len(re.members)+re.bots < re.min {
		if err := l.addBot(re); err != nil {
			l.log.Warn("could not seat a bot", "player", p.id, "game", g.Name, "err", err)
			l.leaveRoom(p) // the room has no humans left, so it closes
			l.showMenu(p, "Could not start that game. Try again.")
			return
		}
	}
}

// syncFill makes a room's fill timer match its situation. A public room of a
// game with a bot, holding exactly one human and no bot, has a timer running;
// any other room has none. It is called whenever that situation may have
// changed: someone joined, someone left, the room closed.
func (l *Lobby) syncFill(re *roomEntry) {
	want := l.fillWait > 0 && re.game.Bots && !re.private && !re.closed &&
		len(re.members) == 1 && re.bots == 0

	switch {
	case want && re.stopFill == nil:
		re.fillSeq++
		seq := re.fillSeq
		// The callback runs on a timer goroutine, which ends as soon as post
		// returns, and post always returns: the event is queued, or the lobby
		// has stopped.
		re.stopFill = l.after(l.fillWait, func() {
			l.post(event{kind: evFill, rm: re, seq: seq})
		})
	case !want && re.stopFill != nil:
		re.stopFill()
		re.stopFill = nil
	}
}

// fill is the fill timer firing: a bot joins the player who has been waiting
// alone. seq says which timer it was. A timer that was stopped just as it
// fired may still get here, so everything is checked again, and a message from
// an old timer is ignored.
func (l *Lobby) fill(re *roomEntry, seq int) {
	if re.closed || re.stopFill == nil || seq != re.fillSeq {
		return
	}
	re.stopFill = nil // this timer has done its job

	if len(re.members) != 1 || re.bots != 0 {
		return
	}
	if err := l.addBot(re); err != nil {
		l.log.Debug("could not fill the room with a bot", "game", re.game.Name, "err", err)
		return
	}
	l.log.Info("a bot joined a waiting player", "game", re.game.Name, "player", re.members[0])
	if len(re.members)+re.bots >= re.max {
		l.removeWaiting(re) // full: nobody else can be seated
	}
}

// addBot seats one computer player in re, with the next free bot id.
func (l *Lobby) addBot(re *roomEntry) error {
	id := botIDBase + l.lastBot + 1
	if err := re.room.AddBot(id, botName); err != nil {
		return err
	}
	l.lastBot++
	re.bots++
	return nil
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
			re = l.newRoom(g, false)
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
			if len(re.members)+re.bots >= re.max {
				l.removeWaiting(re) // full: nobody else can be seated
			}
			l.syncFill(re)
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
		return
	}
	l.syncFill(re)
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

// roomCrashed handles a room whose game panicked (the room has already logged
// the panic and stopped): the players in it go back to the menu with an
// apology and the room is closed. Every other room carries on.
func (l *Lobby) roomCrashed(re *roomEntry) {
	if re.closed {
		return // everyone had already left
	}
	l.log.Error("closing a crashed room", "game", re.game.Name, "players", len(re.members))
	for _, id := range re.members {
		p := l.players[uint64(id)]
		if p == nil || p.state != stateInRoom || p.room != re {
			continue
		}
		p.room = nil
		l.showMenu(p, crashNotice)
	}
	re.members = nil
	l.closeRoom(re)
}

// ---- rooms --------------------------------------------------------------

// newRoom creates a room for game g and starts its goroutines. A public room
// is put in the waiting list, and join removes it once it is full; a private
// room (one that is for a single player and a bot) is never listed.
func (l *Lobby) newRoom(g Entry, private bool) *roomEntry {
	gm := g.New()
	min, max := gm.Seats()
	tick, stopTicker := room.TickerFor(gm)
	rm := room.New(gm, tick, l.log)

	ctx, cancel := context.WithCancel(l.ctx)
	re := &roomEntry{game: g, room: rm, cancel: cancel, min: min, max: max, private: private}
	l.rooms[re] = struct{}{}
	if !private {
		l.waiting[g.Name] = append(l.waiting[g.Name], re)
	}

	// Two goroutines per room, both owned by the lobby: they stop when ctx is
	// cancelled (closeRoom, or the lobby itself stopping), and Run waits for
	// them with wg. wg.Add comes before go.
	l.wg.Add(2)
	go func() { // the room's own goroutine, which owns its game
		defer l.wg.Done()
		defer stopTicker()
		rm.Run(ctx)
	}()
	go func() { // tells the lobby when the game ends, or when the room crashes
		defer l.wg.Done()
		var ev event
		select {
		case <-rm.Over():
			ev = event{kind: evRoomOver, rm: re}
		case <-rm.Done():
			// The room stops by itself only when its game panicked; a plain
			// stop is the lobby cancelling ctx, and needs no message.
			if !rm.Crashed() {
				return
			}
			ev = event{kind: evRoomCrashed, rm: re}
		case <-ctx.Done():
			return
		}
		// The lobby may be busy or its inbox full; give up if it stops.
		select {
		case l.events <- ev:
		case <-l.ctx.Done():
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
	l.syncFill(re) // stops its timer, if it has one
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
