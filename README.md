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
brew install --cask hackmajoris/homebrew-apps/sharefly
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

### Protect a share with a password

```
sharefly serve report.html --password
password: k7f3-x9qa-2mzt-c4wq (shown only now)
https://share.yourdomain.com/k7f3x9qa2m/report.html
```

The server generates the password and prints it once, on stderr, so `| pbcopy` still copies only the link. Visitors get a password page; after the right password, a cookie for that share keeps them in until the browser closes. Scripts can send it as HTTP Basic auth instead: `curl -u x:<password> <url>`. sharefly stores only a hash, so a lost password can't be shown again: `rm` the share and serve it again.

### Copy the link or open it right away

```
sharefly serve report.html | pbcopy          # link on the clipboard
open "$(sharefly serve report.html)"         # open it in your browser
```

### Make links public, instantly (quick tunnel)

No domain or Cloudflare account needed. sharefly runs `cloudflared` for you and uses the random address it gets, even if `public-url` is set in the config (only an explicit `--public-url` overrides it).

```
brew install cloudflared
sharefly config set tunnel quick
sharefly serve report.html     # → https://<random-words>.trycloudflare.com/<id>/report.html
```

For one run without changing the config: `sharefly serve report.html --tunnel quick`. The flag only applies when `serve` starts the server; if one is already running in another mode, `serve` refuses and tells you to `sharefly stop` first. The server keeps the tunnel until `sharefly stop`, so a later plain `serve` still gets a public link.

The address changes every time the server starts, so old links stop working after `sharefly stop`. Use your own domain for links that last.

### Make links public on your own domain

You need a domain on Cloudflare.

1. In the Cloudflare dashboard, go to Zero Trust → Networks → Tunnels and create a tunnel. On the install screen copy only the token (the long `eyJ…` string); don't run the install command. Add a public hostname, e.g. `share.yourdomain.com`, with service `HTTP` and URL `127.0.0.1:8080`. Leave Path empty.
2. Give sharefly the token and your domain. The token is a secret: it's stored in the config file, which only you can read, and is never shown on the command line.
   ```
   brew install cloudflared
   sharefly config set tunnel-token eyJ...
   sharefly config set public-url share.yourdomain.com
   sharefly config set tunnel token
   sharefly serve report.html     # → https://share.yourdomain.com/<id>/report.html
   ```

Token mode needs both the token and `public-url` (or `--public-url`); without either, the server refuses to start and `serve` says why before starting anything.

sharefly starts `cloudflared` together with its server, restarts it if it crashes, and stops it with `sharefly stop`; its output goes to the server log. If you installed cloudflared as a system service earlier, remove it (`sudo cloudflared service uninstall`) so two connectors don't run side by side.

### Share from all your devices

Pick one machine to host the shares (it needs the Cloudflare Tunnel from the previous recipe), and use Tailscale to reach it.

1. On the host, make the server listen on its tailnet address:
   ```
   sharefly config set api-addr "$(tailscale ip -4):8787"
   sharefly start
   ```
   The host's own `serve`, `ls`, `rm` and `renew` follow `api-addr` automatically, and `serve` still auto-starts the server there.
2. On every other device, install sharefly and point it at the host:
   ```
   sharefly config set server http://<host-tailscale-name-or-ip>:8787
   ```
3. `sharefly serve`, `ls`, `rm` and `renew` now work against the host. A remote server is never auto-started.

The server runs until you stop it, the host sleeps or it reboots. After that, run `sharefly start` again (or just `sharefly serve` on the host). Shares on disk are kept and served again as soon as it starts.

To have it start at boot and restart after a crash, run it under your system's service manager, e.g. a launchd LaunchDaemon on macOS or a systemd unit on Linux. Point the service at `sharefly server` (the foreground mode, which reads the same config file), not at `sharefly start`, which exits after launching the server.

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
| `can't reach sharefly server at ...` | For a remote server: check `tailscale status` and that the server is running. The `server` setting must include `http://` (`sharefly config` shows it). |
| `local server did not come up` | Read the log: `tail ~/.local/state/sharefly/server.log`. Usually ports 8787 or 8080 are taken: `lsof -iTCP:8787 -iTCP:8080 -sTCP:LISTEN`. |
| `a sharefly server is answering ... but has no pid file` | A server started by an older version or with another `--data-dir` is still running. Stop it with the `kill` command from the message. |
| Public link shows Cloudflare 502 | cloudflared can't reach the file server. The sharefly server must be running, and the tunnel's hostname must point at `127.0.0.1:8080`. |
| Public link shows Cloudflare 1033 | The tunnel is down. Look for `cloudflared:` lines in `~/.local/state/sharefly/server.log`; a wrong token shows up there. Check `sharefly config` for `tunnel` and `tunnel-token`. |
| Public link gives a DNS error | The domain isn't Active on Cloudflare yet, or the tunnel has no public hostname. |
| `tunnel is ... but cloudflared isn't installed` | `brew install cloudflared`. |
| Public link shows `404 page not found` | The share expired or was deleted. Check `sharefly ls`. |
| Links still show `127.0.0.1` | Check `sharefly config`. If the server was started with `--public-url`, that flag overrides the config file. |

