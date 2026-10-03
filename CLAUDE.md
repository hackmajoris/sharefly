# go-share

Standard library only. Plan history: `docs/plans/`.

Commands:
- build: `go build -o go-share ./cmd/go-share`
- test: `go test -race ./...`, `go vet ./...`, `gofmt -l .`
- plist: `plutil -lint deploy/com.go-share.server.plist`

Layout: `cmd/go-share` (subcommands, server wiring), `pkg/share` (store, TTL, IDs, extract, sweep/reconcile), `pkg/server` (API and public file handlers), `pkg/client` (archive + API client), `deploy/` (launchd plist).

Invariants (keep when changing code):
- Delete a share's dir before its record (Sweep, API delete): a crash must leave a dangling record, never an orphaned public dir.
- Renew rejects already-expired shares, because Sweep deletes from a snapshot without re-checking.
- Store mutations clone the slice, save `shares.json` (fsync + rename), then swap in memory, so memory is never ahead of disk.
- Public responses always carry `Cache-Control: private, no-store`, including 404s. It is set in a ResponseWriter wrapper because `http.FileServer` strips it from error responses.
- The public file system hides directories without a regular-file `index.html`: no listing anywhere.
- Extraction writes only regular files/dirs, requests modes 0644/0755 (ignores tar header modes), and caps uncompressed bytes (100MB, while reading) and entry count.
- The client validates TTL and path (folder has `index.html`) before opening any connection.
- The server binds both listeners before Reconcile/Sweep, so a second instance never touches live data.

Contracts:
- `server.API` assumes `<DataDir>/shares` and `<DataDir>/tmp` exist; only `runServer` creates them.
- `maxUploadBytes` in `cmd/go-share/server.go` must match the 100MB in README.
- Client archive and server extract agree on format (regular-file entries, slash-separated relative paths); `TestArchiveRoundTripsThroughExtract` guards it.
- Tests use `httptest` + `t.TempDir()`; safety tests name the guarantee they protect.
