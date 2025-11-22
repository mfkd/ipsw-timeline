ipsw-timeline
=============

A small CLI that fetches the `https://ipsw.me/timeline.rss` feed and shows the latest Apple firmware releases (iOS, iPadOS, macOS, etc.) in a colorized, fixed-width table in your terminal.

## Build
- `go build -o ipsw-timeline .`

## Run
- `./ipsw-timeline` — fetches the default feed with recent entries.
- `./ipsw-timeline -h` — show all flags.

## Common flags
- `-f, -feed-url` — RSS URL (default `https://ipsw.me/timeline.rss`).
- `-l, -limit` — number of entries to show (default 15).
- `-c, -contains` — case-insensitive filter on title.
- `-t, -timeout` — HTTP timeout in seconds (default 10).
- `-C, -color` — color mode: `auto`, `always`, or `never`.
