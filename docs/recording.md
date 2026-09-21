# Recording the demo GIF

The README has a commented-out slot for `docs/demo.gif`. To fill it:

1. Install the tools once: `brew install asciinema agg` (or your package manager's equivalents).
2. Make the terminal exactly 80x24 and run `asciinema rec demo.cast -c "make demo"`.
3. Play a short Tron round against the bot, then quit with `q` twice (the game, then the menu).
4. Convert it: `agg --font-size 16 demo.cast docs/demo.gif`.
5. Uncomment the image line near the top of `README.md`, and commit `docs/demo.gif`.

Keep the GIF under a few MB so the README loads quickly. Do not commit `demo.cast`.