## Reference

### Commands

```
sharefly serve <file|folder> [--ttl 7d] [--server URL] [--tunnel M] [--password]   # upload, print only the URL
sharefly ls [--server URL]                                                         # table: ID NAME EXPIRES URL
sharefly rm <id> [--server URL]                                                    # delete a share, prints nothing
sharefly rm --all [--server URL]                                                   # delete every share, prints nothing
sharefly renew <id> [--ttl 7d] [--server URL]                                      # reset expiry from now, prints the row
sharefly start [flags]                                                             # start the server in the background
sharefly server [flags]                                                            # run the server in the foreground
sharefly stop [--data-dir DIR]                                                     # stop the local server
sharefly config                                                                    # show settings and where they come from
sharefly config set <key> <value>                                                  # save a setting
sharefly config unset <key>                                                        # remove a setting
sharefly config open                                                               # edit the config file in your editor
```

- `--password` (serve): protect the share with a generated password, printed once on stderr.
- `--tunnel` (serve): tunnel for the server `serve` starts (`off`, `quick`, `token`). Error if a server already runs in another mode, or if `--server` is another machine.
- `--ttl`: `Nm` (minutes), `Nh` (hours), `Nd` (days), N a positive integer, or `never`. Default `7d`.
- Flags may come before or after the positional argument.
- A folder's URL points at the folder (`/<id>/`). A single file's URL points at the file (`/<id>/report.html`), except a lone `index.html`, which gets `/<id>/`.
- Auto-start: when the server address is local (`127.0.0.1`, `localhost`, `::1`) and nothing answers, `serve` starts `sharefly server --api-addr <that address>` in the background, logs to `<data-dir>/server.log`, and waits until it answers. If the server exits during startup, `serve` (and `start`) fail at once with the last lines of its log. `ls`, `rm` and `renew` never auto-start.
- Expiry times print in local time.
- Exit codes: 0 success, 1 error (message on stderr), 2 usage error.

### Configuration

Settings live in `~/.config/sharefly/config.json` (`$XDG_CONFIG_HOME/sharefly/config.json` if set). Change them with `sharefly config set <key> <value>`; `sharefly config` shows every setting, its effective value and whether it comes from the file or the default.

| Key | Default | Used by | |
|---|---|---|---|
| `public-url` | `http://<public-addr>` | server | base URL for share links |
| `server` | `http://<api-addr>` | `serve`, `ls`, `rm`, `renew`, `stop` | server API URL to talk to, with scheme |
| `data-dir` | `~/.local/state/sharefly` (`$XDG_STATE_HOME/sharefly` if set) | server, `stop` | holds `shares/`, `tmp/`, `shares.json`, `server.pid`, `server.args.json`, `server.log` |
| `api-addr` | `127.0.0.1:8787` | server | management API listen address; the tailnet IP accepts other devices |
| `public-addr` | `127.0.0.1:8080` | server | file server listen address (what cloudflared points at) |
| `tunnel` | `off` | server | `off`, `quick` (random trycloudflare.com URL; links use it automatically) or `token` (your tunnel) |
| `tunnel-token` | none | server | Cloudflare tunnel token for `tunnel=token`; secret, shown hidden, no flag |
| `ttl` | `7d` | `serve` | default link lifetime |

