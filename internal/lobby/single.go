// Package lobby decides which room a connection ends up in.
//
// Right now it only has SingleRoom, a temporary stand-in that puts everybody
// in one hardcoded room. The real lobby (menu, matchmaking, many rooms)
// replaces it in Stage 4.
package lobby

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/n9e6y/gocade/internal/game"
	"github.com/n9e6y/gocade/internal/input"
	"github.com/n9e6y/gocade/internal/room"
	"github.com/n9e6y/gocade/internal/server"
)

// Compile-time checks that the pieces fit together.
var (
	_ server.Handler = (*SingleRoom)(nil)
	_ room.Sink      = (*server.Session)(nil)
)

// SingleRoom is a server.Handler that puts every connection into one room
// running one game. It is TEMPORARY: it plays a single game per server run,
// so restart the server for another.
type SingleRoom struct {
	room *room.Room
}

// NewSingleRoom returns a handler around a room for g. tick is the room's
// tick channel (nil for a turn-based game; see room.TickerFor).
func NewSingleRoom(g game.Game, tick <-chan time.Time, log *slog.Logger) *SingleRoom {
	return &SingleRoom{room: room.New(g, tick, log)}
}

// Run runs the room's goroutine until ctx is cancelled. The caller starts it
// (with wg.Add before go) and cancels ctx to stop it.
func (l *SingleRoom) Run(ctx context.Context) {
	l.room.Run(ctx)
}

// playerID turns a session into the player it represents in the game.
func playerID(s *server.Session) game.PlayerID {
	return game.PlayerID(s.ID())
}

// OnConnect seats the new player. If the game will not take them, they are
// told why and left connected: closing right after Send could lose the
// message, and they can press q to leave.
func (l *SingleRoom) OnConnect(s *server.Session) {
	if err := l.room.Join(playerID(s), s); err != nil {
		s.Send([]byte(fmt.Sprintf("Cannot join: %v. Press q to quit.\r\n", err)))
	}
}

// OnKeys ends the session on a quit key and passes every other key to the
// room. Keys from a player who never joined are ignored by the room.
func (l *SingleRoom) OnKeys(s *server.Session, keys []input.Key) {
	for _, k := range keys {
		if k.IsQuit() {
			s.Close()
			return
		}
		l.room.Input(playerID(s), k)
	}
}

// OnDisconnect removes the player from the room, which forfeits their game.
// It is a no-op for a player who never joined.
func (l *SingleRoom) OnDisconnect(s *server.Session) {
	l.room.Leave(playerID(s))
}
