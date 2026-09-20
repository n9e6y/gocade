// Package game defines the Game interface that every game implements, and
// the small types they share.
//
// Game packages under this directory are pure: no goroutines, no net, no
// clock, no I/O. The same inputs always give the same result, which is what
// makes them easy to test and to replay. (This package imports time only for
// the time.Duration type; nothing here reads a clock.)
package game

import (
	"errors"
	"fmt"
	"time"

	"github.com/n9e6y/gocade/internal/input"
	"github.com/n9e6y/gocade/internal/render"
)

// PlayerID identifies a player within a game. The zero value is never a
// valid player, so Outcome.Winner can use it to mean "nobody".
type PlayerID uint64

// State is where a game is in its life.
type State uint8

// The states of a game, in order.
const (
	StateWaiting State = iota // not enough players yet
	StateRunning              // being played
	StateOver                 // finished; see Outcome
)

// String returns the state's name, for logs and test failures.
func (s State) String() string {
	switch s {
	case StateWaiting:
		return "Waiting"
	case StateRunning:
		return "Running"
	case StateOver:
		return "Over"
	}
	return fmt.Sprintf("State(%d)", uint8(s))
}

// Outcome is the result of a finished game.
type Outcome struct {
	Winner PlayerID // zero when the game was a draw
	Draw   bool
}

// Errors returned by Game.Join.
var (
	ErrFull          = errors.New("game is full")
	ErrAlreadyJoined = errors.New("player already joined")
	ErrOver          = errors.New("game is over")
)

// Game is the rules of one game, as seen by a Room. A Game is only ever used
// by one goroutine (its Room's), so implementations need no locking.
type Game interface {
	// Name is the game's registry key, such as "tictactoe".
	Name() string

	// Seats returns the fewest and most players the game supports.
	Seats() (min, max int)

	// TickEvery is how often Tick should be called. Zero means the game is
	// turn-based and needs no ticker.
	TickEvery() time.Duration

	// Join seats a player. When the last seat fills, the game starts.
	Join(p PlayerID) error

	// Leave removes a player. Mid-game this forfeits for that player.
	Leave(p PlayerID)

	// Input applies one key press from a player. Illegal input is not an
	// error: the game shows the player a message in their next View.
	Input(p PlayerID, k input.Key)

	// Tick advances a real-time game by one step. It does nothing for
	// turn-based games.
	Tick()

	// View draws the game as player p sees it.
	View(p PlayerID) *render.Canvas

	// State reports whether the game is waiting, running or over.
	State() State

	// Outcome reports the result. It is only meaningful once State is Over.
	Outcome() Outcome
}