- `sharefly config open` opens the file in `$VISUAL` or `$EDITOR` (e.g. `EDITOR="code --wait"`), or the system's default app if neither is set. The file lists every key with its default filled in, using underscores (`public_url`). `public_url` and `server` stay empty unless you set them, which means they follow `public_addr` and `api_addr`. After a terminal editor exits, the file is checked and a running local server restarts if a listen address or `data_dir` changed. Unknown keys and invalid values are rejected by every command except `config set` and `config open`, so you can always repair the file. A URL without a scheme is accepted: `public-url` gets `https://` and `server` gets `http://`.
- An explicit `--tunnel off` (on `serve`, `start` or `server`) means local links: the config `public-url` is ignored for that server, since nothing would serve it. `tunnel off` from the config file keeps `public-url`, for a proxy you run yourself. An explicit `--public-url` wins over both.
- Every key except `tunnel-token` has a matching flag (`--public-url`, `--server`, `--data-dir`, `--api-addr`, `--public-addr`, `--ttl`, `--tunnel`) that overrides the config file for one command, e.g. `sharefly start --tunnel quick`. The token has no flag so it never shows up in `ps`.
- A running server reads `public-url` from the file whenever the file changes, however you edit it, unless it was started with `--public-url`. Setting another server key (`data-dir`, `api-addr`, `public-addr`, `tunnel`, `tunnel-token`) restarts a running local server with the flags it was started with, so the change applies at once. Changing `data-dir` doesn't move existing shares; the command prints where they are.

### Server

`sharefly start` launches the server in the background, waits until it answers, prints its pid and log path, and returns; if one is already running it says so. `sharefly server` runs in the foreground until Ctrl-C or SIGTERM, to watch its log or to run it under your own service manager. Both read the config file and accept `--api-addr`, `--public-addr`, `--public-url`, `--data-dir` and `--tunnel`. With a tunnel, the server also runs `cloudflared`; in quick mode it waits up to 30s for the tunnel address before answering, so the first link is already public. While running, the server writes `<data-dir>/server.pid` (used by `stop`) and `<data-dir>/server.args.json` (used to restart it after a config change), and logs to `<data-dir>/server.log` when started in the background.

### HTTP API

The management API on `--api-addr` is plain HTTP + JSON. The CLI uses it; any other client can too.

| Method | Path | Success | Errors |
|---|---|---|---|
| `POST` | `/shares?ttl=7d&name=report.html[&password=1]` (tar.gz body; missing `ttl` = `7d`) | 201 record + `url` (+ `password` once) | 400 bad ttl, archive or `password` value, 413 body or uncompressed size over 100MB, 500 |
| `GET` | `/shares` | 200 array of record + `url` | |
| `DELETE` | `/shares/{id}` | 204 | 404, 500 |
| `POST` | `/shares/{id}/renew` body `{"ttl":"7d"}` | 200 record + `url` | 400, 404 (also for an already-expired share), 500 |

Record: `{"id","name","entry","size","created_at","expires_at","url","protected"}`; the upload response of a protected share also has `"password"`, and no response ever has it again. `entry` is the path opened by `url`, relative to the share (`""` = its `index.html`); `size` is uncompressed bytes; `expires_at: null` means never. Errors are `{"error":"..."}`.

The archive may hold only regular files and directories with relative paths, at most 10000 entries, and no entry may conflict with an earlier one (a file `a` followed by `a/b`).

## Good to know

- **Access:** each share gets a random 10-character ID. The unguessable link is the only access control, unless the share was served with `--password` (80-bit generated password, asked on a password page and kept in a cookie scoped to that share). When the server listens on its tailnet address, anyone on your tailnet can create and delete shares.
- **Expiry:** expired shares are swept at startup and every 5 minutes. `rm` takes effect immediately; responses carry `Cache-Control: private, no-store`, so Cloudflare never serves stale copies.
- **Limits:** 100MB per upload (compressed and uncompressed), 10000 files.
- **What gets uploaded:** empty folders aren't. `.git` and `.DS_Store` are skipped. Inside a folder, symlinks and other non-regular files are skipped with a warning, and a symlinked root `index.html` is rejected. The path you pass to `serve` may itself be a symlink; it is followed.
- **Data folder:** at startup the server deletes anything under `<data-dir>/shares/` that has no record and empties `<data-dir>/tmp/`. Don't put files there by hand.
- **Network:** the file server binds `127.0.0.1` and is reached publicly only through cloudflared. With `tunnel` set, sharefly runs the `cloudflared` binary itself; it never calls the Cloudflare API.

## Development

```
make            # test + build
make check      # vet, lint, tests, gofmt check
make run        # local server with data in .bin/data
make install    # symlink .bin/sharefly into /usr/local/bin
```
