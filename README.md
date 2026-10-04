# sharefly

Share a static HTML file or folder via a link. Runs locally with zero config; add a Cloudflare Tunnel to make links public, or run it on an always-on home server (e.g. a Mac mini) and share from any device on your tailnet.

```
$ sharefly serve report.html
started local sharefly server (pid 4242, log ~/.local/state/sharefly/server.log)
http://127.0.0.1:8080/k7f3x9qa2m/report.html
```

One binary. `serve` starts a local server in the background when none is running; `sharefly stop` stops it. Standard library only.

## Quick start

1. **Local only:** `sharefly serve report.html`. That's it; the link works on this machine.
2. **Public:** run cloudflared on the same machine with a public hostname pointing at `http://127.0.0.1:8080`, and set `export SHAREFLY_PUBLIC_URL=https://share.yourdomain.com` (e.g. in `~/.zshrc`) so links use it. Run `sharefly stop` once so the next `serve` restarts the server with the new URL.
3. **Always-on home server:** run `sharefly start --api-addr <tailscale-ip>:8787 --public-url https://share.yourdomain.com` under launchd on the mini (see [One-time mini setup](#one-time-mini-setup)), and set `SHAREFLY_SERVER=http://<mini>:8787` on your laptops.

## Architecture

```
laptop                                 mac mini
sharefly serve x.html ──tailnet──▶ sharefly start
                                   ├─ API    <tailscale-ip>:8787  (tailnet only)
                                   ├─ files  127.0.0.1:8080       (no dir listing,
                                   │                               Cache-Control: private, no-store)
                                   └─ <data-dir>/{shares/, tmp/, shares.json}
                cloudflared service (token) ──▶ 127.0.0.1:8080
                https://share.yourdomain.com/<id>/
```

- In the home-server setup the management API listens only on the tailnet address (by default it binds `127.0.0.1`). The file server binds to loopback and is reached only through cloudflared.
- Each share gets a random 10-char ID. The unguessable link is the only access control on the public side.
- Expired shares are swept at startup and every 5 minutes, so an expired share can stay reachable for up to 5 minutes.
- At startup the server deletes anything under `<data-dir>/shares/` that has no record and empties `<data-dir>/tmp/`. If that cleanup fails, the server exits with an error instead of serving (launchd retries). Don't put files there by hand.
- sharefly never talks to cloudflared or the Cloudflare API. The tunnel is configured in the Cloudflare dashboard.

## Install

```
brew install --cask hackmajoris/apps/sharefly
```

Install it on the mini and on each client. The binary lands in `$(brew --prefix)/bin/sharefly`: `/opt/homebrew/bin` on Apple Silicon, `/usr/local/bin` on Intel. Upgrade with `brew upgrade --cask sharefly`.

From source (Go 1.27.1+):

```
go build -o sharefly ./cmd/sharefly
```

## Release

Push a `v*` tag. The release workflow runs GoReleaser, which publishes the GitHub release and updates the cask in `hackmajoris/homebrew-apps`. It needs a `GORELEASER_GITHUB_TOKEN` secret with write access to both repos.

## Client usage

```
sharefly serve <file|folder> [--ttl 7d] [--server URL]   # upload, print only the URL (auto-starts a local server)
sharefly ls [--server URL]                               # table: ID NAME EXPIRES URL
sharefly rm <id> [--server URL]                          # delete a share, prints nothing
sharefly renew <id> [--ttl 7d] [--server URL]            # reset expiry from now, prints the row
sharefly stop [--data-dir DIR]                           # stop the local server
```

- `--ttl`: `Nd` (days), `Nh` (hours), `Nm` (minutes), N a positive integer, or `never`. Default `7d`.
- Server address: `--server` flag, else `SHAREFLY_SERVER` env, else `http://127.0.0.1:8787` (this machine). It must be a full URL with scheme (`http://host:8787`, not `host:8787`).
- Auto-start: when the server address is local (`127.0.0.1`, `localhost`, `::1`) and nothing answers, `serve` starts `sharefly start --api-addr <that address>` in the background, logging to `~/.local/state/sharefly/server.log`, and waits up to 5s for it. Remote servers are never started; their connection errors are reported as-is. `ls`, `rm` and `renew` don't auto-start.
- The local server lives as long as the machine stays awake; on a laptop, closing the lid stops the links and the expiry sweep. Use the home-server setup for always-on links.
- Flags may come before or after the positional argument.
- A folder must contain `index.html` at its root; the URL points at the folder (`/<id>/`). A single file's URL points at the file (`/<id>/report.html`), except a lone `index.html`, which gets `/<id>/`.
- Expiry times print in local time.
- Exit codes: 0 success, 1 error (message on stderr), 2 usage error.

## Server

```
sharefly start                                                                       # local, zero config
sharefly start --api-addr <tailscale-ip>:8787 --public-url https://share.yourdomain.com  # home server
```

Runs in the foreground until Ctrl-C or SIGTERM. `server` is an alias for `start`. While running it writes `<data-dir>/server.pid`, which `sharefly stop` uses.

| Flag | Default | |
|---|---|---|
| `--api-addr` | `127.0.0.1:8787` | management API listen address; use the tailnet IP to accept other devices |
| `--public-url` | `$SHAREFLY_PUBLIC_URL`, else `http://<public-addr>` | base URL used to build share links |
| `--public-addr` | `127.0.0.1:8080` | public file server listen address |
| `--data-dir` | `$XDG_STATE_HOME/sharefly`, else `~/.local/state/sharefly` | holds `shares/`, `tmp/`, `shares.json`, `server.pid`, `server.log` |

## API

The management API on `--api-addr` is plain HTTP + JSON. The CLI uses it; any other client can too.

| Method | Path | Success | Errors |
|---|---|---|---|
| `POST` | `/shares?ttl=7d&name=report.html` (tar.gz body; missing `ttl` = `7d`) | 201 record + `url` | 400 bad ttl or archive, 413 body or uncompressed size over 100MB, 500 |
| `GET` | `/shares` | 200 array of record + `url` | |
| `DELETE` | `/shares/{id}` | 204 | 404, 500 |
| `POST` | `/shares/{id}/renew` body `{"ttl":"7d"}` | 200 record + `url` | 400, 404 (also for an already-expired share), 500 |

Record: `{"id","name","entry","size","created_at","expires_at","url"}`. `entry` is the path opened by `url`, relative to the share (`""` = its `index.html`); `size` is uncompressed bytes; `expires_at: null` means never. Errors are `{"error":"..."}`.

The archive may hold only regular files and directories with relative paths, at most 10000 entries, and no entry may conflict with an earlier one (a file `a` followed by `a/b`).

## One-time mini setup

1. **Tunnel.** Cloudflare dashboard: Zero Trust → Networks → Tunnels → create a tunnel. Add a public hostname `share.yourdomain.com` → `http://127.0.0.1:8080`. Copy the tunnel token. The token is a secret: anyone holding it can run your tunnel.
2. **cloudflared.**
   ```
   brew install cloudflared
   sudo cloudflared service install <TOKEN>
   ```
3. **Binary.** `brew install --cask hackmajoris/apps/sharefly` (see above).
4. **Data dir.** Create it as `YOUR_USER` (not with `sudo`) before loading the plist. launchd does not create the log file's parent directory, and the server, which runs as `YOUR_USER`, must be able to write it.
   ```
   mkdir -p ~/.local/state/sharefly
   ```
5. **Plist.** Edit `deploy/com.sharefly.server.plist` and replace the placeholders: `YOUR_USER` (in `UserName`, `--data-dir` and both log paths), `TAILSCALE_IP` (from `tailscale ip -4`), `https://share.yourdomain.com`, and the binary path if not `/opt/homebrew/bin/sharefly` (Intel: `/usr/local/bin/sharefly`). After `brew upgrade --cask sharefly`, restart with `sudo launchctl kickstart -k system/com.sharefly.server`. Logs go to `<data-dir>/server.log`, which is never rotated; truncate it now and then (`: > ~/.local/state/sharefly/server.log`) or add a `newsyslog` rule.
   ```
   sudo cp deploy/com.sharefly.server.plist /Library/LaunchDaemons/
   sudo chown root:wheel /Library/LaunchDaemons/com.sharefly.server.plist
   sudo launchctl bootstrap system /Library/LaunchDaemons/com.sharefly.server.plist
   ```
   It runs as a LaunchDaemon so it starts at boot without a login, but it can only bind `--api-addr` once the tailnet IP exists. The Tailscale GUI app connects only after a user logs in, so until then launchd keeps retrying and the public listener is down too. For a true headless boot, run Tailscale as a system daemon (`brew install tailscale`, `sudo tailscaled install-system-daemon`, `tailscale up`) or enable auto-login. To reload after edits: `sudo launchctl bootout system/com.sharefly.server`, then bootstrap again.
6. **No sleep.** System Settings → Energy → prevent automatic sleeping.

## Caveats

- Use relative links in shared HTML. Shares live under `/<id>/`, so absolute paths like `/style.css` break.
- Empty folders are not uploaded.
- Inside a folder, symlinks (including symlinked subfolders, skipped whole) and other non-regular files are skipped with a warning. A symlinked root `index.html` is rejected. The path you pass to `serve` may itself be a symlink; it is followed. `.git` (directory or worktree/submodule file) and `.DS_Store` are skipped.
- The tailnet is the API's auth. Anyone on your tailnet can create and delete shares.
- Upload limit is 100MB (compressed and uncompressed).
- Default TTL is 7 days. Use `--ttl never` to keep a share until `rm`.
- Responses carry `Cache-Control: private, no-store` so Cloudflare never serves cached copies: `rm` takes effect immediately, expiry at the next sweep (within 5 minutes).
