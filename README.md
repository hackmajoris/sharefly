# sharefly

Share a static HTML file or folder via a link, straight from your terminal.

```
$ sharefly serve report.html
started local sharefly server (pid 4242, log ~/.local/state/sharefly/server.log)
http://127.0.0.1:8080/k7f3x9qa2m/report.html
```

Works locally with zero config. Add a Cloudflare Tunnel and the links work for anyone on the internet. Start it on one machine with Tailscale and you can share from all your devices.

- [Install](#install)
- [Cookbook](#cookbook): copy-paste recipes for common tasks
- [Reference](#reference): commands, environment, server flags, HTTP API
- [Good to know](#good-to-know)

## Install

```
brew install --cask hackmajoris/apps/sharefly
```

Upgrade with `brew upgrade --cask sharefly`. From source (Go 1.27.1+): `make build`, which puts the binary in `.bin/sharefly`.

## Cookbook

### Share a single page

```
sharefly serve report.html
```

The first `serve` starts a background server for you. The command prints only the link, so it's easy to script.

### Share a whole site

```
sharefly serve ./site
```

The folder needs an `index.html` at its root. Links in your HTML must be relative (`style.css`, not `/style.css`), because every share lives under its own `/<id>/` path.

### Pick how long a link lives

```
sharefly serve report.html --ttl 1h      # minutes: 30m, hours: 1h, days: 3d
sharefly serve report.html --ttl never   # until you delete it
```

The default is 7 days. Expired links disappear within 5 minutes.

### See, extend and delete links

```
sharefly ls                        # ID, name, expiry and URL of every share
sharefly renew k7f3x9qa2m --ttl 7d # expire 7 days from now
sharefly rm k7f3x9qa2m             # gone immediately
```

### Copy the link or open it right away

```
sharefly serve report.html | pbcopy          # link on the clipboard
open "$(sharefly serve report.html)"         # open it in your browser
```

### Make links public with a Cloudflare Tunnel

You need a domain on Cloudflare.

1. In the Cloudflare dashboard, go to Zero Trust → Networks → Tunnels and create a tunnel. Add a public hostname, e.g. `share.yourdomain.com`, with service `HTTP` and URL `127.0.0.1:8080`. Leave Path empty.
2. On the machine that runs sharefly, install the connector with the token the dashboard shows. The token is a secret.
   ```
   brew install cloudflared
   sudo cloudflared service install <TOKEN>
   ```
3. Tell sharefly to build links with your domain, then restart its server:
   ```
   echo 'export SHAREFLY_PUBLIC_URL=https://share.yourdomain.com' >> ~/.zshrc
   source ~/.zshrc
   sharefly stop
   sharefly serve report.html     # → https://share.yourdomain.com/<id>/report.html
   ```

### Share from all your devices

Pick one machine to host the shares (it needs the Cloudflare Tunnel from the previous recipe), and use Tailscale to reach it.

1. On the host, start the server on its tailnet address:
   ```
   echo "export SHAREFLY_SERVER=http://$(tailscale ip -4):8787" >> ~/.zshrc
   source ~/.zshrc
   sharefly start --api-addr "$(tailscale ip -4):8787"
   ```
   The host needs `SHAREFLY_SERVER` too: the API now listens only on the tailnet address, so a plain `serve` there would otherwise look for a server on `127.0.0.1`.
2. On every other device, install sharefly and point it at the host:
   ```
   echo 'export SHAREFLY_SERVER=http://<host-tailscale-name-or-ip>:8787' >> ~/.zshrc
   ```
3. `sharefly serve`, `ls`, `rm` and `renew` now work against the host. A remote server is never auto-started.

The server runs until you stop it, the host sleeps or it reboots. After that, run the `sharefly start` command from step 1 again. Shares on disk are kept and served again as soon as it starts.

### Start and stop the server yourself

```
sharefly start                                        # background; returns once it answers
sharefly start --public-url https://share.example.com # with flags, e.g. a public URL
sharefly stop
```

You rarely need `start`: `serve` starts the server when none is running. Use `sharefly server` instead to keep it in the foreground and watch its log.

### Troubleshooting

| Symptom | What to do |
|---|---|
| `can't reach sharefly server at ...` | For a remote server: check `tailscale status` and that the server is running. `SHAREFLY_SERVER` must include `http://`. |
| `local server did not come up` | Read the log: `tail ~/.local/state/sharefly/server.log`. Usually ports 8787 or 8080 are taken: `lsof -iTCP:8787 -iTCP:8080 -sTCP:LISTEN`. |
| `a sharefly server is answering ... but has no pid file` | A server started by an older version or with another `--data-dir` is still running. Stop it with the `kill` command from the message. |
| Public link shows Cloudflare 502 | cloudflared can't reach the file server. sharefly must be running on the same machine as cloudflared, and the tunnel's hostname must point at `127.0.0.1:8080`. |
| Public link shows Cloudflare 1033 | The tunnel is down. Check the connector: `sudo cloudflared service uninstall`, then install it again with the token from the dashboard. |
| Public link gives a DNS error | The domain isn't Active on Cloudflare yet, or the tunnel has no public hostname. |
| Public link shows `404 page not found` | The share expired or was deleted. Check `sharefly ls`. |
| Links still show `127.0.0.1` after setting `SHAREFLY_PUBLIC_URL` | Run `sharefly stop`; the server reads the variable only when it starts. |

## Reference

### Commands

```
sharefly serve <file|folder> [--ttl 7d] [--server URL]   # upload, print only the URL
sharefly ls [--server URL]                               # table: ID NAME EXPIRES URL
sharefly rm <id> [--server URL]                          # delete a share, prints nothing
sharefly renew <id> [--ttl 7d] [--server URL]            # reset expiry from now, prints the row
sharefly start [flags]                                   # start the server in the background
sharefly server [flags]                                  # run the server in the foreground
sharefly stop [--data-dir DIR]                           # stop the local server
```

- `--ttl`: `Nm` (minutes), `Nh` (hours), `Nd` (days), N a positive integer, or `never`. Default `7d`.
- Flags may come before or after the positional argument.
- A folder's URL points at the folder (`/<id>/`). A single file's URL points at the file (`/<id>/report.html`), except a lone `index.html`, which gets `/<id>/`.
- Auto-start: when the server address is local (`127.0.0.1`, `localhost`, `::1`) and nothing answers, `serve` starts `sharefly server --api-addr <that address>` in the background, logs to `<data-dir>/server.log`, and waits up to 5s. `ls`, `rm` and `renew` never auto-start.
- Expiry times print in local time.
- Exit codes: 0 success, 1 error (message on stderr), 2 usage error.

### Environment

| Variable | Used by | Default | |
|---|---|---|---|
| `SHAREFLY_SERVER` | `serve`, `ls`, `rm`, `renew`, `stop` | `http://127.0.0.1:8787` | server API URL, with scheme; `--server` overrides it |
| `SHAREFLY_PUBLIC_URL` | `start`, `server`, including the auto-started server | `http://<public-addr>` | base URL for share links; `--public-url` overrides it |
| `XDG_STATE_HOME` | `start`, `server`, `stop`, auto-start | `~/.local/state` | data lives in `$XDG_STATE_HOME/sharefly`; `--data-dir` overrides it |

### Server flags

Both commands take the same flags. `sharefly start` launches the server in the background, waits until it answers, prints its pid and log path, and returns; if one is already running it says so. `sharefly server` runs in the foreground until Ctrl-C or SIGTERM, to watch its log or to run it under your own service manager. While running, the server writes `<data-dir>/server.pid`, which `stop` uses, and logs to `<data-dir>/server.log` when started in the background.

| Flag | Default | |
|---|---|---|
| `--api-addr` | `127.0.0.1:8787` | management API listen address; use the tailnet IP to accept other devices |
| `--public-url` | `$SHAREFLY_PUBLIC_URL`, else `http://<public-addr>` | base URL used to build share links |
| `--public-addr` | `127.0.0.1:8080` | file server listen address (what cloudflared points at) |
| `--data-dir` | `$XDG_STATE_HOME/sharefly`, else `~/.local/state/sharefly` | holds `shares/`, `tmp/`, `shares.json`, `server.pid`, `server.log` |

### HTTP API

The management API on `--api-addr` is plain HTTP + JSON. The CLI uses it; any other client can too.

| Method | Path | Success | Errors |
|---|---|---|---|
| `POST` | `/shares?ttl=7d&name=report.html` (tar.gz body; missing `ttl` = `7d`) | 201 record + `url` | 400 bad ttl or archive, 413 body or uncompressed size over 100MB, 500 |
| `GET` | `/shares` | 200 array of record + `url` | |
| `DELETE` | `/shares/{id}` | 204 | 404, 500 |
| `POST` | `/shares/{id}/renew` body `{"ttl":"7d"}` | 200 record + `url` | 400, 404 (also for an already-expired share), 500 |

Record: `{"id","name","entry","size","created_at","expires_at","url"}`. `entry` is the path opened by `url`, relative to the share (`""` = its `index.html`); `size` is uncompressed bytes; `expires_at: null` means never. Errors are `{"error":"..."}`.

The archive may hold only regular files and directories with relative paths, at most 10000 entries, and no entry may conflict with an earlier one (a file `a` followed by `a/b`).

## Good to know

- **Access:** each share gets a random 10-character ID. The unguessable link is the only access control. When the server listens on its tailnet address, anyone on your tailnet can create and delete shares.
- **Expiry:** expired shares are swept at startup and every 5 minutes. `rm` takes effect immediately; responses carry `Cache-Control: private, no-store`, so Cloudflare never serves stale copies.
- **Limits:** 100MB per upload (compressed and uncompressed), 10000 files.
- **What gets uploaded:** empty folders aren't. `.git` and `.DS_Store` are skipped. Inside a folder, symlinks and other non-regular files are skipped with a warning, and a symlinked root `index.html` is rejected. The path you pass to `serve` may itself be a symlink; it is followed.
- **Data folder:** at startup the server deletes anything under `<data-dir>/shares/` that has no record and empties `<data-dir>/tmp/`. Don't put files there by hand.
- **Network:** the file server binds `127.0.0.1` and is reached publicly only through cloudflared. sharefly never talks to cloudflared or the Cloudflare API.

## Development

```
make            # test + build
make check      # vet, lint, tests, gofmt check
make run        # local server with data in .bin/data
make install    # symlink .bin/sharefly into /usr/local/bin
```
