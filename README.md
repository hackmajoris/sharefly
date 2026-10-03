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
- Expired shares are swept at startup and every 5 minutes.
- go-share never talks to cloudflared or the Cloudflare API. The tunnel is configured in the Cloudflare dashboard.

## Build / install

Requires Go 1.27+.

```
go build -o go-share ./cmd/go-share
sudo install -m 755 go-share /usr/local/bin/go-share
```

Install the same binary on the mini and on each client.

## Client usage

```
go-share serve <file|folder> [--ttl 7d] [--server URL]   # upload, print public URL
go-share ls [--server URL]                               # list shares
go-share rm <id> [--server URL]                          # delete a share
go-share renew <id> [--ttl 7d] [--server URL]            # reset expiry from now
```

- `--ttl`: `Nd`, `Nh`, `Nm` (positive integer) or `never`. Default `7d`.
- Server address: `--server` flag, else `GO_SHARE_SERVER` env, else `http://macmini:8787`.
- Flags may come before or after the positional argument.
- A folder must contain `index.html` at its root; the URL points at the folder. A single file's URL points at the file.

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

## One-time mini setup

1. **Tunnel.** Cloudflare dashboard: Zero Trust → Networks → Tunnels → create a tunnel. Add a public hostname `share.yourdomain.com` → `http://localhost:8080`. Copy the tunnel token. The token is a secret: anyone holding it can run your tunnel.
2. **cloudflared.**
   ```
   brew install cloudflared
   sudo cloudflared service install <TOKEN>
   ```
3. **Binary.** Install `go-share` to `/usr/local/bin/go-share` (see above).
4. **Data dir.** Create it before loading the plist. launchd does not create the log file's parent directory and the job will fail to start without it.
   ```
   mkdir -p ~/go-share
   ```
5. **Plist.** Edit `deploy/com.go-share.server.plist` and replace the placeholders: `YOUR_USER` (in `UserName`, `--data-dir` and both log paths), `TAILSCALE_IP` (from `tailscale ip -4`), `https://share.yourdomain.com`, and the binary path if not `/usr/local/bin/go-share`. Logs go to `<data-dir>/server.log`.
   ```
   sudo cp deploy/com.go-share.server.plist /Library/LaunchDaemons/
   sudo chown root:wheel /Library/LaunchDaemons/com.go-share.server.plist
   sudo launchctl bootstrap system /Library/LaunchDaemons/com.go-share.server.plist
   ```
   It runs as a LaunchDaemon so it starts at boot without a login. To reload after edits: `sudo launchctl bootout system/com.go-share.server`, then bootstrap again.
6. **No sleep.** System Settings → Energy → prevent automatic sleeping.

## Caveats

- Use relative links in shared HTML. Shares live under `/<id>/`, so absolute paths like `/style.css` break.
- Empty folders are not uploaded.
- Symlinks and other non-regular files are skipped with a warning. `.git/` and `.DS_Store` are skipped.
- The tailnet is the API's auth. Anyone on your tailnet can create and delete shares.
- Upload limit is 100MB (compressed and uncompressed).
- Default TTL is 7 days. Use `--ttl never` to keep a share until `rm`.
- Responses carry `Cache-Control: private, no-store` so `rm` and expiry take effect immediately instead of Cloudflare serving cached copies.
