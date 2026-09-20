# GOCADE

A multiplayer game server in Go. Anyone can play by typing `nc host 9000`: no
client, no dependencies, standard library only.

> Work in progress. Planned games: Tic-Tac-Toe and real-time Tron, with bots
> for solo play.

## Quickstart (planned)

```sh
go run ./cmd/arena -addr :9000
stty -icanon -echo; nc localhost 9000; stty sane
```

## Development

```sh
make build test vet fmt-check
```

See [docs/decisions.md](docs/decisions.md) for design decisions and trade-offs.
