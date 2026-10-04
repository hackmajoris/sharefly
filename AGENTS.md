# sharefly

Standard library only. Plan history: `docs/plans/`.

Commands:
- build: `go build -o sharefly ./cmd/sharefly`
- test: `go test -race ./...`, `go vet ./...`, `gofmt -l .`

Layout: `cmd/sharefly` (subcommands, server wiring), `pkg/share` (store, TTL, IDs, extract, sweep/reconcile), `pkg/server` (API and public file handlers), `pkg/client` (archive + API client).

Invariants (keep when changing code):
- Delete a share's dir before its record (Sweep, API delete): a crash must leave a dangling record, never an orphaned public dir.
- Renew rejects already-expired shares, because Sweep deletes from a snapshot without re-checking.
- Store mutations clone the slice, save `shares.json` (fsync + rename), then swap in memory, so memory is never ahead of disk.
- Public responses always carry `Cache-Control: private, no-store`, including 404s. It is set in a ResponseWriter wrapper because `http.FileServer` strips it from error responses.
- The public file system hides directories without a regular-file `index.html`: no listing anywhere.
- Extraction writes only regular files/dirs, requests modes 0644/0755 (ignores tar header modes), and caps uncompressed bytes (100MB, while reading) and entry count.
- The client validates TTL and path (folder has `index.html`) before opening any connection.
- The server binds both listeners before Reconcile/Sweep, so a second instance never touches live data. A Reconcile error aborts startup (orphans would otherwise stay public).
- Upload saves the record before renaming the dir into `shares/`, so nothing is public without a record. Add+Rename and the delete handler share `API.publishMu`, so a delete can't drop the record in between.
- Extract returns `ErrPathConflict` (400) for archive file/dir conflicts; other `*fs.PathError`s are server faults (500).

Contracts:
- `server.API` owns the dir names (`SharesDir()`, `TmpDir()`) and assumes they exist; only `runServer` creates them.
- `maxUploadBytes` in `cmd/sharefly/server.go` must match the 100MB in README.
- `serve` auto-starts a server only when the target is this machine (`ownServerAddr`: loopback or the configured `api-addr`); never for remote ones. The auto-started server reads the config file like any other. `start` writes `<data-dir>/server.pid` after binding and removes it on exit; `stop` relies on it.
- Settings resolve flag > `~/.config/sharefly/config.json` > default; keys and defaults live in one table (`configKeys` in `cmd/sharefly/config.go`). There are no `SHAREFLY_*` env vars. The server writes `server.args.json` next to the pid file; setting a server-side key restarts a running local server with those args, except `public-url`, which the server re-reads live when the config file's mtime changes (`livePublicURL`).
- Client archive and server extract agree on format (regular-file entries, slash-separated relative paths); `TestArchiveRoundTripsThroughExtract` guards it.
- Tests use `httptest` + `t.TempDir()`; safety tests name the guarantee they protect.
- With `tunnel` set, `runServer` supervises a `cloudflared` child (`cmd/sharefly/tunnel.go`): token only via `TUNNEL_TOKEN` env, restart with backoff, SIGTERM on shutdown and wait for it to exit. Quick-tunnel URL is parsed from its output and wins over config `public-url` (an explicit `--public-url` wins over both); an explicit `--tunnel off` pins links to `http://<public-addr>`. `parseServerFlags` rejects token mode without `tunnel-token` or a public-url, and every path that starts a server (`start`, `serve` auto-start, config restart) runs it first; a restart validates before stopping the old server. `waitReady` fails as soon as the spawned pid is gone (`spawnServer` reaps it). Secret keys (`tunnel-token`) have no flag and are masked in `sharefly config`; the config file is written 0600.
