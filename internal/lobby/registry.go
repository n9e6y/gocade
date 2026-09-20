package lobby

import (
	"errors"
	"fmt"
	"slices"

	"github.com/n9e6y/gocade/internal/game"
)

// MaxGames is how many games a registry holds: the menu chooses a game with
// the digit keys 1-9.
const MaxGames = 9

// Errors returned by Registry.Register.
var (
	ErrDuplicateGame = errors.New("game already registered")
	ErrInvalidGame   = errors.New("invalid game registration")
)

// Factory returns a fresh game, ready for players. The lobby calls it once
// per room, so factories must not share state between the games they return.
type Factory func() game.Game

// Entry is one game the lobby offers.
type Entry struct {
	Name  string  // stable key, such as "tictactoe"
	Title string  // what the menu shows, such as "Tic-Tac-Toe"
	New   Factory // makes a game for a new room
}

// Registry lists the games the lobby offers, in the order they were added
// (the menu shows them in that order). It is built once at startup, before
// the lobby runs, and is only read afterwards, so it needs no locking.
//
// Adding a game to Arena is one new package plus one Register call in main.
type Registry struct {
	entries []Entry
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a game. It rejects an empty name or title, a nil factory, a
// name that is already taken, and a tenth game (see MaxGames).
func (r *Registry) Register(name, title string, f Factory) error {
	switch {
	case name == "":
		return fmt.Errorf("%w: empty name", ErrInvalidGame)
	case title == "":
		return fmt.Errorf("%w: game %q has no title", ErrInvalidGame, name)
	case f == nil:
		return fmt.Errorf("%w: game %q has no factory", ErrInvalidGame, name)
	case len(r.entries) >= MaxGames:
		return fmt.Errorf("%w: the menu holds at most %d games", ErrInvalidGame, MaxGames)
	}
	if _, ok := r.Lookup(name); ok {
		return fmt.Errorf("%w: %q", ErrDuplicateGame, name)
	}
	r.entries = append(r.entries, Entry{Name: name, Title: title, New: f})
	return nil
}

// Entries returns the registered games in registration order. The slice is a
// copy, so callers cannot change the registry through it.
func (r *Registry) Entries() []Entry {
	return slices.Clone(r.entries)
}

// Lookup finds a game by name.
func (r *Registry) Lookup(name string) (Entry, bool) {
	for _, e := range r.entries {
		if e.Name == name {
			return e, true
		}
	}
	return Entry{}, false
}
