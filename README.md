# go-share

Share a static HTML file or folder from any device on your tailnet via a public URL served from a home server (e.g. a Mac mini) through a Cloudflare Tunnel.

```
$ go-share serve report.html
https://share.yourdomain.com/k7f3x9qa2m/report.html
```

One binary, two roles: `go-share server` on the mini, `serve`/`ls`/`rm`/`renew` on laptops. Standard library only.

## Architecture

```
laptop                                 mac mini
go-share serve x.html ──tailnet──▶ go-share server
                                   ├─ API    <tailscale-ip>:8787  (tailnet only)
                                   ├─ files  127.0.0.1:8080       (no dir listing,
                                   │                               Cache-Control: private, no-store)
                                   └─ <data-dir>/{shares/, tmp/, shares.json}
                cloudflared service (token) ──▶ 127.0.0.1:8080
                https://share.yourdomain.com/<id>/
```

- The management API listens only on the tailnet address. The file server binds to loopback and is reached only through cloudflared.
- Each share gets a random 10-char ID. The unguessable link is the only access control on the public side.
- Expired shares are swept at startup and every 5 minutes, so an expired share can stay reachable for up to 5 minutes.
- At startup the server deletes anything under `<data-dir>/shares/` that has no record and empties `<data-dir>/tmp/`. Don't put files there by hand.
- go-share never talks to cloudflared or the Cloudflare API. The tunnel is configured in the Cloudflare dashboard.

## Build / install

Requires Go 1.27.1+.

```
go build -o go-share ./cmd/go-share
sudo install -m 755 go-share /usr/local/bin/go-share
```

Install the same binary on the mini and on each client.

## Client usage

```
go-share serve <file|folder> [--ttl 7d] [--server URL]   # upload, print only the public URL
go-share ls [--server URL]                               # table: ID NAME EXPIRES URL
go-share rm <id> [--server URL]                          # delete a share, prints nothing
go-share renew <id> [--ttl 7d] [--server URL]            # reset expiry from now, prints the row
```

- `--ttl`: `Nd` (days), `Nh` (hours), `Nm` (minutes), N a positive integer, or `never`. Default `7d`.
- Server address: `--server` flag, else `GO_SHARE_SERVER` env, else `http://macmini:8787`. It must be a full URL with scheme (`http://host:8787`, not `host:8787`). The default relies on the MagicDNS name `macmini`.
- Flags may come before or after the positional argument.
- A folder must contain `index.html` at its root; the URL points at the folder (`/<id>/`). A single file's URL points at the file (`/<id>/report.html`), except a lone `index.html`, which gets `/<id>/`.
- Expiry times print in local time.
- Exit codes: 0 success, 1 error (message on stderr), 2 usage error.

## Server

```
go-share server --api-addr <tailscale-ip>:8787 --public-url https://share.yourdomain.com
```

| Flag | Default | |
|---|---|---|
| `--api-addr` | (required) | management API listen address, use the tailnet IP |
| `--public-url` | (required) | public base URL used to build share links |
| `--public-addr` | `127.0.0.1:8080` | public file server listen address |
| `--data-dir` | `~/go-share` | holds `shares/`, `tmp/`, `shares.json` |

## API

The management API on `--api-addr` is plain HTTP + JSON. The CLI uses it; any other client can too.

| Method | Path | Success | Errors |
|---|---|---|---|
| `POST` | `/shares?ttl=7d&name=report.html` (tar.gz body; missing `ttl` = `7d`) | 201 record + `url` | 400 bad ttl or archive, 413 body or uncompressed size over 100MB, 500 |
| `GET` | `/shares` | 200 array of record + `url` | |
| `DELETE` | `/shares/{id}` | 204 | 404, 500 |
| `POST` | `/shares/{id}/renew` body `{"ttl":"7d"}` | 200 record + `url` | 400, 404 (also for an already-expired share), 500 |

Record: `{"id","name","entry","size","created_at","expires_at","url"}`. `entry` is the path opened by `url`, relative to the share (`""` = its `index.html`); `size` is uncompressed bytes; `expires_at: null` means never. Errors are `{"error":"..."}`.

The archive may hold only regular files and directories with relative paths, at most 10000 entries.

## One-time mini setup

1. **Tunnel.** Cloudflare dashboard: Zero Trust → Networks → Tunnels → create a tunnel. Add a public hostname `share.yourdomain.com` → `http://127.0.0.1:8080`. Copy the tunnel token. The token is a secret: anyone holding it can run your tunnel.
2. **cloudflared.**
   ```
   brew install cloudflared
   sudo cloudflared service install <TOKEN>
   ```
3. **Binary.** Install `go-share` to `/usr/local/bin/go-share` (see above).
4. **Data dir.** Create it as `YOUR_USER` (not with `sudo`) before loading the plist. launchd does not create the log file's parent directory, and the server, which runs as `YOUR_USER`, must be able to write it.
   ```
   mkdir -p ~/go-share
   ```
5. **Plist.** Edit `deploy/com.go-share.server.plist` and replace the placeholders: `YOUR_USER` (in `UserName`, `--data-dir` and both log paths), `TAILSCALE_IP` (from `tailscale ip -4`), `https://share.yourdomain.com`, and the binary path if not `/usr/local/bin/go-share`. Logs go to `<data-dir>/server.log`, which is never rotated; truncate it now and then (`: > ~/go-share/server.log`) or add a `newsyslog` rule.
   ```
   sudo cp deploy/com.go-share.server.plist /Library/LaunchDaemons/
   sudo chown root:wheel /Library/LaunchDaemons/com.go-share.server.plist
   sudo launchctl bootstrap system /Library/LaunchDaemons/com.go-share.server.plist
   ```
   It runs as a LaunchDaemon so it starts at boot without a login, but it can only bind `--api-addr` once the tailnet IP exists. The Tailscale GUI app connects only after a user logs in, so until then launchd keeps retrying and the public listener is down too. For a true headless boot, run Tailscale as a system daemon (`brew install tailscale`, `sudo tailscaled install-system-daemon`, `tailscale up`) or enable auto-login. To reload after edits: `sudo launchctl bootout system/com.go-share.server`, then bootstrap again.
6. **No sleep.** System Settings → Energy → prevent automatic sleeping.

## Caveats

- Use relative links in shared HTML. Shares live under `/<id>/`, so absolute paths like `/style.css` break.
- Empty folders are not uploaded.
- Inside a folder, symlinks (including symlinked subfolders, skipped whole) and other non-regular files are skipped with a warning. A symlinked root `index.html` is rejected. The path you pass to `serve` may itself be a symlink; it is followed. `.git/` and `.DS_Store` are skipped.
- The tailnet is the API's auth. Anyone on your tailnet can create and delete shares.
- Upload limit is 100MB (compressed and uncompressed).
- Default TTL is 7 days. Use `--ttl never` to keep a share until `rm`.
- Responses carry `Cache-Control: private, no-store` so Cloudflare never serves cached copies: `rm` takes effect immediately, expiry at the next sweep (within 5 minutes).
